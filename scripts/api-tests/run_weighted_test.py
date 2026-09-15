"""加权综合选路（weighted 模式）活体套件 —— **只用本地 mock 上游**。

口径（设计稿 §2 与 internal/relay/weights.go 顶部注释）:
- 五维: 成本(价表) / 质量(近期成功率) / 延迟(最近一次尝试耗时) / 在途 / 近期消耗(60s);
- 权重来自设置项 route_weight_*, 取值 0..100, 缺省 30/30/15/15/10;
- 某维无数据按中性 0.5 参与（不惩罚新成员）; 并列按 priority 再按 ID;
- 权重全 0 → 全员同分, 退化为 priority 顺序（文档化行为）。

用例:
  T1 weighted 模式可建档并正常转发（200）
  T2 延迟权重拉满(其余置 0) → 先量出慢成员, 之后选路应挑快成员
  T3 权重全 0 → 退化为 priority 顺序（选回 priority 第一的慢成员）
  T4 越界权重被夹到 0..100 且接口不报错
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
GROUP = "DS-TEST-weighted"
SLOW, FAST = "mock-slow", "mock-good"
WEIGHT_KEYS = ["route_weight_cost", "route_weight_quality", "route_weight_latency",
               "route_weight_busy", "route_weight_load", "route_weight_multiplier",
               "route_weight_per_call", "route_weight_balance", "route_weight_monthly"]
BILLING_GROUP = "DS-TEST-weighted-billing"
PRICE_CHANNEL = "DS-TEST-mock-price"
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:220])


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


def group_id(name):
    c = db()
    row = c.execute("select id from groups where name = ?", (name,)).fetchone()
    c.close()
    return row[0] if row else None


def grants():
    c = db()
    rows = dict(c.execute("select m.name, g.id from channel_grants g join channel_models m on m.id=g.channel_model_id "
                          "join channels ch on ch.id=m.channel_id where ch.name='DS-TEST-mock' "
                          "and m.name in (?, ?)", (SLOW, FAST)))
    c.close()
    return rows


def relay_api_key():
    c = db()
    row = c.execute("select api_key from api_keys where enabled=1 order by id limit 1").fetchone()
    c.close()
    return row[0]


def relay(model, timeout=90):
    key = relay_api_key()
    payload = {"model": model, "max_tokens": 8, "messages": [{"role": "user", "content": "ping"}]}
    req = urllib.request.Request(RELAY + "/v1/chat/completions", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + key)
    started = time.time()
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = json.loads(resp.read().decode())
            target = ((body.get("choices") or [{}])[0].get("message") or {}).get("content")
            return resp.status, round(time.time() - started, 1), body.get("model"), target
    except urllib.error.HTTPError as e:
        return e.code, round(time.time() - started, 1), None, e.read().decode()[:120]
    except Exception as e:  # noqa: BLE001
        return 0, round(time.time() - started, 1), None, type(e).__name__


def set_weights(**kwargs):
    for key in WEIGHT_KEYS:
        name = key.replace("route_weight_", "")
        value = kwargs.get(name, 0)
        call("POST", "/api/v1/setting/set", {"key": key, "value": str(value)})


def last_target_model():
    """从最近一条 DS-TEST-weighted 日志行读实际命中的上游模型。"""
    c = db()
    row = c.execute("select target_model from relay_logs where model = ? order by id desc limit 1", (GROUP,)).fetchone()
    c.close()
    return row[0] if row else None


def channel_id(name):
    c = db()
    row = c.execute("select id from channels where name=?", (name,)).fetchone()
    c.close()
    return row[0] if row else None


def grant_of(channel_name, model_name):
    c = db()
    row = c.execute("select g.id from channel_grants g join channel_models m on m.id=g.channel_model_id "
                    "join channels ch on ch.id=m.channel_id where ch.name=? and m.name=?",
                    (channel_name, model_name)).fetchone()
    c.close()
    return row[0] if row else None


def last_target_channel(group_name):
    c = db()
    row = c.execute("select target_channel from relay_logs where model=? order by id desc limit 1", (group_name,)).fetchone()
    c.close()
    return row[0] if row else None


def ensure_price_channel():
    """"贵"的 mock 渠道: 同一个本地 mock 上游, 但倍率 3 / 按次 0.02 / 包月额度 1000 已用 100。"""
    existing = channel_id(PRICE_CHANNEL)
    if existing:
        call("POST", "/api/v1/channel/delete/%d" % existing)
    call("POST", "/api/v1/channel/create", {
        "name": PRICE_CHANNEL, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
        "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
        "models": [FAST],
        "billing_mode": "per_call", "multiplier": 3, "per_call_price": 0.02,
        "monthly_quota": 1000, "monthly_used": 100,
    })
    created = channel_id(PRICE_CHANNEL)
    if not created:
        return None
    # 建档时只给 models 不会materialize 授权, 需要再 update 一次并显式给 grants（与 setup_test_entities 同一做法）。
    call("POST", "/api/v1/channel/update", {
        "id": created, "name": PRICE_CHANNEL, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
        "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
        "models": [FAST],
        "grants": [{"model_name": FAST, "key_name": "mockkey", "protocols": 14}],
        "billing_mode": "per_call", "multiplier": 3, "per_call_price": 0.02,
        "monthly_quota": 1000, "monthly_used": 100,
    })
    _, detail = call("GET", "/api/v1/channel/detail/%d" % created)
    return (detail or {}).get("data") or {}


def cleanup_price_channel():
    existing = channel_id(PRICE_CHANNEL)
    if existing:
        call("POST", "/api/v1/channel/delete/%d" % existing)


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    g = grants()
    if len(g) < 2:
        record("fixtures", False, "DS-TEST-mock 的 mock-slow/mock-good 授权缺失")
        return 1

    # 建档: weighted 模式, priority 上把慢的放前面（这样"选路是否真的换人"才有信息量）。
    existing = group_id(GROUP)
    if existing:
        call("DELETE", "/api/v1/group/delete/%d" % existing)
    payload = {
        "name": GROUP, "mode": "weighted",
        "items": [{"channel_grant_id": g[SLOW]}, {"channel_grant_id": g[FAST]}],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_non_stream_response_timeout_seconds": 120,
                         "member_stream_first_event_timeout_seconds": 30,
                         "member_cooldown_seconds": 60, "member_affinity_seconds": 0},
    }
    status, resp = call("POST", "/api/v1/group/create", payload)
    record("T1 weighted 模式可建档", status == 200, "HTTP %d %s" % (status, str(resp.get("message"))[:80]))

    # T2 延迟权重拉满（其余 0）: 先跑一次让"慢"这件事被量到, 之后应选快成员。
    # 注意: 本轮成员的价表数据为空 => 成本维度对所有人都是"无数据"(中性), 故延迟是唯一有效维度。
    set_weights(latency=100)
    status1, secs1, _, body1 = relay(GROUP, timeout=90)
    first_target = last_target_model()
    time.sleep(0.3)
    status2, secs2, _, body2 = relay(GROUP, timeout=90)
    second_target = last_target_model()
    record("T2 延迟权重拉满后改选快成员",
           status1 == 200 and status2 == 200 and first_target == SLOW and second_target == FAST and secs2 < 8,
           "第一次命中=%s(%ss) 第二次命中=%s(%ss)" % (first_target, secs1, second_target, secs2))

    # T3 权重全 0 → 全员同分, 退化为 priority 顺序（文档化行为: 选回 priority 第一的慢成员）。
    set_weights()
    status3, secs3, _, body3 = relay(GROUP, timeout=90)
    third_target = last_target_model()
    record("T3 权重全 0 退化为 priority 顺序",
           status3 == 200 and third_target == SLOW,
           "命中=%s(%ss)" % (third_target, secs3))

    # T4 越界权重被拒（只接受 0..100）, 且不影响已存的合法值。
    status4, resp4 = call("POST", "/api/v1/setting/set", {"key": "route_weight_latency", "value": "500"})
    _, listing = call("GET", "/api/v1/setting/list")
    saved = next((s.get("value") for s in ((listing or {}).get("data") or [])
                  if s.get("key") == "route_weight_latency"), None)
    record("T4 越界权重被拒且不写入", status4 != 200 and str(saved) != "500",
           "HTTP %d 读回=%s msg=%s" % (status4, saved, str(resp4.get("message"))[:60]))

    # T5 计费字段（第二阶段）写入并读回: 建一个"贵"的 mock 渠道（倍率 3 + 按次 0.02 + 包月额度）。
    price_channel = ensure_price_channel()
    record("T5 计费字段可写入并读回",
           price_channel is not None and price_channel.get("multiplier") == 3 and price_channel.get("per_call_price") == 0.02
           and price_channel.get("billing_mode") == "per_call" and price_channel.get("monthly_quota") == 1000,
           "读回=%s" % {k: (price_channel or {}).get(k) for k in ("billing_mode", "multiplier", "per_call_price", "monthly_quota", "monthly_used")})

    # T6 倍率权重拉满 → 选同一个模型里倍率更低的那条成员（两条成员同模型、不同渠道, 靠 target_channel 区分胜负）。
    if price_channel:
        cheap_grant = grant_of("DS-TEST-mock", FAST)
        price_grant = grant_of("DS-TEST-mock-price", FAST)
        if cheap_grant and price_grant:
            existing = group_id(BILLING_GROUP)
            if existing:
                call("DELETE", "/api/v1/group/delete/%d" % existing)
            call("POST", "/api/v1/group/create", {
                "name": BILLING_GROUP, "mode": "weighted",
                "items": [{"channel_grant_id": price_grant}, {"channel_grant_id": cheap_grant}],  # 贵的排前面
                "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                                 "member_non_stream_response_timeout_seconds": 60,
                                 "member_stream_first_event_timeout_seconds": 30,
                                 "member_cooldown_seconds": 60, "member_affinity_seconds": 0},
            })
            set_weights(multiplier=100)
            status6, _, _, _ = relay(BILLING_GROUP, timeout=60)
            winner_channel = last_target_channel(BILLING_GROUP)
            record("T6 倍率权重拉满改选倍率更低的成员",
                   status6 == 200 and winner_channel == "DS-TEST-mock",
                   "命中渠道=%s（期望 DS-TEST-mock, 因为它的倍率是 1 而另一个是 3）" % winner_channel)
            set_weights()
            existing = group_id(BILLING_GROUP)
            if existing:
                call("DELETE", "/api/v1/group/delete/%d" % existing)

    # 复原与清理。
    set_weights(cost=30, quality=30, latency=15, busy=15, load=10, multiplier=15, per_call=10, balance=15, monthly=10)
    existing = group_id(GROUP)
    if existing:
        call("DELETE", "/api/v1/group/delete/%d" % existing)
    cleanup_price_channel()

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("WEIGHTED total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
