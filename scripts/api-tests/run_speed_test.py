"""速度观测与速度应对活体套件（T-speed-001）—— **只用本地 mock 上游**。

口径（internal/relay/speed.go 文件头）:
- 速度 = 首帧耗时（TTFB, 只有流式有真值）+ 输出吞吐（completion tokens ÷ 本轮耗时）;
- 样本不足（<3）不下结论: 不打折、不收预算;
- 折扣只削权不剔除，看门狗只会更早放弃（预算永远 <= 分组配置）。

用例（每条都有否定式对照）:
  V0 速度设置项可读写、越界被拒
  V1 成功流式请求后监控页显示首帧与吞吐（否定式: 不是 0、不是"量测中"）
  V2 慢成员被削权: 慢成员权重 < 快成员（且两者都仍参与分配, 不是被剔除）
  V3 自适应首帧看门狗真的提前放弃: 先把这个成员量成"快", 再让它变慢 ——
     开着看门狗时该成员在 ~5s 内被掐断并进冷却, 关掉时同一请求等到上游 15s 拿到首帧、不进冷却
  V4 关闭速度维度（强度 0 / 倍数 0）后行为回到改造前: 监控里不再判慢
"""

import json
import os
import sqlite3
import sys
import threading
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY = "http://%s:%s" % (os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1"),
                          os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
MOCK_ADMIN = os.environ.get("OCTOPUS_MOCK_ADMIN", "http://127.0.0.1:18099")
CHANNEL = "DS-TEST-mock"
FAST, SLOW = "mock-good", "mock-slow"
GROUP_SPREAD = "DS-TEST-speed-spread"
GROUP_SOLO = "DS-TEST-speed-solo"
GROUP_OFF = "DS-TEST-speed-off"
COOKIE = {}
RESULTS = []

SPEED_KEYS = ["route_allocate_speed_weight", "route_speed_slow_ttfb_ms", "route_speed_slow_tps",
              "route_speed_first_event_multiple", "route_speed_first_event_floor_ms"]


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


def relay_key():
    c = db()
    row = c.execute("select api_key from api_keys where enabled=1 order by id limit 1").fetchone()
    c.close()
    return row[0] if row else None


def relay_stream(model, timeout=90):
    """发一次流式请求, 返回 (status, 秒数, 是否拿到过任何事件)。"""
    payload = {"model": model, "max_tokens": 16, "stream": True,
               "messages": [{"role": "user", "content": "ping"}]}
    req = urllib.request.Request(RELAY + "/v1/chat/completions", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + relay_key())
    started = time.time()
    got_event = False
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for _ in resp:
                got_event = True
    except urllib.error.HTTPError:
        return "http_error", round(time.time() - started, 1), got_event
    except Exception:  # noqa: BLE001
        return "timeout", round(time.time() - started, 1), got_event
    return "ok", round(time.time() - started, 1), got_event


def mock_control(model, behavior):
    req = urllib.request.Request(MOCK_ADMIN + "/__control",
                                 data=json.dumps({"model": model, "behavior": behavior}).encode(),
                                 method="POST")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return resp.status
    except Exception as e:  # noqa: BLE001
        return "%s: %s" % (type(e).__name__, e)


def set_setting(key, value):
    return call("POST", "/api/v1/setting/set", {"key": key, "value": str(value)})[0]


def reset_settings():
    set_setting("route_allocate_speed_weight", 40)
    set_setting("route_speed_slow_ttfb_ms", 3000)
    set_setting("route_speed_slow_tps", 8)
    set_setting("route_speed_first_event_multiple", 4)
    set_setting("route_speed_first_event_floor_ms", 5000)


def create_group(name, mode, grant_ids, first_event_timeout=30):
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
                         "member_stream_first_event_timeout_seconds": first_event_timeout,
                         "member_cooldown_seconds": 30, "member_affinity_seconds": 0},
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

    # V0 设置项可读写、越界被拒（越界必须是参数错, 不能静默夹紧）。
    ok_status = set_setting("route_allocate_speed_weight", 40)
    bad_status, bad_resp = call("POST", "/api/v1/setting/set", {"key": "route_allocate_speed_weight", "value": "500"})
    _, listing = call("GET", "/api/v1/setting/list")
    saved = next((s.get("value") for s in ((listing or {}).get("data") or [])
                  if s.get("key") == "route_allocate_speed_weight"), None)
    record("V0 速度设置项可写 / 越界被拒",
           ok_status == 200 and bad_status != 200 and str(saved) == "40",
           "写入=%s 越界=%s 读回=%s msg=%s" % (ok_status, bad_status, saved, str(bad_resp.get("message"))[:60]))

    fast_grant, slow_grant = grant_of(FAST), grant_of(SLOW)
    if not fast_grant or not slow_grant:
        record("fixtures", False, "DS-TEST-mock 的 mock-good/mock-slow 授权缺失")
        return 1
    # 余量必须充足, 否则"谁被选中"会被余量维度主导, 测不到速度这一维。
    cid = channel_id(CHANNEL)
    _, detail = call("GET", "/api/v1/channel/detail/%d" % cid)
    data = (detail or {}).get("data") or {}
    call("POST", "/api/v1/channel/update", {
        "id": cid, "name": data.get("name", CHANNEL), "dialect": data.get("dialect", "generic"),
        "enabled": True, "base_url": data.get("base_url", MOCK_BASE),
        "keys": [{"name": k.get("name"), "key": k.get("key"), "enabled": k.get("enabled", True)}
                 for k in (data.get("keys") or [])],
        "models": data.get("models") or [],
        "grants": [{"model_name": g.get("model_name"), "key_name": g.get("key_name"),
                    "protocols": g.get("protocols", 14)} for g in (data.get("grants") or [])],
        "billing_mode": "subscription", "multiplier": 0, "per_call_price": 0,
        "monthly_quota": 1000000, "monthly_used": 0,
    })

    # V1+V2 装置: 先关掉看门狗（倍数 0），让"慢成员"的每一轮都能跑完（15s）从而攒够样本 ——
    # 否则被看门狗切断的轮次按失败记账、不记速度样本，慢成员永远到不了"可下结论"的样本门槛，
    # 那条"慢成员被削权"的断言就成了对装置的断言而不是对功能的断言。
    set_setting("route_speed_first_event_multiple", 0)
    create_group(GROUP_SPREAD, "allocate", [fast_grant, slow_grant])
    for _ in range(6):
        relay_stream(GROUP_SPREAD, timeout=60)
    status, snapshot = monitor()
    group_rows = rows_of(snapshot, GROUP_SPREAD)
    by_model = {row.get("model_name"): row for row in group_rows}
    fast_row = by_model.get(FAST, {})
    slow_row = by_model.get(SLOW, {})
    record("V1 监控页显示首帧与吞吐（不是 0、不是量测中）",
           status == 200 and fast_row.get("speed_samples", 0) > 0 and fast_row.get("ttfb_ms", 0) > 0
           and fast_row.get("tokens_per_sec", 0) > 0,
           "快成员样本=%s 首帧=%sms 吞吐=%.2f tok/s" % (fast_row.get("speed_samples"), fast_row.get("ttfb_ms"),
                                                     fast_row.get("tokens_per_sec", 0)))
    record("V2 慢成员被削权（权重更低但仍在分配里, 不是被剔除）",
           slow_row.get("slow") is True and slow_row.get("speed_factor", 1) < 1
           and slow_row.get("weight", 0) >= 1 and fast_row.get("speed_factor", 0) == 1
           and slow_row.get("weight", 100) < fast_row.get("weight", 0),
           "快成员 factor=%.2f 权重=%s | 慢成员 factor=%.2f 权重=%s slow=%s 样本=%s" % (
               fast_row.get("speed_factor", 0), fast_row.get("weight"), slow_row.get("speed_factor", 0),
               slow_row.get("weight"), slow_row.get("slow"), slow_row.get("speed_samples")))

    # V3 看门狗差分对照: 同一个装置（单成员 + 先量三次快 + 再让上游变慢）, 只改"倍数"这一个变量。
    #    开着 → 预算收紧到 5s, 请求在 ~5s 内被放弃; 关掉 → 必须等满上游的 15s。
    def watchdog_case(multiple):
        """只改一个变量（看门狗倍数）, 用"成员何时被掐断"作为可观测事实。

        装置说明: 单成员分组被看门狗掐断后该成员进入冷却, 这个请求会落进既有的"等可用成员"
        循环里一直等到客户端超时 —— 所以「客户端可见耗时」不是本机制的观测量（关掉看门狗
        同样会等满上游 15s）。真正可差分观测的是: 开 = 成员在 ~5s 进冷却(掐断发生);
        关 = 成员拿到首帧、全程不进冷却。这正是看门狗的作用点。
        """
        set_setting("route_speed_first_event_multiple", multiple)
        mock_control(FAST, "clear")
        create_group(GROUP_SOLO, "allocate", [fast_grant], first_event_timeout=30)
        for _ in range(3):
            relay_stream(GROUP_SOLO, timeout=60)   # 量成"快"（建立首帧样本）
        _, warm_snapshot = monitor()
        warm = (rows_of(warm_snapshot, GROUP_SOLO) or [{}])[0]
        samples = warm.get("speed_samples", 0)
        mock_control(FAST, "slow")
        seen = {}

        def worker():
            state, seconds, got_event = relay_stream(GROUP_SOLO, timeout=45)
            seen["state"] = state
            seen["seconds"] = seconds
            seen["first_event"] = got_event

        thread = threading.Thread(target=worker, daemon=True)
        started = time.time()
        thread.start()
        cooled_at = None
        first_event_at = None
        while time.time() - started < 40:
            if seen.get("first_event") is True:
                first_event_at = seen.get("seconds")
                break
            _, snap = monitor()
            rows = rows_of(snap, GROUP_SOLO)
            if rows and rows[0].get("cooling"):
                cooled_at = round(time.time() - started, 1)
                break
            time.sleep(0.3)
        mock_control(FAST, "clear")
        if cooled_at is None and first_event_at is None:
            thread.join(timeout=45)
            if seen.get("first_event") is True:
                first_event_at = seen.get("seconds")
        return samples, cooled_at, first_event_at

    try:
        samples_on, cooled_on, _ = watchdog_case(4)
        samples_off, cooled_off, first_off = watchdog_case(0)
    finally:
        mock_control(FAST, "clear")
        reset_settings()
    record("V3 自适应首帧看门狗提前掐断慢成员（开=~5s 进冷却, 关=等满上游 15s 拿到首帧）",
           samples_on > 0 and samples_off > 0 and cooled_on is not None and cooled_on <= 10
           and cooled_off is None and first_off is not None and first_off >= 13,
           "开: 样本=%s 掐断=%ss | 关: 样本=%s 冷却=%s 首帧=%ss" % (
               samples_on, cooled_on, samples_off, cooled_off, first_off))

    # V4 关掉速度维度后不再判慢（否定式: 全关 = 回到改造前的行为）。
    set_setting("route_allocate_speed_weight", 0)
    set_setting("route_speed_first_event_multiple", 0)
    status, snapshot = monitor()
    off_rows = rows_of(snapshot, GROUP_SPREAD)
    record("V4 关掉速度维度后不判慢、不打折",
           status == 200 and all(not row.get("slow") and row.get("speed_factor", 1) == 1 for row in off_rows),
           "slow=%s factors=%s" % ([row.get("slow") for row in off_rows],
                                   [row.get("speed_factor") for row in off_rows]))
    reset_settings()

    # 清理: 只删本轮新建的分组（不动共享装置 DS-TEST-mock）。
    for name in (GROUP_SPREAD, GROUP_SOLO, GROUP_OFF):
        gid = group_id(name)
        if gid:
            call("DELETE", "/api/v1/group/delete/%d" % gid)

    failed = [name for name, ok, _ in RESULTS if not ok]
    print("total=%d pass=%d fail=%d" % (len(RESULTS), len(RESULTS) - len(failed), len(failed)))
    if failed:
        print("失败: " + ", ".join(failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
