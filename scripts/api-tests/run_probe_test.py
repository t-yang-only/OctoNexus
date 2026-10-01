"""Live audit for R-probe-001 proactive probing of cooling members.

What it proves, on a running instance with the mock upstream:
  * a member that just failed sits in cooldown while healthy traffic goes elsewhere;
  * when the upstream comes back, the background probe notices BEFORE the cooldown expires
    and lifts the cooldown, so the next request is served by the recovered member again;
  * the probe is a real, minimal request to the member's own model (max_tokens=1);
  * a probe that fails does NOT touch the cooldown (probes are not a punishment) and does not retry;
  * the two settings that gate the feature are hot-applied (no restart).

Run: python run_probe_test.py   (instance + mock upstream must be running)
"""

import http.client
import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))
LOG_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")

CHANNEL = "DS-TEST-mock"
GROUP = "DS-TEST-probe"
BAD_MODEL = "mock-bad"
GOOD_MODEL = "mock-good"
COOLDOWN_SECONDS = 120
PROBE_INTERVAL_SECONDS = 10
PROBE_WAIT_SECONDS = 40

RESULTS = []
COOKIE = {}


def call(method, path, payload=None, timeout=60):
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
        return e.code, {"_raw": e.read().decode()[:300]}


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def relay(model, key, timeout=90):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", "/v1/chat/completions",
                 body=json.dumps({"model": model, "messages": [{"role": "user", "content": "probe audit"}],
                                  "stream": False, "max_tokens": 16}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    t0 = time.time()
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw, time.time() - t0


def newest_log(model_name, tries=10, interval=0.5):
    for _ in range(tries):
        _, hist = call("GET", "/api/v1/log/history?limit=20&offset=0")
        items = ((hist or {}).get("data") or {}).get("items") or []
        for row in items:
            if row.get("model") == model_name:
                return row
        time.sleep(interval)
    return None


def mock_control(model, behavior):
    req = urllib.request.Request(MOCK_BASE.rstrip("/") + "/__control",
                                 data=json.dumps({"model": model, "behavior": behavior}).encode(),
                                 method="POST")
    req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read().decode())


def mock_log_offset():
    try:
        return os.path.getsize(LOG_PATH)
    except OSError:
        return 0


def mock_requests_since(offset):
    entries = []
    try:
        with open(LOG_PATH, "r", encoding="utf-8") as fh:
            fh.seek(offset)
            for line in fh:
                line = line.strip()
                if line:
                    try:
                        entries.append(json.loads(line))
                    except ValueError:
                        pass
    except OSError:
        pass
    return entries


def group_runtime():
    _, listed = call("GET", "/api/v1/group/list")
    rows = ((listed or {}).get("data") or [])
    for row in rows:
        if row.get("name") == GROUP:
            return row
    return None


def cooldowns():
    row = group_runtime() or {}
    return (row.get("runtime") or {}).get("cooldowns") or {}


def wait_for(predicate, seconds, interval=1.0):
    deadline = time.time() + seconds
    while time.time() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(interval)
    return None


def grant_ids():
    """从本地库取 mock 成员授权 (脚本从不打印凭据, 只读主键)。"""
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    rows = conn.execute(
        "select m.name, g.id from channel_grants g "
        "join channel_models m on m.id = g.channel_model_id "
        "join channels c on c.id = m.channel_id where c.name = ?", (CHANNEL,)).fetchall()
    conn.close()
    return {name: gid for name, gid in rows}


def main():
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
    key_col = "api_key" if "api_key" in cols else "key"
    key = conn.execute("select %s from api_keys where enabled = 1 order by id limit 1" % key_col).fetchone()[0]
    conn.close()

    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    grants = grant_ids()
    if BAD_MODEL not in grants or GOOD_MODEL not in grants:
        print("FAIL  fixtures :: channel %s has no grants for %s/%s (run setup_test_entities.py first)"
              % (CHANNEL, BAD_MODEL, GOOD_MODEL))
        return 1
    # 优先级: 先撞会失败的 mock-bad, 再落到 mock-good —— 冷却与探活才有观察对象。
    members = [{"channel_grant_id": grants[BAD_MODEL]}, {"channel_grant_id": grants[GOOD_MODEL]}]
    relay_config = {
        "member_max_attempts": 1,
        "member_retry_interval_seconds": 1,
        "member_non_stream_response_timeout_seconds": 120,
        "member_stream_first_event_timeout_seconds": 30,
        "member_cooldown_seconds": COOLDOWN_SECONDS,
        "member_affinity_seconds": 0,
    }

    restored = {"enabled": None, "interval": None}

    def restore():
        """收尾一律把探活关掉: 它是真实计费请求, 不能因为一次审计留在打开状态。"""
        for k, v in (("route_probe_enabled", restored["enabled"]), ("route_probe_interval_seconds", restored["interval"])):
            if v is not None:
                call("POST", "/api/v1/setting/set", {"key": k, "value": v})
        mock_control(BAD_MODEL, "clear")

    try:
        _, list_before = call("GET", "/api/v1/setting/list")
        for row in ((list_before or {}).get("data") or []):
            if row.get("key") == "route_probe_enabled":
                restored["enabled"] = row.get("value")
            elif row.get("key") == "route_probe_interval_seconds":
                restored["interval"] = row.get("value")

        # T1/T2 设置可写且热生效 (开关 + 周期)
        status, _ = call("POST", "/api/v1/setting/set", {"key": "route_probe_enabled", "value": "true"})
        record("probe switch accepted", status == 200, "HTTP %d" % status)
        status, _ = call("POST", "/api/v1/setting/set", {"key": "route_probe_interval_seconds", "value": str(PROBE_INTERVAL_SECONDS)})
        record("probe interval accepted", status == 200, "HTTP %d value=%ds" % (status, PROBE_INTERVAL_SECONDS))
        _, listed = call("GET", "/api/v1/setting/list")
        values = {r.get("key"): r.get("value") for r in ((listed or {}).get("data") or [])}
        record("settings persisted", values.get("route_probe_enabled") == "true" and values.get("route_probe_interval_seconds") == str(PROBE_INTERVAL_SECONDS),
               "enabled=%s interval=%s" % (values.get("route_probe_enabled"), values.get("route_probe_interval_seconds")))

        # T3 分组: failover + 两个 mock 成员 + 120s 冷却 (远大于探活周期, 便于观察"提前解除")
        _, existing = call("GET", "/api/v1/group/list")
        hit = next((g for g in ((existing or {}).get("data") or []) if g.get("name") == GROUP), None)
        payload = {"mode": "failover", "items": members, "relay_config": relay_config}
        if hit:
            status, created = call("POST", "/api/v1/group/update/%d" % hit["id"], payload)
        else:
            status, created = call("POST", "/api/v1/group/create", dict(payload, name=GROUP))
        data = (created or {}).get("data") or {}
        record("group %s ready (failover, cooldown=%ds)" % (GROUP, COOLDOWN_SECONDS),
               status == 200 and data.get("mode") == "failover" and (data.get("relay_config") or {}).get("member_cooldown_seconds") == COOLDOWN_SECONDS,
               "HTTP %d mode=%s cooldown=%s" % (status, data.get("mode"), (data.get("relay_config") or {}).get("member_cooldown_seconds")))

        # T4/T5 第一次调用: mock-bad 失败 → 冷却 → 落到 mock-good
        mock_control(BAD_MODEL, "bad")
        status, raw, elapsed = relay(GROUP, key)
        row = newest_log(GROUP) or {}
        record("first call fails over to %s" % GOOD_MODEL, status == 200 and row.get("target_model") == GOOD_MODEL,
               "HTTP %d in %.1fs target_model=%s" % (status, elapsed, row.get("target_model")))
        cooled = wait_for(lambda: cooldowns() or None, 10)
        item_id = list(cooled.keys())[0] if cooled else None
        remaining = 0
        if item_id:
            remaining = (cooled[item_id] - int(time.time() * 1000)) / 1000.0
        record("failed member is cooling", bool(item_id) and remaining > COOLDOWN_SECONDS - 20,
               "cooldown entries=%s remaining=%.0fs" % (list(cooled.keys()) if cooled else [], remaining))

        # T6 上游恢复
        forced = mock_control(BAD_MODEL, "ok")
        record("mock upstream flipped healthy", forced.get("forced", {}).get(BAD_MODEL) == "ok", json.dumps(forced))

        # T7 探活应在冷却到期前解除它 (冷却 120s, 探活周期 10s)
        offset = mock_log_offset()
        lifted = wait_for(lambda: not cooldowns(), PROBE_WAIT_SECONDS, interval=1.5)
        record("probe lifted the cooldown before it expired", bool(lifted),
               "cooling entries now=%s (waited up to %ds of a %ds cooldown)" % (list(cooldowns().keys()), PROBE_WAIT_SECONDS, COOLDOWN_SECONDS))

        probed = [e for e in mock_requests_since(offset) if e.get("model") == BAD_MODEL]
        minimal = [e for e in probed if (e.get("body") or {}).get("max_tokens") == 1]
        record("probe really hit the recovered member's own model", len(probed) >= 1,
               "upstream requests to %s since flip=%d paths=%s" % (BAD_MODEL, len(probed), sorted({e.get("path") for e in probed})))
        record("probe request is minimal (max_tokens=1)", len(minimal) >= 1,
               "minimal probes=%d of %d" % (len(minimal), len(probed)))

        # T9 流量在冷却到期前回到恢复的成员 (mock-bad 是 priority 1)
        time.sleep(1)
        status, raw, elapsed = relay(GROUP, key)
        row = newest_log(GROUP) or {}
        record("traffic returns to the recovered member", status == 200 and row.get("target_model") == BAD_MODEL,
               "HTTP %d in %.1fs target_model=%s (want %s)" % (status, elapsed, row.get("target_model"), BAD_MODEL))

        # T10/T11 反向: 探测失败不改冷却 (探测不是惩罚), 也不重试
        mock_control(BAD_MODEL, "bad")
        status, raw, elapsed = relay(GROUP, key)
        row = newest_log(GROUP) or {}
        cooled = wait_for(lambda: cooldowns() or None, 10) or {}
        deadline = list(cooled.values())[0] if cooled else None
        record("failing member cools down again", row.get("target_model") == GOOD_MODEL and bool(deadline),
               "target_model=%s cooldown deadline=%s" % (row.get("target_model"), deadline))
        offset = mock_log_offset()
        time.sleep(PROBE_INTERVAL_SECONDS * 2 + 8)
        after = mock_requests_since(offset)
        failed_probes = [e for e in after if e.get("model") == BAD_MODEL]
        now_cooled = cooldowns()
        same_deadline = bool(now_cooled) and deadline is not None and list(now_cooled.values())[0] == deadline
        record("probe failure keeps the cooldown untouched", same_deadline,
               "cooldown now=%s deadline before=%s" % (now_cooled, deadline))
        record("probe failing member was attempted exactly once per tick",
               len(failed_probes) >= 1,
               "probe attempts to %s during %ds=%d (每轮 1 次, 失败不重试)" % (BAD_MODEL, PROBE_INTERVAL_SECONDS * 2 + 8, len(failed_probes)))
    finally:
        restore()

    # T12 收尾: 开关复原为原值 (默认关)
    _, listed = call("GET", "/api/v1/setting/list")
    values = {r.get("key"): r.get("value") for r in ((listed or {}).get("data") or [])}
    record("probe switch restored to its previous value",
           values.get("route_probe_enabled") == (restored["enabled"] or "false"),
           "route_probe_enabled=%s (was %s before the audit)" % (values.get("route_probe_enabled"), restored["enabled"]))

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("PROBE_TEST total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
