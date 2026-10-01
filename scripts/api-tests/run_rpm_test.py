"""lowest_tpm_rpm（近期消耗最低）活体验证（NM-DS-014 / T-route-007）。

成员：DS-TEST-mock 渠道的 mock-slow（上游 sleep 15s，priority 1）与 mock-good（正常，priority 2）。

场景：
  ① 冷启动：两个成员在 60s 窗口内都没有记录 → 平手 → 按 priority 选中 mock-slow（耗时 ≈15s）；
  ② 紧接着的第二次请求：mock-slow 在窗口内已有 1 次尝试 → 改选窗口内零消耗的 mock-good（耗时 ≈0s）；
  ③ 第三次仍选 mock-good（窗口是 60s，不是"当次"）；
  ④ 对照组 DS-TEST-rpmf（failover、同成员）：同样的时序仍按 priority 撞 mock-slow，
     证明②③的差异来自模式而不是成员本身；
  ⑤ 窗口过期：等过 memberLoadWindowMs（60s）后，mock-slow 的窗口记录失效，重新按 priority 被选中
     （此时用 mock 控制接口把它临时改成快速应答，避免再等 15s）。

Run: python run_rpm_test.py   （实例 + mock 上游必须在跑）
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
MOCK_ROOT = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")[:-3]
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))

GROUP = "DS-TEST-rpm"
CONTROL = "DS-TEST-rpmf"
SLOW = "mock-slow"
GOOD = "mock-good"
WINDOW_SECONDS = 60

COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def call(method, path, payload=None, timeout=120):
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
    except Exception as e:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(e).__name__, e)}


def relay(model, key, timeout=90):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", "/v1/chat/completions",
                 body=json.dumps({"model": model, "messages": [{"role": "user", "content": "rpm?"}],
                                  "stream": False, "max_tokens": 16}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    started = time.time()
    resp = conn.getresponse()
    resp.read()
    conn.close()
    return resp.status, round(time.time() - started, 2)


def log_len():
    if not os.path.exists(MOCK_LOG):
        return 0
    with open(MOCK_LOG, "r", encoding="utf-8", errors="ignore") as fh:
        return len(fh.readlines())


def log_models(since):
    out = []
    with open(MOCK_LOG, "r", encoding="utf-8", errors="ignore") as fh:
        for line in fh.readlines()[since:]:
            try:
                entry = json.loads(line)
            except ValueError:
                continue
            if entry.get("method") == "POST" and entry.get("model"):
                out.append(entry.get("model"))
    return out


def mock_control(model, behavior, usage_scale=None):
    payload = {"model": model, "behavior": behavior}
    if usage_scale is not None:
        payload["usage_scale"] = usage_scale
    req = urllib.request.Request(MOCK_ROOT + "/__control", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read().decode())


def grant_ids():
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    rows = conn.execute(
        "select m.name, g.id from channel_grants g "
        "join channel_models m on m.id = g.channel_model_id "
        "join channels c on c.id = m.channel_id where c.name = 'DS-TEST-mock'").fetchall()
    conn.close()
    return {name: gid for name, gid in rows}


def ensure_group(name, mode, ids, key, fresh=False):
    """建组/更新组。fresh=True 时先删后建：成员行会拿到新的 GroupItem.ID，
    于是进程内"近期负载"窗口对这些成员天然是干净的（负载按成员行聚合），用例不必先等 60s。"""
    _, existing = call("GET", "/api/v1/group/list")
    hit = next((g for g in ((existing or {}).get("data") or []) if g.get("name") == name), None)
    payload = {
        "mode": mode,
        "items": [{"channel_grant_id": ids[SLOW]}, {"channel_grant_id": ids[GOOD]}],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_non_stream_response_timeout_seconds": 120,
                         "member_stream_first_event_timeout_seconds": 30,
                         "member_cooldown_seconds": 5, "member_affinity_seconds": 0},
    }
    if hit and fresh:
        call("DELETE", "/api/v1/group/delete/%d" % hit["id"])
        hit = None
    if hit:
        status, _ = call("POST", "/api/v1/group/update/%d" % hit["id"], payload)
    else:
        status, _ = call("POST", "/api/v1/group/create", dict(payload, name=name))
    return status


def main():
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
    key_col = "api_key" if "api_key" in cols else "key"
    key = conn.execute("select %s from api_keys where enabled = 1 order by id limit 1" % key_col).fetchone()[0]
    conn.close()

    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    ids = grant_ids()
    if SLOW not in ids or GOOD not in ids:
        record("test members exist", False, "DS-TEST-mock grants missing: %s" % sorted(ids))
        return 1
    create_rpm = ensure_group(GROUP, "lowest_tpm_rpm", ids, key, fresh=True)
    create_ctl = ensure_group(CONTROL, "failover", ids, key)
    record("lowest_tpm_rpm mode accepted by the API", create_rpm == 200 and create_ctl == 200,
           "create/update statuses: rpm=%s failover=%s" % (create_rpm, create_ctl))
    time.sleep(1)
    # 成员行刚重建 → 负载窗口干净；先确认选路确实从"零记录"起步。
    _, groups = call("GET", "/api/v1/group/list")
    members = next((g for g in ((groups or {}).get("data") or []) if g.get("name") == GROUP), None)
    record("the rpm group was rebuilt with fresh member rows", bool(members) and len(members.get("items") or []) == 2,
           "members=%s" % [item.get("id") for item in ((members or {}).get("items") or [])])

    try:
        # ① 冷启动：窗口内都没有记录 → 平手 → priority 选中 mock-slow（真实 15s，顺带证明"乐观先验"）。
        mark = log_len()
        status, elapsed = relay(GROUP, key)
        used = log_models(mark)
        record("cold start falls back to priority (no load records in the window)",
               status == 200 and used == [SLOW] and elapsed >= 14,
               "status=%s upstream=%s elapsed=%.2fs" % (status, used, elapsed))

        # 后面几步只关心"选谁"，与上游快慢无关，故把慢成员临时改成快速应答，省掉多次 15s。
        mock_control(SLOW, "ok")

        # ② mock-good 窗口内零消耗 → 让开刚消耗过的 mock-slow。
        mark = log_len()
        status, elapsed = relay(GROUP, key)
        used = log_models(mark)
        record("a member with no recent consumption is preferred",
               status == 200 and used == [GOOD] and elapsed < 5,
               "status=%s upstream=%s elapsed=%.2fs" % (status, used, elapsed))

        # ③ 两边都消耗过一次（请求数与 token 数相同）→ 按 priority 决胜。
        #    这是口径而不是缺陷：消耗相同时按 priority，等价于在两个同样空闲的成员间交替。
        mark = log_len()
        status, elapsed = relay(GROUP, key)
        used = log_models(mark)
        record("equal consumption falls back to priority (documented tie rule)",
               status == 200 and used == [SLOW],
               "status=%s upstream=%s elapsed=%.2fs" % (status, used, elapsed))

        # ④ 于是 mock-good 又变成消耗更少的一方 → 再次被优先选中（窗口内的让开是可重复的）。
        mark = log_len()
        status, elapsed = relay(GROUP, key)
        used = log_models(mark)
        record("the windowed avoidance repeats while one side stays lighter",
               status == 200 and used == [GOOD] and elapsed < 5,
               "status=%s upstream=%s elapsed=%.2fs" % (status, used, elapsed))

        # ⑤ token 优先于请求数：此刻双方都是 2 次尝试 / 36 token（平手）。
        #    r1 按 priority 落到 mock-slow；随后把 mock-good 的 token 计数放大 40 倍，
        #    r2 因 mock-good 消耗更少而被选中，并真的吃掉 720 个 token；
        #    r3 时两边**请求数相同（各 3 次）**，但 mock-good 的 token 远大于 mock-slow —— 必须选 mock-slow。
        mock_control(GOOD, "ok", usage_scale=40)
        mark = log_len()
        relay(GROUP, key)
        r1 = log_models(mark)
        mark = log_len()
        relay(GROUP, key)
        r2 = log_models(mark)
        _, logged = call("GET", "/api/v1/log/history?limit=5&offset=0")
        heavy_rows = [row for row in ((logged or {}).get("data") or {}).get("items", [])
                      if row.get("target_model") == GOOD and row.get("status") == "success"]
        heavy = heavy_rows[0] if heavy_rows else {}
        mark = log_len()
        status, elapsed = relay(GROUP, key)
        r3 = log_models(mark)
        record("scaled token usage is recorded for the heavy member",
               heavy.get("prompt_tokens") == 440 and heavy.get("completion_tokens") == 280,
               "history row tokens=%s/%s (want 440/280)" % (heavy.get("prompt_tokens"), heavy.get("completion_tokens")))
        record("token consumption outweighs request count (TPM first)",
               r1 == [SLOW] and r2 == [GOOD] and r3 == [SLOW],
               "r1=%s r2=%s r3=%s (equal request counts, token-heavy member passed over)" % (r1, r2, r3))
        mock_control(GOOD, "clear", usage_scale=1)

        # ⑥ 对照组：同样的成员与 priority，failover 仍然撞 mock-slow。
        mark = log_len()
        status, elapsed = relay(CONTROL, key)
        used = log_models(mark)
        record("failover control still follows priority",
               status == 200 and used == [SLOW],
               "status=%s upstream=%s elapsed=%.2fs" % (status, used, elapsed))

        # ⑦ 窗口过期后已消耗的成员重新平等参与（此时它已被改成快速应答，省掉 15s 等待）。
        remaining = WINDOW_SECONDS + 3
        print("  ... waiting %ds for the load window to expire" % remaining)
        time.sleep(remaining)
        mark = log_len()
        status, elapsed = relay(GROUP, key)
        used = log_models(mark)
        record("after the window expires the previously loaded member is eligible again",
               status == 200 and used == [SLOW] and elapsed < 5,
               "status=%s upstream=%s elapsed=%.2fs" % (status, used, elapsed))
    finally:
        try:
            mock_control(SLOW, "clear")
            mock_control(GOOD, "clear", usage_scale=1)
        except Exception:  # noqa: BLE001
            pass

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("RPM_TEST total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
