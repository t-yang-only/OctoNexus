"""组合场景活体套件：加权选路 × 计费权重 × 首字竞速 × 流式 × 冷却 × 日志统计。

为什么要有它：各特性都有独立套件，但**没人验证它们叠在一起还成立**（这轮之前，加权与竞速各自过、组合没人试）。
本套件把最容易互相打架的几件事放在同一个分组上跑，全部走本地 mock。

用例:
  C1 加权选路 + 首字竞速：权重把慢成员排第一, 竞速仍能在 800ms 后救场 → 快成员胜出（且两条成员都被尝试过）
  C2 组合下胜者不进冷却：落选成员不冷却、胜者正常记成功
  C3 计费权重决定胜者：关掉竞速, 把倍率权重拉满 → 倍率低的渠道胜出（哪怕它 priority 更靠后）
  C4 组合下日志统计一致：本组请求数与日志行数一致, 成功行有 first_byte_ms
  C5 流式 + 竞速 + 加权：首个 SSE 块由竞速胜出的成员给出

Run: python run_combo_test.py    （实例 + mock 必须在跑）
"""

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
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")

GROUP = "DS-TEST-combo"
PLAIN_CHANNEL = "DS-TEST-combo-slow"    # 本套件专用: 放 mock-slow（慢成员）
# 注意: 不能复用共享夹具渠道 DS-TEST-mock —— 本套件要写渠道字段, 而 ensure_channel 是"删了重建",
# 那会把其它套件依赖的模型/授权一起删掉（本轮踩过: combo 跑完后 hotapply 直接超时失败）。
PRICE_CHANNEL = "DS-TEST-combo-price"   # 放 mock-good（快成员, 渠道计费字段也写在这条上）
SLOW, FAST = "mock-slow", "mock-good"
WEIGHT_KEYS = ["route_weight_cost", "route_weight_quality", "route_weight_latency", "route_weight_busy",
               "route_weight_load", "route_weight_multiplier", "route_weight_per_call",
               "route_weight_balance", "route_weight_monthly"]
COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:230])


def call(method, path, payload=None, timeout=180):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    COOKIE["v"] = v.split(";")[0]
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


def relay(model, timeout=90, stream=False):
    payload = {"model": model, "max_tokens": 8, "messages": [{"role": "user", "content": "ping"}]}
    if stream:
        payload["stream"] = True
    req = urllib.request.Request(RELAY + "/v1/chat/completions", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + api_key())
    started = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            if stream:
                first = None
                while True:
                    chunk = resp.read(256)
                    if not chunk:
                        break
                    if first is None and b"data:" in chunk:
                        first = round(time.time() - started, 1)
                return resp.status, first if first is not None else -1, resp.status
            resp.read()
            return resp.status, round(time.time() - started, 1), None
    except urllib.error.HTTPError as e:
        return e.code, round(time.time() - started, 1), {"_raw": e.read().decode()[:120]}
    except Exception as e:  # noqa: BLE001
        return 0, round(time.time() - started, 1), type(e).__name__


def mark():
    return os.path.getsize(MOCK_LOG) if os.path.exists(MOCK_LOG) else 0


def models_since(offset):
    if not os.path.exists(MOCK_LOG):
        return []
    seen = []
    with open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        for line in fh.read().splitlines():
            try:
                seen.append(json.loads(line).get("model"))
            except ValueError:
                continue
    return seen


def set_weights(**kwargs):
    for key in WEIGHT_KEYS:
        call("POST", "/api/v1/setting/set", {"key": key, "value": str(kwargs.get(key.replace("route_weight_", ""), 0))})


def ensure_channel(name, models, billing, grants_models):
    existing = scalar("select id from channels where name=?", (name,))
    if existing:
        call("POST", "/api/v1/channel/delete/%d" % existing)
    payload = {"name": name, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
               "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
               "models": models, **billing}
    call("POST", "/api/v1/channel/create", payload)
    cid = scalar("select id from channels where name=?", (name,))
    if not cid:
        return None
    # 建档时只给 models 不会 materialize 授权, 再 update 一次并显式给 grants。
    call("POST", "/api/v1/channel/update", {
        **payload, "id": cid,
        "grants": [{"model_name": m, "key_name": "mockkey", "protocols": 14} for m in grants_models],
    })
    return cid


def grant_of(channel_name, model_name):
    return scalar("select g.id from channel_grants g join channel_models m on m.id=g.channel_model_id "
                  "join channels ch on ch.id=m.channel_id where ch.name=? and m.name=?",
                  (channel_name, model_name))


def ensure_group(items, hedge):
    existing = scalar("select id from groups where name=?", (GROUP,))
    if existing:
        call("DELETE", "/api/v1/group/delete/%d" % existing)
    status, resp = call("POST", "/api/v1/group/create", {
        "name": GROUP, "mode": "weighted",
        "items": [{"channel_grant_id": gid} for gid in items],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_non_stream_response_timeout_seconds": 60,
                         "member_stream_first_event_timeout_seconds": 30,
                         "member_cooldown_seconds": 60, "member_affinity_seconds": 0,
                         "hedge_enabled": hedge, "hedge_width": 2, "hedge_after_ms": 800,
                         "hedge_peak_in_flight": 0},
    })
    return status, resp


def last_log():
    c = db()
    row = c.execute("select status, target_channel, target_model, first_byte_ms from relay_logs "
                    "where model=? order by id desc limit 1", (GROUP,)).fetchone()
    c.close()
    return row


def log_rows():
    return scalar("select count(*) from relay_logs where model=?", (GROUP,)) or 0


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    # 装置: 两条渠道各放一个模型 —— 慢成员在"便宜"渠道, 快成员在"贵"渠道（计费字段写在贵渠道上）。
    plain = ensure_channel(PLAIN_CHANNEL, [SLOW], {"billing_mode": "metered", "multiplier": 5}, [SLOW])
    price = ensure_channel(PRICE_CHANNEL, [FAST], {"billing_mode": "per_call", "multiplier": 3, "per_call_price": 0.02}, [FAST])
    slow_grant, fast_grant = grant_of(PLAIN_CHANNEL, SLOW), grant_of(PRICE_CHANNEL, FAST)
    if not (plain and price and slow_grant and fast_grant):
        record("装置", False, "渠道/授权创建失败 plain=%s price=%s slow=%s fast=%s" % (plain, price, slow_grant, fast_grant))
        return 1

    # C1 加权 + 竞速: 权重只给延迟 → 排名把慢成员放第一, 竞速 800ms 后并发快成员 → 快者胜出。
    set_weights(latency=100)
    ensure_group([slow_grant, fast_grant], hedge=True)
    offset = mark()
    status, secs, _ = relay(GROUP, timeout=60)
    attempts = models_since(offset)
    row = last_log()
    record("C1 加权选路 + 首字竞速：慢者排第一仍被竞速救回",
           status == 200 and secs < 10 and SLOW in attempts and FAST in attempts and row and row[1] == PRICE_CHANNEL,
           "HTTP %s 耗时 %ss 上游尝试=%s 命中渠道=%s" % (status, secs, attempts, row and row[1]))

    # C2 组合下胜者/落选者的冷却状态: 落选（被我们自己取消）不进冷却。
    group = call("GET", "/api/v1/group/list")[1]
    target = next((g for g in ((group or {}).get("data") or []) if g.get("name") == GROUP), None)
    cooldowns = ((target or {}).get("runtime") or {}).get("cooldowns") or {}
    slow_item = scalar("select gi.id from group_items gi join groups g on g.id=gi.group_id "
                       "where g.name=? and gi.channel_grant_id=?", (GROUP, slow_grant))
    record("C2 组合下落选成员不进冷却", slow_item is not None and str(slow_item) not in {str(k) for k in cooldowns},
           "慢成员 item=%s cooldowns=%s" % (slow_item, cooldowns))

    # C3 计费权重决定胜者（关掉竞速）: 两条渠道都写了倍率（慢渠道 5、快渠道 3）→ 倍率低的胜出。
    set_weights(multiplier=100)
    ensure_group([slow_grant, fast_grant], hedge=False)
    offset = mark()
    status, secs, _ = relay(GROUP, timeout=60)
    attempts = models_since(offset)
    row = last_log()
    record("C3 倍率权重拉满：倍率更低的渠道胜出（且不触发起竞速）",
           status == 200 and row and row[1] == PRICE_CHANNEL and len(attempts) == 1,
           "HTTP %s 耗时 %ss 上游尝试=%s 命中渠道=%s" % (status, secs, attempts, row and row[1]))

    # C4 组合下的日志统计一致: 每个请求恰好一行, 成功行有 first_byte_ms。
    before = log_rows()
    status, secs, _ = relay(GROUP, timeout=60)
    after = log_rows()
    row = last_log()
    record("C4 组合下日志统计一致（一请求一行 + 成功行有首字节）",
           status == 200 and after == before + 1 and row is not None and row[0] == "success" and (row[3] or -1) >= 0,
           "行数 %d→%d 末行=%s" % (before, after, row))

    # C5 流式 + 竞速 + 加权: 打开竞速, 首个 SSE 块应由竞速胜出的成员给出。
    set_weights(latency=100)
    ensure_group([slow_grant, fast_grant], hedge=True)
    offset = mark()
    status, first, _ = relay(GROUP, timeout=60, stream=True)
    attempts = models_since(offset)
    record("C5 流式 + 竞速 + 加权：首个 SSE 块由胜出成员给出",
           status == 200 and first is not None and 0 <= first < 10 and SLOW in attempts and FAST in attempts,
           "HTTP %s 首块 %ss 上游尝试=%s" % (status, first, attempts))

    # 复原与清理: 权重回默认, 分组与两条本套件渠道全部删掉（共享夹具一概不动）。
    set_weights(cost=30, quality=30, latency=15, busy=15, load=10, multiplier=15, per_call=10, balance=15, monthly=10)
    existing = scalar("select id from groups where name=?", (GROUP,))
    if existing:
        call("DELETE", "/api/v1/group/delete/%d" % existing)
    for name in (PLAIN_CHANNEL, PRICE_CHANNEL):
        cid = scalar("select id from channels where name=?", (name,))
        if cid:
            call("POST", "/api/v1/channel/delete/%d" % cid)

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("COMBO total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
