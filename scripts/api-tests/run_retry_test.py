"""重试语义活体套件（T-retry-001）—— 只用本地 mock 上游, 不打真实上游、不产生费用。

被验证的三件事（前两件来自上游 issue, 后一件来自用户日志包）:
  1. 上游持续不可用时不再无限重试（上游 #388 手动模式无限重试 / #338 客户端超时后后台持续空转）:
     单请求尝试次数有上限, 到点给客户端一个明确的失败响应而不是让它自己超时。
  2. 确定性错误不再被反复重试（上游 #375）:
     - 请求本身非法（400/422 等）: 不计成员失败、不打冷却, 换成员继续; 全部成员都拒绝才结束。
     - 成员自身问题（401/403/404 等）: 记失败并立即冷却换人, 不耗尽该成员的尝试次数。
  3. developer 角色归一化为 system（上游 PR #360 对 #19 回归的修复）:
     客户端用 developer 携带指令时, 上游收到的是等价的 system。

用例:
  R1 可恢复失败（500）在尝试上限处终结: HTTP 502, 上游只收到 attemptCap 次尝试（不是无限）。
  R2 请求本身非法且只有这一个成员: 只尝试 1 次就以 502 结束。
  R3 请求本身非法但还有别的成员: 换成员后成功（2 次尝试）, 且被拒绝的成员不计失败。
  R4 成员自身问题（401）: 换成员后成功（2 次尝试, 不耗尽该成员次数）, 且该成员计 1 次失败。
  R5 手动模式同样有上限: 钉住的成员一直 500 时以 502 结束, 不再每秒重试。
  R6 developer 角色被改写为 system（读 mock 落盘的请求正文核对）。
  R7 客户端凭据不会被透传给上游（上游收到的是渠道密钥, 上游 #372 同类的头部处理）。

Run: python run_retry_test.py    （实例 + mock 必须在跑）
"""

import hashlib
import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY = "http://%s:%s" % (os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1"),
                          os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
DB = os.environ.get("OCTOPUS_DB", r"D:\奇怪的软件\octopus\data\data.db")
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")

CHANNEL = "DS-TEST-retry"
GOOD, BAD, REJECT400, REJECT401 = "mock-good", "mock-bad", "mock-reject400", "mock-reject401"
CONV = "mock-chatonly"  # 转换路径专用成员: 它只说 messages, chat 客户端命中它时必然被转换
RESP = "mock-plain"     # 转换路径专用成员: 它只说 responses, 用来覆盖 Responses 形状的请求体
GROUP_CAP, GROUP_400_SOLO, GROUP_400_PAIR, GROUP_401_PAIR, GROUP_MANUAL, GROUP_DEV = (
    "DS-TEST-retrycap", "DS-TEST-retry400solo", "DS-TEST-retry400pair",
    "DS-TEST-retry401pair", "DS-TEST-retrymanual", "DS-TEST-retrydev")
GROUP_CONV = "DS-TEST-retryconv"
GROUP_RESP = "DS-TEST-retryresp"
KEY_MAIN, KEY_CONV, KEY_RESP = "mockkey", "convkey", "respkey"
CHANNEL_KEY_SECRET = "mock-secret-not-a-real-key"
CONV_KEY_SECRET = "mock-secret-conv-not-a-real-key"
RESP_KEY_SECRET = "mock-secret-resp-not-a-real-key"

# max_attempts=2 ⇒ 单成员上限 max(6, 1×2×2)=6（见 internal/relay/retry.go attemptCap）。
EXPECTED_SOLO_CAP = 6
COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    text = ("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:260]
    try:
        print(text)
    except UnicodeEncodeError:
        sys.stdout.buffer.write(text.encode("utf-8", "replace") + b"\n")


def call(method, path, payload=None, timeout=60):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for header, value in resp.getheaders():
                if header.lower() == "set-cookie":
                    COOKIE["v"] = value.split(";")[0]
            body = resp.read().decode()
            return resp.status, (json.loads(body) if body else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {"_raw": raw[:200]}
    except Exception as e:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(e).__name__, e)}


def db():
    return sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)


def scalar(sql, args=()):
    c = db()
    row = c.execute(sql, args).fetchone()
    c.close()
    return row[0] if row else None


def api_key():
    return scalar("select api_key from api_keys where enabled=1 order by id limit 1")


def mark():
    return os.path.getsize(MOCK_LOG) if os.path.exists(MOCK_LOG) else 0


def attempts_since(offset):
    """某时刻之后 mock 收到的 (模型, 路径) 列表 —— 数真实的上游尝试次数。"""
    if not os.path.exists(MOCK_LOG):
        return []
    out = []
    with open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        for line in fh.read().splitlines():
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if row.get("path", "").endswith("/__control"):
                continue
            out.append(row.get("model"))
    return out


def rows_since(offset):
    """某时刻之后 mock 收到的完整请求记录（供核对凭据与请求头, 已由 mock 脱敏）。"""
    if not os.path.exists(MOCK_LOG):
        return []
    out = []
    with open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        for line in fh.read().splitlines():
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if row.get("path", "").endswith("/__control"):
                continue
            out.append(row)
    return out


def last_mock_body():
    if not os.path.exists(MOCK_LOG):
        return {}
    last = {}
    with open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        for line in fh.read().splitlines():
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if row.get("path", "").endswith("/__control"):
                continue
            last = row
    return last


def relay(group, timeout=45, messages=None):
    """非流式调用, 返回 (状态码, 耗时, 响应体)。"""
    key = api_key()
    payload = {"model": group, "max_tokens": 8,
               "messages": messages or [{"role": "user", "content": "ping"}]}
    req = urllib.request.Request(RELAY + "/v1/chat/completions", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + key)
    started = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = json.loads(resp.read().decode())
            return resp.status, round(time.time() - started, 1), body
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, round(time.time() - started, 1), json.loads(raw)
        except ValueError:
            return e.code, round(time.time() - started, 1), {"_raw": raw[:200]}
    except Exception as e:  # noqa: BLE001
        return 0, round(time.time() - started, 1), {"_raw": "%s: %s" % (type(e).__name__, e)}


def model_failed(model):
    """读渠道模型的内存统计（落库是异步的, 必须走 /channel/stats）。"""
    status, resp = call("GET", "/api/v1/channel/stats")
    if status != 200:
        return None
    for channel in (resp.get("data") or []):
        if channel.get("channel_name") != CHANNEL:
            continue
        for entry in (channel.get("models") or []):
            if entry.get("model_name") == model:
                return entry.get("request_failed")
    return None


def ensure_channel():
    existing = scalar("select id from channels where name=?", (CHANNEL,))
    if existing:
        call("DELETE", "/api/v1/channel/delete/%d" % existing)
    models = [GOOD, BAD, REJECT400, REJECT401, CONV, RESP]
    payload = {"name": CHANNEL, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
               "keys": [{"name": KEY_MAIN, "key": CHANNEL_KEY_SECRET, "enabled": True},
                        {"name": KEY_CONV, "key": CONV_KEY_SECRET, "enabled": True},
                        {"name": KEY_RESP, "key": RESP_KEY_SECRET, "enabled": True}],
               "models": models}
    call("POST", "/api/v1/channel/create", payload)
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if not cid:
        return None
    # protocols 位: 2=chat / 4=responses / 8=messages（见 lossless 套件 PINS）。前四个成员说全协议,
    # CONV 只说 messages、RESP 只说 responses —— chat 客户端命中它们时必然走转换路径,
    # 且两条转换路径的上游请求体形状不同（messages 数组 vs input 数组）。
    call("POST", "/api/v1/channel/update", dict(payload, id=cid,
                                                grants=[{"model_name": m, "key_name": KEY_MAIN, "protocols": 14}
                                                        for m in (GOOD, BAD, REJECT400, REJECT401)]
                                                       + [{"model_name": CONV, "key_name": KEY_CONV, "protocols": 8},
                                                          {"model_name": RESP, "key_name": KEY_RESP, "protocols": 4}]))
    return cid


def grants():
    c = db()
    rows = dict(c.execute("select m.name, g.id from channel_grants g "
                          "join channel_models m on m.id=g.channel_model_id "
                          "join channels ch on ch.id=m.channel_id where ch.name=?", (CHANNEL,)))
    c.close()
    return rows


def relay_config():
    return {"member_max_attempts": 2, "member_retry_interval_seconds": 1,
            "member_non_stream_response_timeout_seconds": 20,
            "member_stream_first_event_timeout_seconds": 10,
            "member_cooldown_seconds": 1, "member_affinity_seconds": 0}


def ensure_group(name, grant_ids, mode="failover"):
    existing = scalar("select id from groups where name=?", (name,))
    if existing:
        call("DELETE", "/api/v1/group/delete/%d" % existing)
    status, _ = call("POST", "/api/v1/group/create", {
        "name": name, "mode": mode,
        "items": [{"channel_grant_id": gid} for gid in grant_ids],
        "relay_config": relay_config(),
    })
    gid = scalar("select id from groups where name=?", (name,))
    return gid


def group_item_id(group_id):
    return scalar("select id from group_items where group_id=? order by priority, id limit 1", (group_id,))


def remove_fixtures():
    for name in (GROUP_CAP, GROUP_400_SOLO, GROUP_400_PAIR, GROUP_401_PAIR, GROUP_MANUAL, GROUP_DEV,
                 GROUP_CONV, GROUP_RESP):
        gid = scalar("select id from groups where name=?", (name,))
        if gid:
            call("DELETE", "/api/v1/group/delete/%d" % gid)
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if cid:
        call("DELETE", "/api/v1/channel/delete/%d" % cid)


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    cid = ensure_channel()
    grant = grants()
    if not cid or len(grant) < 6:
        record("R0 装置就位", False, "渠道/授权创建失败 channel=%s grants=%s" % (cid, grant))
        return 1
    ensure_group(GROUP_CAP, [grant[BAD]])
    ensure_group(GROUP_400_SOLO, [grant[REJECT400]])
    ensure_group(GROUP_400_PAIR, [grant[REJECT400], grant[GOOD]])
    ensure_group(GROUP_401_PAIR, [grant[REJECT401], grant[GOOD]])
    manual_id = ensure_group(GROUP_MANUAL, [grant[BAD]], mode="manual")
    ensure_group(GROUP_DEV, [grant[GOOD]])
    ensure_group(GROUP_CONV, [grant[CONV]])
    ensure_group(GROUP_RESP, [grant[RESP]])
    item = group_item_id(manual_id) if manual_id else None
    if manual_id and item:
        call("POST", "/api/v1/group/update/%d" % manual_id, {"active_item_id": item})

    # R1 可恢复失败（500）: 到尝试上限后给客户端明确失败, 不再无限重试。
    offset = mark()
    status, secs, body = relay(GROUP_CAP)
    hits = [m for m in attempts_since(offset) if m == BAD]
    record("R1 可恢复失败在尝试上限处终结",
           status == 502 and len(hits) == EXPECTED_SOLO_CAP and secs < 40,
           "HTTP %s 耗时 %ss 上游尝试 %d 次（上限 %d）body=%s"
           % (status, secs, len(hits), EXPECTED_SOLO_CAP, json.dumps(body, ensure_ascii=False)[:120]))

    # R2 请求本身非法且只有这一个成员: 只打一次, 立刻以 502 结束（不计成员失败）。
    failed_before = model_failed(REJECT400)
    offset = mark()
    status, secs, body = relay(GROUP_400_SOLO)
    hits = attempts_since(offset)
    failed_after = model_failed(REJECT400)
    record("R2 请求本身非法: 只尝试 1 次即结束, 不计成员失败",
           status == 502 and len(hits) == 1 and failed_before == failed_after and secs < 20,
           "HTTP %s 耗时 %ss 尝试=%s 成员失败计数 %s→%s body=%s"
           % (status, secs, hits, failed_before, failed_after, json.dumps(body, ensure_ascii=False)[:120]))

    # R3 请求本身非法但还有别的成员: 换成员后成功, 被拒绝的成员不计失败。
    failed_before = model_failed(REJECT400)
    offset = mark()
    status, secs, body = relay(GROUP_400_PAIR)
    hits = attempts_since(offset)
    failed_after = model_failed(REJECT400)
    record("R3 请求本身非法: 换成员后成功且不计失败",
           status == 200 and hits == [REJECT400, GOOD] and failed_before == failed_after,
           "HTTP %s 耗时 %ss 尝试=%s 成员失败计数 %s→%s" % (status, secs, hits, failed_before, failed_after))

    # R10 日志里能看到这次请求打了几轮上游（上游 #395「重试详情」的最小可用切片）:
    # 排障时"这次为什么慢"先看这个数 —— >1 就是换过成员, 与耗时/首字时间一起看才能分清是上游慢还是换人换出来的。
    # 断言用同一套夹具已有的两条日志: 耗尽上限的 R1（6 轮）与换人成功的 R3（2 轮）。
    hist = call("GET", "/api/v1/log/history?limit=40&offset=0")
    items = ((hist[1] or {}).get("data") or {}).get("items") or []
    cap_row = next((r for r in items if r.get("model") == GROUP_CAP), None)
    pair_row = next((r for r in items if r.get("model") == GROUP_400_PAIR), None)
    cap_attempts = (cap_row or {}).get("attempts")
    pair_attempts = (pair_row or {}).get("attempts")
    record("R10 日志记录上游轮次（耗尽上限=6 / 换人成功=2）",
           cap_attempts == EXPECTED_SOLO_CAP and pair_attempts == 2,
           "上限用例 attempts=%s（期望 %d）; 换人用例 attempts=%s（期望 2）"
           % (cap_attempts, EXPECTED_SOLO_CAP, pair_attempts))

    # R4 成员自身问题（401）: 记 1 次失败并立即换人, 不耗尽该成员的全部尝试次数。
    failed_before = model_failed(REJECT401)
    offset = mark()
    status, secs, body = relay(GROUP_401_PAIR)
    hits = attempts_since(offset)
    failed_after = model_failed(REJECT401)
    record("R4 成员自身问题: 立即换人且计 1 次失败",
           status == 200 and hits == [REJECT401, GOOD]
           and failed_before is not None and failed_after == failed_before + 1,
           "HTTP %s 耗时 %ss 尝试=%s 成员失败计数 %s→%s" % (status, secs, hits, failed_before, failed_after))

    # R5 手动模式: 钉住的成员持续失败时同样有上限（上游 #388）。
    offset = mark()
    status, secs, body = relay(GROUP_MANUAL)
    hits = [m for m in attempts_since(offset) if m == BAD]
    record("R5 手动模式不再无限重试",
           status == 502 and len(hits) == EXPECTED_SOLO_CAP and secs < 40,
           "HTTP %s 耗时 %ss 上游尝试 %d 次（上限 %d）" % (status, secs, len(hits), EXPECTED_SOLO_CAP))

    # R6 developer 角色归一化: 上游收到的必须是等价的 system。
    offset = mark()
    status, secs, body = relay(GROUP_DEV, messages=[
        {"role": "developer", "content": "be terse"},
        {"role": "user", "content": "ping"},
    ])
    mock_body = last_mock_body().get("body") or {}
    roles = [m.get("role") for m in (mock_body.get("messages") or [])]
    record("R6 developer 角色改写为 system",
           status == 200 and roles == ["system", "user"],
           "HTTP %s 耗时 %ss 上游收到的角色=%s" % (status, secs, roles))

    # R7 凭据隔离（上游 #372 同类的头部处理）: 客户端带来的凭据绝不能出现在上游请求里,
    # 上游收到的必须是渠道自己的密钥。mock 只落盘凭据的哈希前缀（见 mock_upstream._redact）,
    # 故这里按同样的算法比对哈希, 断言不涉及凭据原文。
    offset = mark()
    status, secs, body = relay(GROUP_400_PAIR)
    rows = [r for r in rows_since(offset) if r.get("model") == GOOD]
    channel_key = CHANNEL_KEY_SECRET  # ensure_channel 里配置的渠道密钥
    client_key = api_key() or ""
    channel_bearer = "bearer:" + hashlib.sha256(("Bearer " + channel_key).encode()).hexdigest()[:12]
    channel_header = "key:" + hashlib.sha256(channel_key.encode()).hexdigest()[:12]
    client_bearer = "bearer:" + hashlib.sha256(("Bearer " + client_key).encode()).hexdigest()[:12]
    client_header = "key:" + hashlib.sha256(client_key.encode()).hexdigest()[:12]
    seen = [(r.get("authorization"), r.get("x_api_key")) for r in rows]
    # 窗口内可能有后台探活等并发请求, 故不强求"恰好一条", 但**每一条**都必须是渠道密钥、且一条都不许带客户端凭据。
    carried = [pair for pair in seen if channel_bearer in pair or channel_header in pair]
    leaked = [pair for pair in seen if client_bearer in pair or client_header in pair]
    # 除两个凭据列外, 任何头部的指纹里也不许出现客户端凭据；再兜一层：整行里不许出现凭据原文。
    leaked_any = [r for r in rows
                  if {client_bearer, client_header} & set((r.get("headers") or {}).values())
                  or (client_key and client_key in json.dumps(r, ensure_ascii=False))]
    record("R7 客户端凭据不透传给上游",
           status == 200 and len(seen) >= 1 and len(carried) == len(seen) and not leaked and not leaked_any,
           "HTTP %s 上游请求 %d 条, 携带渠道密钥 %d 条, 携带客户端凭据 %d 条（任意头部 %d 条）" % (
               status, len(seen), len(carried), len(leaked), len(leaked_any)))

    # R8 转换路径同样要改写 developer：R6 守的是同协议直通（protocol.go 的 buildPassthroughRequest），
    # 这里守跨协议转换（upstream.go 的转换中间件）——两条路径各有守位, 少一个就会漏（变异检查发现的覆盖缺口）。
    offset = mark()
    status, secs, body = relay(GROUP_CONV, messages=[
        {"role": "developer", "content": "be terse"},
        {"role": "user", "content": "ping"},
    ])
    rows = [r for r in rows_since(offset) if r.get("model") == CONV]
    row = rows[-1] if rows else {}
    converted = "/v1/messages" in (row.get("path") or "")
    upstream = row.get("body") or {}
    raw = json.dumps(upstream, ensure_ascii=False)
    roles = [x.get("role") for x in (upstream.get("messages") or [])]
    no_dev = "developer" not in raw
    kept = "be terse" in raw
    record("R8 转换路径 developer 角色同样改写为 system",
           status == 200 and converted and no_dev and kept,
           "HTTP %s 耗时 %ss 上游路径=%s 上游角色=%s developer残留=%s 文本保留=%s" % (
               status, secs, row.get("path"), roles, not no_dev, kept))

    # R9 同一条不变量的 Responses 形状：Chat 客户端 → 只说 responses 的成员, 上游正文用的是顶层 input 数组,
    # 角色不在 messages 里。分协议位实测发现这里曾经漏网（T-devrole-001）—— R8 只盖 messages 形状,
    # 少这条就会让「developer 一律改写成 system」在 Responses 路径上失效。
    offset = mark()
    status, secs, body = relay(GROUP_RESP, messages=[
        {"role": "developer", "content": "be terse"},
        {"role": "user", "content": "ping"},
    ])
    rows = [r for r in rows_since(offset) if r.get("model") == RESP]
    row = rows[-1] if rows else {}
    upstream = row.get("body") or {}
    raw = json.dumps(upstream, ensure_ascii=False)
    is_responses = "/v1/responses" in (row.get("path") or "")
    items = upstream.get("input")
    item_roles = [x.get("role") for x in items] if isinstance(items, list) else []
    no_dev = "developer" not in raw
    kept = "be terse" in raw
    record("R9 Responses 形状的转换路径也不留 developer",
           status == 200 and is_responses and no_dev and kept,
           "HTTP %s 耗时 %ss 上游路径=%s input条目角色=%s developer残留=%s 文本保留=%s" % (
               status, secs, row.get("path"), item_roles, not no_dev, kept))

    return 0


if __name__ == "__main__":
    try:
        main()
    finally:
        try:
            remove_fixtures()
        except Exception as e:  # noqa: BLE001
            print("清理夹具失败: %s" % e)
    failed = [name for name, ok, _ in RESULTS if not ok]
    print("RETRY_TEST total=%d pass=%d fail=%d" % (len(RESULTS), len(RESULTS) - len(failed), len(failed)))
    for name in failed:
        print("  FAILED: %s" % name)
    sys.exit(1 if failed else 0)
