"""Timeout switchover + failover tests against the local octopus relay.

Groups under test (created by setup_test_entities.py, relay_config:
member_max_attempts=2, member_retry_interval_seconds=1,
member_non_stream_response_timeout_seconds=4,
member_stream_first_event_timeout_seconds=4, member_cooldown_seconds=30):

  DS-TEST-failover : mock-bad (immediate 500) -> mock-good
  DS-TEST-timeout  : mock-slow (sleeps 15s)   -> mock-good
  DS-TEST-allslow  : mock-slow only           (created here: total failure path)

Evidence used: relay HTTP status/latency, mock upstream request log
(which member was actually hit, how many times), stats_total deltas.

Run: python run_failover_tests.py
"""

import http.client
import json
import os
import sqlite3
import sys
import time
import urllib.request

RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
DB = r"D:\奇怪的软件\octopus\data\data.db"
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")

RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, ok, detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def relay_api_key():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    rows = conn.execute("select id, name, api_key, supported_models from api_keys where enabled=1").fetchall()
    conn.close()
    for _id, name, key, sup in rows:
        if not sup or sup in ("[]", "null", ""):
            return key, name, _id
    if rows:
        return rows[0][2], rows[0][1], rows[0][0]
    raise SystemExit("no enabled api key in local DB")


class Admin:
    def __init__(self):
        self.cookie = None
        self.call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    def call(self, method, path, payload=None):
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(ADMIN + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if self.cookie:
            req.add_header("Cookie", self.cookie)
        with urllib.request.urlopen(req, timeout=60) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    self.cookie = v.split(";")[0]
            return json.loads(resp.read().decode())

    def ensure_streamtimeout(self):
        """专用流式超时组: mock-slow2 只有它自己会挂, 与 mock-slow 的冷却互不干扰。

        流式分支走的是 MemberStreamFirstEventTimeoutSeconds (等待首个流事件),
        与非流式的整响应超时是两条不同代码路径, 必须各用一组慢成员。
        """
        grants = self.call("GET", "/api/v1/channel/grants").get("data") or []
        mine = [g for g in grants if g.get("channel_name") == "DS-TEST-mock"]
        slow2 = [g["id"] for g in mine if g.get("model_name") == "mock-slow2"]
        good = [g["id"] for g in mine if g.get("model_name") == "mock-good"]
        if not slow2:
            stats = self.call("GET", "/api/v1/channel/stats").get("data") or []
            cid = next((c["channel_id"] for c in stats if c.get("channel_name") == "DS-TEST-mock"), None)
            if cid is None:
                raise SystemExit("DS-TEST-mock channel missing")
            detail = self.call("GET", f"/api/v1/channel/detail/{cid}").get("data")
            models = list(dict.fromkeys((detail.get("models") or []) + ["mock-slow2"]))
            keys = [{"name": k["name"], "key": "mock-secret-not-a-real-key", "enabled": True}
                    for k in (detail.get("keys") or [])] or \
                   [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}]
            grant_cfgs = []
            for m in models:
                protocols = 2 if m == "mock-chatonly" else 14
                grant_cfgs.append({"model_name": m, "key_name": keys[0]["name"], "protocols": protocols})
            self.call("POST", "/api/v1/channel/update", {
                "id": cid, "name": "DS-TEST-mock", "dialect": "generic", "enabled": True,
                "base_url": os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1"), "keys": keys, "models": models, "grants": grant_cfgs})
            grants = self.call("GET", "/api/v1/channel/grants").get("data") or []
            mine = [g for g in grants if g.get("channel_name") == "DS-TEST-mock"]
            slow2 = [g["id"] for g in mine if g.get("model_name") == "mock-slow2"]
            good = [g["id"] for g in mine if g.get("model_name") == "mock-good"]
        if not slow2 or not good:
            raise SystemExit(f"cannot prepare stream timeout group: slow2={slow2} good={good}")
        relay_config = {"member_max_attempts": 2, "member_retry_interval_seconds": 1,
                        "member_non_stream_response_timeout_seconds": 4,
                        "member_stream_first_event_timeout_seconds": 4,
                        "member_cooldown_seconds": 3, "member_affinity_seconds": 0}
        groups = self.call("GET", "/api/v1/group/list").get("data") or []
        existing = next((g for g in groups if g.get("name") == "DS-TEST-streamtimeout"), None)
        if existing:
            # 已存在也必须按目标配置更新：否则早先建的组会带着旧冷却时长，跨轮次残留冷却让流式用例假失败。
            self.call("POST", f"/api/v1/group/update/{existing['id']}",
                      {"mode": "failover", "relay_config": relay_config,
                       "items": [{"channel_grant_id": slow2[0]}, {"channel_grant_id": good[0]}]})
            return existing["id"]
        r = self.call("POST", "/api/v1/group/create",
                      {"name": "DS-TEST-streamtimeout", "mode": "failover", "relay_config": relay_config,
                       "items": [{"channel_grant_id": slow2[0]}, {"channel_grant_id": good[0]}]})
        print("created DS-TEST-streamtimeout:", json.dumps(r.get("data"), ensure_ascii=False)[:120])
        return None

    def reset_cooldowns(self, seconds=3):
        """把本套件用到的分组统一改成短冷却并按原样重建成员列表。

        冷却存在进程内的分组路由状态里，跨轮次会残留：上一轮被冷却的慢成员在下一轮开头
        会被直接跳过，于是"第一次尝试必撞慢成员"的超时用例假失败。这里在用例开始前重置。
        """
        names = ("DS-TEST-failover", "DS-TEST-timeout", "DS-TEST-allslow")
        groups = self.call("GET", "/api/v1/group/list").get("data") or []
        for group in groups:
            if group.get("name") not in names:
                continue
            config = dict(group.get("relay_config") or {})
            config["member_cooldown_seconds"] = seconds
            items = [{"channel_grant_id": item.get("channel_grant_id")} for item in (group.get("items") or [])
                     if item.get("channel_grant_id")]
            self.call("POST", f"/api/v1/group/update/{group['id']}",
                      {"mode": group.get("mode"), "relay_config": config, "items": items})

    def wait_cooldown(self, seconds=4):
        """等过本组冷却（3s），让每个用例都从"两个成员都可用"的干净状态开始。"""
        time.sleep(seconds)

    def stats(self):
        return self.call("GET", "/api/v1/stats/total").get("data") or {}

    def ensure_allslow(self):
        grants = self.call("GET", "/api/v1/channel/grants").get("data") or []
        slow = [g["id"] for g in grants if g.get("model_name") == "mock-slow" and g.get("channel_name") == "DS-TEST-mock"]
        if not slow:
            raise SystemExit("mock-slow grant missing")
        groups = self.call("GET", "/api/v1/group/list").get("data") or []
        for g in groups:
            if g.get("name") == "DS-TEST-allslow":
                return g["id"]
        relay_config = {"member_max_attempts": 2, "member_retry_interval_seconds": 1,
                        "member_non_stream_response_timeout_seconds": 4,
                        "member_stream_first_event_timeout_seconds": 4,
                        "member_cooldown_seconds": 30, "member_affinity_seconds": 0}
        r = self.call("POST", "/api/v1/group/create",
                      {"name": "DS-TEST-allslow", "mode": "failover", "relay_config": relay_config,
                       "items": [{"channel_grant_id": slow[0]}]})
        gid = ((r.get("data") or {}).get("group") or {}).get("id")
        print("created DS-TEST-allslow id=", gid)
        return gid


def mock_log_len():
    if not os.path.exists(MOCK_LOG):
        return 0
    with open(MOCK_LOG, encoding="utf-8") as fh:
        return sum(1 for _ in fh)


def mock_since(n0):
    entries = []
    if not os.path.exists(MOCK_LOG):
        return entries
    with open(MOCK_LOG, encoding="utf-8") as fh:
        for i, line in enumerate(fh):
            if i < n0:
                continue
            line = line.strip()
            if line:
                try:
                    entries.append(json.loads(line))
                except Exception:
                    pass
    return entries


def call_relay(path, payload, api_key, stream=False, timeout=120):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + api_key}
    t0 = time.time()
    conn.request("POST", path, body=json.dumps(payload), headers=headers)
    resp = conn.getresponse()
    status = resp.status
    chunks = []
    if stream:
        while True:
            line = resp.readline()
            if not line:
                break
            chunks.append(line)
            if len(chunks) > 4000:
                break
    else:
        chunks.append(resp.read())
    elapsed = time.time() - t0
    conn.close()
    return {"status": status, "elapsed": elapsed, "raw": b"".join(chunks).decode("utf-8", "replace")}


def main():
    key, key_name, key_id = relay_api_key()
    admin = Admin()
    admin.ensure_allslow()
    relay_key_note = f"key id={key_id}"
    print("relay " + relay_key_note)

    # ---------- A. non-stream failover on upstream 500 ----------
    s0 = admin.stats()
    n0 = mock_log_len()
    res = call_relay("/v1/chat/completions",
                     {"model": "DS-TEST-failover", "messages": [{"role": "user", "content": "hi"}], "stream": False}, key)
    hits = mock_since(n0)
    s1 = admin.stats()
    models = [h["model"] for h in hits if h["method"] == "POST"]
    ok = res["status"] == 200 and "mock chat ok" in res["raw"] and models.count("mock-bad") >= 1 and models.count("mock-good") == 1
    # 跨轮次残留的成员冷却会让超时/切换用例假失败：先把本套件用到的分组冷却压到 3s 并等过它。
    admin.reset_cooldowns()
    admin.wait_cooldown(4)
    record("A 500-failover switches to healthy member",
           ok, f"HTTP {res['status']} {res['elapsed']:.2f}s upstream_hits={models} "
               f"success_delta={(s1.get('request_success', 0) - s0.get('request_success', 0))} "
               f"failed_delta={(s1.get('request_failed', 0) - s0.get('request_failed', 0))}")

    # ---------- B. cooldown: second call skips the cooled member ----------
    s0 = admin.stats()
    n0 = mock_log_len()
    res2 = call_relay("/v1/chat/completions",
                      {"model": "DS-TEST-failover", "messages": [{"role": "user", "content": "hi"}], "stream": False}, key)
    hits2 = mock_since(n0)
    models2 = [h["model"] for h in hits2 if h["method"] == "POST"]
    ok = res2["status"] == 200 and models2 == ["mock-good"] and res2["elapsed"] < 2.0
    record("B cooldown skips failed member on next call",
           ok, f"HTTP {res2['status']} {res2['elapsed']:.2f}s upstream_hits={models2}")

    # ---------- C. non-stream timeout switchover ----------
    s0 = admin.stats()
    n0 = mock_log_len()
    res3 = call_relay("/v1/chat/completions",
                      {"model": "DS-TEST-timeout", "messages": [{"role": "user", "content": "hi"}], "stream": False},
                      key, timeout=180)
    hits3 = mock_since(n0)
    models3 = [h["model"] for h in hits3 if h["method"] == "POST"]
    s1 = admin.stats()
    ok = (res3["status"] == 200 and "mock chat ok" in res3["raw"]
          and models3.count("mock-slow") >= 1 and models3.count("mock-good") == 1
          and 4 <= res3["elapsed"] <= 14)
    record("C non-stream timeout switchover (member hangs, no response)",
           ok, f"HTTP {res3['status']} {res3['elapsed']:.2f}s upstream_hits={models3} "
               f"(slow upstream sleeps 15s; per-member timeout 4s x2 attempts)")
    if not ok:
        print("    raw:", res3["raw"][:300])

    # ---------- D. stream first-event timeout switchover (dedicated slow member, so the
    # non-stream cooldown above cannot mask the stream path) ----------
    admin.ensure_streamtimeout()
    # 本组冷却 3s：先等过上一轮可能残留的冷却，保证"第一次尝试必撞慢成员"这个前提成立。
    admin.wait_cooldown()
    s0 = admin.stats()
    n0 = mock_log_len()
    res4 = call_relay("/v1/chat/completions",
                      {"model": "DS-TEST-streamtimeout",
                       "messages": [{"role": "user", "content": "hi"}], "stream": True},
                      key, stream=True, timeout=180)
    hits4 = mock_since(n0)
    models4 = [h["model"] for h in hits4 if h["method"] == "POST"]
    sse_ok = "chat.completion.chunk" in res4["raw"] and "[DONE]" in res4["raw"]
    ok = (res4["status"] == 200 and models4.count("mock-slow2") >= 1 and models4.count("mock-good") == 1
          and sse_ok and 4 <= res4["elapsed"] <= 14)
    record("D stream first-event timeout switchover",
           ok, f"HTTP {res4['status']} {res4['elapsed']:.2f}s upstream_hits={models4} sse_complete={sse_ok} "
               f"(member hangs 15s before its first event; per-member first-event timeout 4s x2)")
    if not ok:
        print("    raw:", res4["raw"][:300])

    # ---------- E. all members unavailable: relay keeps waiting for a member to recover
    # (documented design) -- the abandoned client request must still be recorded ----------
    time.sleep(31)  # let the cooled member come back so the slow member is really retried
    s0 = admin.stats()
    n0 = mock_log_len()
    client_timeout = False
    try:
        res5 = call_relay("/v1/chat/completions",
                          {"model": "DS-TEST-allslow", "messages": [{"role": "user", "content": "hi"}],
                           "stream": False}, key, timeout=25)
    except Exception as e:
        client_timeout = True
        res5 = {"status": 0, "elapsed": 25.0, "raw": type(e).__name__ + ": " + str(e)[:80]}
    hits5 = mock_since(n0)
    models5 = [h["model"] for h in hits5 if h["method"] == "POST"]
    time.sleep(3)
    s1 = admin.stats()
    failed_delta = s1.get("request_failed", 0) - s0.get("request_failed", 0)
    success_delta = s1.get("request_success", 0) - s0.get("request_success", 0)
    hist = admin.call("GET", "/api/v1/log/history?limit=10&offset=0").get("data") or {}
    rows5 = [r for r in (hist.get("items") or []) if r.get("model") == "DS-TEST-allslow"]
    ok = client_timeout and len(models5) >= 1 and len(rows5) >= 1 and success_delta == 0
    record("E all members unavailable -> waits (by design) and logs the abandoned request",
           ok, f"client_timed_out={client_timeout} upstream_attempts={len(models5)} "
               f"logged={[(r.get('status'), r.get('duration_ms')) for r in rows5]} "
               f"failed_delta={failed_delta} success_delta={success_delta}")

    failed = [n for n, ok, _ in RESULTS if not ok]
    print(f"\nFAILOVER_TESTS total={len(RESULTS)} pass={len(RESULTS) - len(failed)} fail={len(failed)}")
    if failed:
        print("FAILED:", failed)
    print("FAILOVER_TESTS_EXIT=" + ("0" if not failed else "1"))
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())

