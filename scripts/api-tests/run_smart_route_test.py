"""智能路由（mode = smart，对齐阶跃 Step Router 的用法）活体套件。

口径：客户端只填一个模型名（分组名），选路层按请求特征判复杂度 ——
复杂走靠前的成员（决策引擎档）、简单走靠后的成员（执行引擎档）。
本套件用两个渠道成员做差分对照，靠 mock 落盘的 model 字段分辨是谁服务的。

用例：
  S1 简单请求 → 执行引擎档
  S2 复杂请求 → 决策引擎档（与 S1 同一分组、只改请求形状）
  S3 灵敏度阶梯：单项特征不足以翻转（33 分 < 50），任意两项组合能翻转（>50）—— 三组对照
  S4 阈值两侧：同一份请求，阈值 70 判简单、阈值 60 判复杂
  S5 决策引擎渠道停用 → 复杂请求回退到执行引擎档且仍成功（省钱不能变成不可用）
  S6 流式请求同样按复杂度分档
  S7 其它模式不受影响（同分组切回 failover 时按成员顺序选，不看请求形状）
"""
import http.client
import io
import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.request

ROOT = r"D:\奇怪的软件\octopus"
DB = os.environ.get("OCTOPUS_DB", os.path.join(ROOT, "data", "data.db"))
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
ADMIN = "http://" + os.environ.get("OCTOPUS_ADMIN_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))

CHANNEL_STRONG = "DS-TEST-smart-strong"   # 决策引擎渠道（排在前面的成员）
CHANNEL_FAST = "DS-TEST-smart-fast"       # 执行引擎渠道（排在后面的成员）
MODEL_STRONG = "mock-good"
MODEL_FAST = "mock-plain"
GROUP = "DS-TEST-smart"
GROUP_ORDER = "DS-TEST-smart-order"      # 用于 S7：同两个成员，模式切回 failover
GROUP_HEDGE = "DS-TEST-smart-hedge"      # 用于 S8：四个成员（决策档两名 + 执行档两名）+ 开启竞速
CHANNEL_BAD = "DS-TEST-smart-bad"

COOKIE = {}
RESULTS = []
CLEANUP_CHANNELS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


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
    except urllib.error.HTTPError as exc:
        return exc.code, {"_raw": exc.read().decode()[:200]}
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}


def db():
    return sqlite3.connect("file:" + os.path.abspath(DB).replace("\\", "/") + "?mode=ro", uri=True)


def scalar(sql, args=()):
    conn = db()
    try:
        row = conn.execute(sql, args).fetchone()
        return row[0] if row else None
    finally:
        conn.close()


def api_key():
    conn = db()
    try:
        cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
        col = "api_key" if "api_key" in cols else "key"
        row = conn.execute("select %s from api_keys where enabled = 1 order by id limit 1" % col).fetchone()
        return row[0] if row else ""
    finally:
        conn.close()


def mark():
    return os.path.getsize(MOCK_LOG) if os.path.exists(MOCK_LOG) else 0


def rows_since(offset):
    out = []
    if not os.path.exists(MOCK_LOG):
        return out
    with io.open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        for line in fh.read().splitlines():
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if row.get("path", "").endswith("/__control"):
                continue
            if row.get("method") != "POST":
                continue
            out.append(row)
    return out


def relay(group, body, timeout=45):
    key = api_key()
    body = dict(body)
    body["model"] = group
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + key}
    conn.request("POST", "/v1/chat/completions", body=json.dumps(body), headers=headers)
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw


def served_by(group, body):
    """打一次请求, 返回 (HTTP 状态, 上游实际收到的模型名)。"""
    offset = mark()
    status, _raw = relay(group, body)
    rows = rows_since(offset)
    return status, (rows[-1].get("model") if rows else None)


def simple_body():
    return {"max_tokens": 16, "messages": [{"role": "user", "content": "ping"}]}


def complex_body(rounds=8, tools=8, pad=0):
    messages = [{"role": "user" if i % 2 == 0 else "assistant", "content": "step %d" % i} for i in range(rounds)]
    if pad:
        messages[0]["content"] = "context " + ("x" * pad)
    return {"max_tokens": 16, "messages": messages,
            "tools": [{"type": "function", "function": {"name": "t%d" % i, "parameters": {"type": "object"}}}
                      for i in range(tools)]}


def cleanup():
    for name in (GROUP, GROUP_ORDER, GROUP_HEDGE):
        gid = scalar("select id from groups where name=?", (name,))
        if gid:
            call("DELETE", "/api/v1/group/delete/%d" % gid)
    for name in [CHANNEL_STRONG, CHANNEL_FAST] + CLEANUP_CHANNELS:
        cid = scalar("select id from channels where name=?", (name,))
        if cid:
            call("DELETE", "/api/v1/channel/delete/%d" % cid)


def ensure_channel(name, model, key_specs, enabled=True):
    """key_specs: [(凭据名, 密钥), ...] —— 一个渠道可以有多条凭据, 每条凭据一条授权。"""
    cid = scalar("select id from channels where name=?", (name,))
    if cid:
        call("DELETE", "/api/v1/channel/delete/%d" % cid)
    payload = {"name": name, "dialect": "generic", "enabled": enabled, "base_url": MOCK_BASE,
               "keys": [{"name": key_name, "key": secret, "enabled": True} for key_name, secret in key_specs],
               "models": [model]}
    call("POST", "/api/v1/channel/create", payload)
    cid = scalar("select id from channels where name=?", (name,))
    if not cid:
        return None, []
    call("POST", "/api/v1/channel/update", dict(payload, id=cid,
                                                grants=[{"model_name": model, "key_name": key_name, "protocols": 14}
                                                        for key_name, _secret in key_specs]))
    grants = []
    for key_name, _secret in key_specs:
        grant = scalar("select g.id from channel_grants g join channel_models m on m.id = g.channel_model_id "
                       "join channel_keys k on k.id = g.channel_key_id join channels c on c.id = m.channel_id "
                       "where c.name = ? and m.name = ? and k.name = ?", (name, model, key_name))
        grants.append(grant)
    return cid, grants


def ensure_group(name, grant_ids, mode="smart", threshold=50):
    gid = scalar("select id from groups where name=?", (name,))
    if gid:
        call("DELETE", "/api/v1/group/delete/%d" % gid)
    relay_config = {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                    "member_cooldown_seconds": 1, "member_affinity_seconds": 0,
                    "smart_route_threshold": threshold}
    status, _ = call("POST", "/api/v1/group/create", {
        "name": name, "mode": mode,
        "items": [{"channel_grant_id": g} for g in grant_ids],
        "relay_config": relay_config})
    if status != 200:
        return None
    return scalar("select id from groups where name=?", (name,))


def set_group(name, mode=None, threshold=None, items=None):
    gid = scalar("select id from groups where name=?", (name,))
    if not gid:
        return False
    body = {}
    if mode is not None:
        body["mode"] = mode
    if threshold is not None:
        body["relay_config"] = {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                                "member_cooldown_seconds": 1, "member_affinity_seconds": 0,
                                "smart_route_threshold": threshold}
    if items is not None:
        body["items"] = [{"channel_grant_id": g} for g in items]
    status, _ = call("POST", "/api/v1/group/update/%d" % gid, body)
    return status == 200


def ensure_group_hedge(name, grant_ids):
    """S8 专用分组：智能路由 + 开启首字竞速（宽度 3、1 毫秒后追加竞速路）。"""
    gid = scalar("select id from groups where name=?", (name,))
    if gid:
        call("DELETE", "/api/v1/group/delete/%d" % gid)
    status, _ = call("POST", "/api/v1/group/create", {
        "name": name, "mode": "smart",
        "items": [{"channel_grant_id": g} for g in grant_ids],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_cooldown_seconds": 1, "member_affinity_seconds": 0,
                         "smart_route_threshold": 50,
                         "hedge_enabled": True, "hedge_width": 3, "hedge_after_ms": 1,
                         "hedge_peak_in_flight": 0}})
    return status == 200


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    cleanup()
    _cid_strong, strong_grants = ensure_channel(CHANNEL_STRONG, MODEL_STRONG,
                                                [("smartkey", "smart-secret-strong"), ("smartkey2", "smart-secret-strong2")])
    cid_fast, fast_grants = ensure_channel(CHANNEL_FAST, MODEL_FAST,
                                           [("fastkey", "smart-secret-fast"), ("fastkey2", "smart-secret-fast2")])
    if len(strong_grants) != 2 or len(fast_grants) != 2 or not all(strong_grants + fast_grants):
        record("S0 装置就位", False, "渠道授权没建全: strong=%s fast=%s" % (strong_grants, fast_grants))
        return 1
    grant_strong, grant_fast = strong_grants[0], fast_grants[0]
    # 成员顺序 = 档位：强渠道在前（决策引擎档），快渠道在后（执行引擎档）。
    if not ensure_group(GROUP, [grant_strong, grant_fast]) or not ensure_group(GROUP_ORDER, [grant_strong, grant_fast]):
        record("S0 装置就位", False, "分组创建失败（mode=smart 是否被后端接受？）")
        return 1
    # S8 用的四成员分组：决策档两名（强渠道两条凭据）、执行档两名（快渠道两条凭据）。
    # 奇数/偶数档位切分口径见 SmartTierItems: 4 名成员 → 前 2 后 2。
    if not ensure_group_hedge(GROUP_HEDGE, strong_grants + fast_grants):
        record("S0 装置就位", False, "竞速用例分组创建失败")
        return 1
    record("S0 装置就位", True, "两个渠道（各两条凭据）+ 智能路由分组 + 竞速分档分组就位")
    time.sleep(1)

    # S1/S2：同一分组、只改请求形状。
    status, model = served_by(GROUP, simple_body())
    record("S1 简单请求走执行引擎档", status == 200 and model == MODEL_FAST,
           "HTTP %s 上游模型=%s（期望 %s）" % (status, model, MODEL_FAST))

    status, model = served_by(GROUP, complex_body())
    record("S2 复杂请求走决策引擎档", status == 200 and model == MODEL_STRONG,
           "HTTP %s 上游模型=%s（期望 %s）" % (status, model, MODEL_STRONG))

    # S3 灵敏度阶梯：三项特征等权，单项最多 33 分（低于默认阈值 50），任意两项 ≥ 66 分。
    ladder = [
        ("单项：8 轮", complex_body(rounds=8, tools=0), MODEL_FAST),
        ("两项：8 轮 + 8 工具", complex_body(rounds=8, tools=8), MODEL_STRONG),
        ("两项：8 轮 + 长正文", complex_body(rounds=8, tools=0, pad=40000), MODEL_STRONG),
        ("两项：8 工具 + 长正文", complex_body(rounds=1, tools=8, pad=40000), MODEL_STRONG),
    ]
    ladder_detail = []
    ladder_ok = True
    for label, body, expect in ladder:
        status, model = served_by(GROUP, body)
        ok = status == 200 and model == expect
        ladder_ok = ladder_ok and ok
        ladder_detail.append("%s → %s(%s)" % (label, model, "对" if ok else "错，期望 " + expect))
    record("S3 复杂度灵敏度阶梯（单项不够、两项就够）", ladder_ok, "; ".join(ladder_detail))

    # S4 阈值两侧：同一份请求（8 轮 + 8 工具 ≈ 66 分），阈值 70 判简单、阈值 60 判复杂。
    set_group(GROUP, threshold=70)
    status_high, model_high = served_by(GROUP, complex_body())
    set_group(GROUP, threshold=60)
    status_low, model_low = served_by(GROUP, complex_body())
    set_group(GROUP, threshold=50)
    record("S4 阈值两侧可翻转", status_high == 200 and model_high == MODEL_FAST
           and status_low == 200 and model_low == MODEL_STRONG,
           "阈值 70 → %s（期望 %s）；阈值 60 → %s（期望 %s）" % (
               model_high, MODEL_FAST, model_low, MODEL_STRONG))

    # S5 决策引擎档当前不可用（这里让强渠道的上游必失败, 走生产同一条失败 → 冷却 → 换人路径）：
    # 复杂请求不该失败, 而应回退到执行引擎档 —— 「省钱」不能变成「不可用」。
    _cid_bad, bad_grants = ensure_channel(CHANNEL_BAD, "mock-bad", [("badkey", "smart-secret-bad")])
    grant_bad = bad_grants[0] if bad_grants else None
    if not grant_bad or not set_group(GROUP, items=[grant_bad, grant_fast]):
        record("S5 决策档不可用时回退", False, "第三渠道/成员顺序替换失败")
    else:
        status, model = served_by(GROUP, complex_body())
        fallback_ok = status == 200 and model == MODEL_FAST
        record("S5 决策档不可用时回退", fallback_ok,
               "HTTP %s 上游模型=%s（期望回退到 %s 而不是失败）" % (status, model, MODEL_FAST))
        set_group(GROUP, items=[grant_strong, grant_fast])
        CLEANUP_CHANNELS.append(CHANNEL_BAD)
        time.sleep(2)  # 等冷却（1 秒）到期，避免影响后续用例

    # S6 流式请求同样分档（判定只看请求特征，与是否流式无关）。
    offset = mark()
    status, _ = relay(GROUP, dict(complex_body(), stream=True))
    rows = rows_since(offset)
    stream_model = rows[-1].get("model") if rows else None
    record("S6 流式请求同样分档", status == 200 and stream_model == MODEL_STRONG,
           "HTTP %s 上游模型=%s（期望 %s）" % (status, stream_model, MODEL_STRONG))

    # S7 其它模式不受请求形状影响：切回 failover 后，简单与复杂请求都按成员顺序选强渠道。
    set_group(GROUP, mode="failover")
    _s_simple, model_simple = served_by(GROUP, simple_body())
    _s_complex, model_complex = served_by(GROUP, complex_body())
    record("S7 切回 failover 后不看请求形状", model_simple == MODEL_STRONG and model_complex == MODEL_STRONG,
           "failover 下 简单请求=%s、复杂请求=%s（期望都是 %s）" % (model_simple, model_complex, MODEL_STRONG))
    set_group(GROUP, mode="smart")

    # S8 智能路由 × 首字竞速：竞速只能在选中那一档内进行。开启竞速（宽度 3、1ms 触发）后，
    # 简单请求不该出现任何强渠道的上游尝试，复杂请求不该出现任何快渠道的尝试 ——
    # 否则「按复杂度控成本」会被竞速悄悄绕过（竞速本身就会多发请求）。
    hedge_detail = []
    hedge_ok = True
    for label, body, expect_model, forbid_model in (
            ("简单请求", simple_body(), MODEL_FAST, MODEL_STRONG),
            ("复杂请求", complex_body(), MODEL_STRONG, MODEL_FAST)):
        offset = mark()
        status, _raw = relay(GROUP_HEDGE, body)
        rows = rows_since(offset)
        seen = [row.get("model") for row in rows]
        ok = status == 200 and seen and forbid_model not in seen and all(m == expect_model for m in seen)
        hedge_ok = hedge_ok and ok
        hedge_detail.append("%s → HTTP %s 上游尝试=%s（应为 %s 且不含 %s）" % (
            label, status, seen, expect_model, forbid_model))
    record("S8 竞速不越档（简单不碰强渠道 / 复杂不碰快渠道）", hedge_ok, "; ".join(hedge_detail))

    passed = sum(1 for _n, ok, _d in RESULTS if ok)
    print("\nSMART_ROUTE_TEST total=%d pass=%d fail=%d" % (len(RESULTS), passed, len(RESULTS) - passed))
    return 0 if passed == len(RESULTS) else 1


if __name__ == "__main__":
    try:
        code = main()
    finally:
        cleanup()
    sys.exit(code)
