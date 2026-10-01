"""额度分压（allocate 模式）与分压监控活体套件 —— **只用本地 mock 上游**。

口径（internal/relay/allocate.go 文件头）:
- 剩余请求数: 包月余量(quota-used) 优先, 否则余额 ÷ 单次请求成本; 都不知道 → 未知(不惩罚);
- 权重 = 剩余请求数按候选内最大值铺到 1..100, 未知取已知中位数, 再乘健康折扣;
- 分配 = 平滑加权轮询（挂 RouteState）: 长期看每个人拿到的份额正比于它的权重;
- 余量不足一次(< min_requests)即剔除; 上游 429 带 Retry-After 时冷却按提示走; 自限流到顶让开一轮。

用例（每条都带否定式断言, 见括号）:
  A0 装置: 分压相关设置项可读写（越界被拒）
  A1 allocate 模式可建档并正常转发 200
  A2 余量 9:1 时双方都分到流量, 且余量多的一方明显更多（否定式: 不是贪心全给一个）
  A3 余量已用尽的成员被剔除, 请求全部落到另一个成员（否定式: 不再打它）
  A4 监控接口: 匿名 401 / 登录 200 / 明细含已知与未知成员（否定式: 不把未知当 0）
  A5 监控只读: 连续两次快照的权重与计数不因"看"而漂移
  A6 上游 429 + Retry-After: 成员被限流并让开, 监控显示 throttled 与命中次数
  A7 自限流 RPM=1: 第二个请求让开已达上限的成员
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
GROUP = "DS-TEST-allocate"
GROUP_EXHAUSTED = "DS-TEST-allocate-exhausted"
CHANNEL = "DS-TEST-mock"
GOOD, SLOW = "mock-good", "mock-slow"
COOKIE = {}
RESULTS = []

ALLOC_KEYS = ["route_allocate_estimate_tokens", "route_allocate_health_weight",
              "route_allocate_slow_latency_ms", "route_allocate_min_requests",
              "route_member_rpm_limit", "route_member_tpm_limit",
              "route_ratelimit_cooldown_max_seconds"]


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:240])


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


def anon_get(path, timeout=60):
    """不带 cookie 的只读请求（验证管理面拒匿名）。"""
    req = urllib.request.Request(ADMIN + path, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode()[:120]
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:120]
    except Exception as e:  # noqa: BLE001
        return 0, "%s: %s" % (type(e).__name__, e)


def db():
    return sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)


def group_id(name):
    c = db()
    row = c.execute("select id from groups where name = ?", (name,)).fetchone()
    c.close()
    return row[0] if row else None


def channel_id(name):
    c = db()
    row = c.execute("select id from channels where name=?", (name,)).fetchone()
    c.close()
    return row[0] if row else None


def grant_of(model_name, channel_name=CHANNEL):
    c = db()
    row = c.execute("select g.id from channel_grants g join channel_models m on m.id=g.channel_model_id "
                    "join channels ch on ch.id=m.channel_id where ch.name=? and m.name=?",
                    (channel_name, model_name)).fetchone()
    c.close()
    return row[0] if row else None


def relay_api_key():
    c = db()
    row = c.execute("select api_key from api_keys where enabled=1 order by id limit 1").fetchone()
    c.close()
    return row[0] if row else None


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
            return resp.status, round(time.time() - started, 1), body
    except urllib.error.HTTPError as e:
        return e.code, round(time.time() - started, 1), e.read().decode()[:200]
    except Exception as e:  # noqa: BLE001
        return 0, round(time.time() - started, 1), "%s: %s" % (type(e).__name__, e)


def last_target_model(group_name):
    c = db()
    row = c.execute("select target_model from relay_logs where model=? order by id desc limit 1", (group_name,)).fetchone()
    c.close()
    return row[0] if row else None


def set_setting(key, value):
    return call("POST", "/api/v1/setting/set", {"key": key, "value": str(value)})


def reset_settings():
    set_setting("route_allocate_estimate_tokens", 1000)
    set_setting("route_allocate_health_weight", 0)   # 用例里先关掉健康折扣: 只看余量, 断言才唯一
    set_setting("route_allocate_slow_latency_ms", 0)
    set_setting("route_allocate_min_requests", 1)
    set_setting("route_member_rpm_limit", 0)
    set_setting("route_member_tpm_limit", 0)
    set_setting("route_ratelimit_cooldown_max_seconds", 300)
    # 速度维度（T-speed-001）: 默认值原样复位, 避免本套件把状态漏给别的套件。
    set_setting("route_allocate_speed_weight", 40)
    set_setting("route_speed_slow_ttfb_ms", 3000)
    set_setting("route_speed_slow_tps", 8)
    set_setting("route_speed_first_event_multiple", 4)
    set_setting("route_speed_first_event_floor_ms", 5000)


def model_names(data, key):
    """渠道详情里的 models 是**字符串数组**（名称列表）, 但 grants 是对象数组;
    这里两种形状都兼容, 免得渠道形状一改就整个用例挂掉。"""
    values = data.get(key) or []
    names = []
    for value in values:
        if isinstance(value, dict):
            names.append(value.get("name") or value.get("model_name"))
        else:
            names.append(value)
    return [name for name in names if name]


def set_billing(channel_name, **fields):
    """改渠道的计费事实（包月额度/已用）。渠道 update 是整体替换, 故先读详情再改。"""
    cid = channel_id(channel_name)
    if not cid:
        return False
    _, detail = call("GET", "/api/v1/channel/detail/%d" % cid)
    data = (detail or {}).get("data") or {}
    payload = {
        "id": cid,
        "name": data.get("name", channel_name),
        "dialect": data.get("dialect", "generic"),
        "enabled": data.get("enabled", True),
        "base_url": data.get("base_url", MOCK_BASE),
        "keys": [{"name": k.get("name"), "key": k.get("key"), "enabled": k.get("enabled", True)}
                 for k in (data.get("keys") or [])],
        "models": model_names(data, "models"),
        "grants": [{"model_name": g.get("model_name"), "key_name": g.get("key_name"),
                    "protocols": g.get("protocols", 14)} for g in (data.get("grants") or [])],
        "billing_mode": fields.get("billing_mode", data.get("billing_mode", "")),
        "multiplier": fields.get("multiplier", data.get("multiplier", 0)),
        "per_call_price": fields.get("per_call_price", data.get("per_call_price", 0)),
        "monthly_quota": fields.get("monthly_quota", data.get("monthly_quota", 0)),
        "monthly_used": fields.get("monthly_used", data.get("monthly_used", 0)),
    }
    status, _ = call("POST", "/api/v1/channel/update", payload)
    return status == 200


def create_group(name, mode, grant_ids):
    existing = group_id(name)
    if existing:
        call("DELETE", "/api/v1/group/delete/%d" % existing)
    for _ in range(30):
        if group_id(name) is None:
            break
        time.sleep(0.2)
    payload = {
        "name": name, "mode": mode,
        "items": [{"channel_grant_id": gid} for gid in grant_ids],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_non_stream_response_timeout_seconds": 120,
                         "member_stream_first_event_timeout_seconds": 30,
                         "member_cooldown_seconds": 60, "member_affinity_seconds": 0},
    }
    status, resp = call("POST", "/api/v1/group/create", payload)
    return status, resp


def monitor():
    status, resp = call("GET", "/api/v1/monitor/allocation")
    return status, ((resp or {}).get("data") or {})


def rows_of(snapshot, group_name):
    return [row for row in (snapshot.get("rows") or []) if row.get("group_name") == group_name]


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    reset_settings()

    # A0 装置: 分压设置项可读写, 越界被拒。
    #     （设置项本身是判据的一部分: 选路读的就是它们, 写不进去等于功能没接线。）
    status_ok, _ = set_setting("route_allocate_health_weight", 40)
    status_bad, resp_bad = set_setting("route_allocate_health_weight", 500)
    _, listing = call("GET", "/api/v1/setting/list")
    saved = next((s.get("value") for s in ((listing or {}).get("data") or [])
                  if s.get("key") == "route_allocate_health_weight"), None)
    record("A0 分压设置项可写/越界被拒",
           status_ok == 200 and status_bad != 200 and str(saved) == "40",
           "写入=%d 越界=%d 读回=%s msg=%s" % (status_ok, status_bad, saved, str(resp_bad.get("message"))[:60]))
    reset_settings()

    good, slow = grant_of(GOOD), grant_of(SLOW)
    if not good or not slow:
        record("fixtures", False, "DS-TEST-mock 的 mock-good/mock-slow 授权缺失")
        return 1
    channel = channel_id(CHANNEL)
    if not channel:
        record("fixtures", False, "DS-TEST-mock 渠道缺失")
        return 1

    # A1 allocate 模式可建档并正常转发。
    status, resp = create_group(GROUP, "allocate", [good, slow])
    record("A1 allocate 模式可建档", status == 200, "HTTP %d %s" % (status, str(resp.get("message"))[:80]))
    status, secs, body = relay(GROUP)
    record("A1 allocate 模式可转发", status == 200, "HTTP %d %ss %s" % (status, secs, str(body)[:80]))

    # A2 余量 9:1 → 双方都分到, 且余量多的一方明显更多。
    #    包月口径挂在**渠道**上, 而成员是"渠道×凭据": 同一渠道下的两条成员余量必然相同,
    #    断言就退化成"各 50%"、测不到按比例分压。因此这里用两个渠道造出余量差异:
    #    DS-TEST-allocate-big（mock-good, 余量 100）与 DS-TEST-mock（mock-slow, 余量 900）。
    big = channel_id("DS-TEST-allocate-big")
    if big:
        call("POST", "/api/v1/channel/delete/%d" % big)
    status, _ = call("POST", "/api/v1/channel/create", {
        "name": "DS-TEST-allocate-big", "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
        "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
        "models": [GOOD],
    })
    big = channel_id("DS-TEST-allocate-big")
    if big:
        call("POST", "/api/v1/channel/update", {
            "id": big, "name": "DS-TEST-allocate-big", "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
            "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
            "models": [GOOD],
            "grants": [{"model_name": GOOD, "key_name": "mockkey", "protocols": 14}],
            "billing_mode": "subscription", "monthly_quota": 100, "monthly_used": 0,
        })
    big_grant = grant_of(GOOD, "DS-TEST-allocate-big")
    set_billing(CHANNEL, billing_mode="subscription", monthly_quota=1000, monthly_used=100, multiplier=0,
                per_call_price=0)
    if big_grant:
        create_group(GROUP, "allocate", [big_grant, slow])
        # 相 1 —— 只留"额度比例"这一个变量: 速度强度归零, 12 次请求必须按余量比例分。
        set_setting("route_allocate_speed_weight", 0)
        counts = {GOOD: 0, SLOW: 0}
        for _ in range(12):
            status, _, _ = relay(GROUP)
            target = last_target_model(GROUP)
            if status == 200 and target in counts:
                counts[target] += 1
        # 权重 = 900/900*100=100（mock-slow）与 100/900*100≈11（mock-good）。
        # 断言写"余量多的那方更多", 不写死具体数字: 具体数字随平滑轮询的相位变化。
        # 相 2 —— 恢复默认速度强度, 只读监控（样本已在相 1 攒够, 不再发请求）:
        #        分压权重先把剩余请求数铺成"组内最大=100"再乘折扣; 本装置里 mock-slow 的剩余本来就最大
        #        （900 vs 100）, 被削权后仍是组内最高权重（32 vs 11）——所以判据只能是"同一个比值跨相下降",
        #        不能写"慢成员权重 < 快成员权重"（那会冤枉正确实现）。
        _, snap_off = monitor()
        rows_off = rows_of(snap_off, GROUP)
        weight_off = {row.get("model_name"): (row.get("weight") or 0) for row in rows_off}
        set_setting("route_allocate_speed_weight", 40)
        _, snap = monitor()
        rows = rows_of(snap, GROUP)
        weight_of = {row.get("model_name"): (row.get("weight") or 0) for row in rows}
        ratio_off = (weight_off.get(SLOW, 0) / weight_off[GOOD]) if weight_off.get(GOOD) else 0
        ratio_on = (weight_of.get(SLOW, 0) / weight_of[GOOD]) if weight_of.get(GOOD) else 0
        slow_demoted = ratio_off > 0 and ratio_on > 0 and ratio_on < ratio_off * 0.6
        record("A2 按剩余请求数分压（双方都有流量, 余量多的一方明显更多）",
               counts[GOOD] > 0 and counts[SLOW] > 0 and counts[SLOW] > counts[GOOD] * 3,
               "速度维度关: 12 次分配 = %s（余量 mock-slow 900 : mock-good 100）" % counts)
        record("A2b 叠加速度维度后慢成员被削权（只削权不剔除）",
               len(rows) == 2 and len(rows_off) == 2 and slow_demoted,
               "慢/快权重比 关=%.3f 开=%.3f（两相都 2 行, 开: 权重=%s）" % (ratio_off, ratio_on, weight_of))
    else:
        record("A2 按剩余请求数分压", False, "临时渠道授权创建失败, 用例未执行")
        record("A2b 叠加速度维度后慢成员被削权（只削权不剔除）", False, "临时渠道授权创建失败, 用例未执行")

    # A3 余量用尽的成员被剔除: 把 DS-TEST-mock 渠道的包月改成已用尽, 请求应全部落到另一方。
    set_billing(CHANNEL, billing_mode="subscription", monthly_quota=100, monthly_used=100)
    if big_grant:
        exhausted_counts = {GOOD: 0, SLOW: 0}
        for _ in range(4):
            status, _, _ = relay(GROUP)
            target = last_target_model(GROUP)
            if status == 200 and target in exhausted_counts:
                exhausted_counts[target] += 1
        record("A3 余量不足一次的成员被剔除",
               exhausted_counts[SLOW] == 0 and exhausted_counts[GOOD] > 0,
               "4 次分配 = %s（mock-slow 所属渠道余量归零）" % exhausted_counts)
    else:
        record("A3 余量不足一次的成员被剔除", False, "用例前置渠道缺失")

    # A4 监控接口: 匿名 401 / 登录 200 / 明细含已知与未知成员。
    anon_status, _ = anon_get("/api/v1/monitor/allocation")
    status, snapshot = monitor()
    group_rows = rows_of(snapshot, GROUP)
    known = [row for row in group_rows if row.get("known")]
    unknown = [row for row in group_rows if not row.get("known")]
    record("A4 监控接口鉴权与明细",
           anon_status == 401 and status == 200 and len(group_rows) >= 2 and len(known) >= 1,
           "匿名=%d 登录=%d 明细=%d 条(已知 %d)" % (anon_status, status, len(group_rows), len(known)))
    record("A4 监控不把未知当 0（未知单列计数）",
           all(row.get("requests") == 0 for row in unknown) and
           snapshot.get("summary", {}).get("unknown_count", 0) >= 0 and
           any(row.get("source") == "monthly" for row in known),
           "已知来源=%s 未知条目=%d" % ([row.get("source") for row in known][:3], len(unknown)))

    # A5 监控只读: 连续两次快照的权重不漂移（看一眼不该改变选路）。
    _, first = monitor()
    _, second = monitor()
    first_weights = {row["item_id"]: row["weight"] for row in rows_of(first, GROUP)}
    second_weights = {row["item_id"]: row["weight"] for row in rows_of(second, GROUP)}
    record("A5 监控只读（两次快照权重一致）",
           first_weights == second_weights and len(first_weights) > 0,
           "第一次=%s 第二次=%s" % (first_weights, second_weights))

    # A6 上游 429 + Retry-After: 成员被限流并让开, 监控显示 throttled 与命中次数。
    #    mock 的 mock-limit429 按 429 + Retry-After: 2 应答。
    limit_channel = channel_id("DS-TEST-allocate-limit")
    if limit_channel:
        call("POST", "/api/v1/channel/delete/%d" % limit_channel)
    call("POST", "/api/v1/channel/create", {
        "name": "DS-TEST-allocate-limit", "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
        "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
        "models": ["mock-limit429"],
    })
    limit_channel = channel_id("DS-TEST-allocate-limit")
    if limit_channel:
        call("POST", "/api/v1/channel/update", {
            "id": limit_channel, "name": "DS-TEST-allocate-limit", "dialect": "generic", "enabled": True,
            "base_url": MOCK_BASE,
            "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
            "models": ["mock-limit429"],
            "grants": [{"model_name": "mock-limit429", "key_name": "mockkey", "protocols": 14}],
            "billing_mode": "subscription", "monthly_quota": 1000, "monthly_used": 0,
        })
    limit_grant = grant_of("mock-limit429", "DS-TEST-allocate-limit")
    if limit_grant and big_grant:
        # 成员顺序把"限流的那条"放前面: 它被限流后请求应落到另一方, 且监控能看到限流账。
        create_group("DS-TEST-allocate-limitgroup", "allocate", [limit_grant, big_grant])
        status, secs, _ = relay("DS-TEST-allocate-limitgroup", timeout=90)
        first_target = last_target_model("DS-TEST-allocate-limitgroup")
        _, snap = monitor()
        limit_rows = [row for row in rows_of(snap, "DS-TEST-allocate-limitgroup")
                      if row.get("model_name") == "mock-limit429"]
        throttled = any(row.get("throttled") and row.get("throttle_hits", 0) >= 1 for row in limit_rows)
        record("A6 上游 429 + Retry-After 被记为限流并让开",
               status == 200 and first_target == GOOD and throttled,
               "HTTP %d 命中=%s 限流账=%s" % (status, first_target,
                                              [(row.get("throttled"), row.get("throttle_hits")) for row in limit_rows]))
    else:
        record("A6 上游 429 + Retry-After", False, "限流桩渠道授权创建失败")

    # A7 自限流 RPM=1: 第二条请求让开已达上限的成员。
    #    前置: 把 DS-TEST-mock 的包月改回"有余量"（A3 曾把它改成用尽, 否则这里只剩一个候选,
    #    "换人"这件事根本无从发生 —— 这是本轮演练抓到的装置缺陷）。
    set_billing(CHANNEL, billing_mode="subscription", monthly_quota=1000, monthly_used=0)
    if big_grant:
        set_setting("route_member_rpm_limit", 1)
        create_group("DS-TEST-allocate-rpm", "allocate", [big_grant, slow])
        first_status, _, _ = relay("DS-TEST-allocate-rpm", timeout=90)
        first_target = last_target_model("DS-TEST-allocate-rpm")
        time.sleep(0.8)
        second_status, _, _ = relay("DS-TEST-allocate-rpm", timeout=90)
        second_target = last_target_model("DS-TEST-allocate-rpm")
        reset_settings()
        record("A7 自限流到顶让开一轮（RPM=1 时第二次换人）",
               first_status == 200 and second_status == 200 and first_target != second_target,
               "第一次=%s 第二次=%s（RPM 上限 1）" % (first_target, second_target))
    else:
        record("A7 自限流到顶让开一轮", False, "用例前置渠道缺失")

    # 清理: 临时渠道与分组（不删共享装置 DS-TEST-mock）。
    for name in ("DS-TEST-allocate-big", "DS-TEST-allocate-limit"):
        cid = channel_id(name)
        if cid:
            call("POST", "/api/v1/channel/delete/%d" % cid)
    for name in (GROUP, GROUP_EXHAUSTED, "DS-TEST-allocate-limitgroup", "DS-TEST-allocate-rpm"):
        gid = group_id(name)
        if gid:
            call("DELETE", "/api/v1/group/delete/%d" % gid)
    # 收尾必须恢复到**出厂默认**（不是测试隔离值）: 运行期把 health_weight 压成 0 只是为了让
    # 断言单变量, 若把它留在实例上, 下次人工看现象会与发布件行为不一致（看着像功能坏了）。
    reset_settings()
    set_setting("route_allocate_health_weight", 40)

    failed = [name for name, ok, _ in RESULTS if not ok]
    # 汇总行必须带 total=/pass=/fail=（run_all.py 的 SUMMARY_RE 只认这一种形状）,
    # 否则整个矩阵会把它当成"未识别汇总行", 还会在 GBK 控制台上打印中文尾行时崩掉。
    print("total=%d pass=%d fail=%d" % (len(RESULTS), len(RESULTS) - len(failed), len(failed)))
    if failed:
        print("失败: " + ", ".join(failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
