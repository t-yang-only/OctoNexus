"""Key-level backoffice audit for the local octopus instance (NM-DS-002 continuation).

Covers the surfaces the earlier suite did not touch:
  T1  fresh key relays normally
  T2  /api/v1/apikey/stats and /api/v1/stats/apikey record the key's traffic
  T3  RPM limit -> 429 + Retry-After
  T4  TPM limit -> 429 + Retry-After
  T5  expired key -> 401
  T6  disabled key -> 401
  T7  max_cost reached -> 401
  T8  unknown key -> 401
  T9  supported_models scope
  T10 live overview SSE (/api/v1/log/overview/stream) delivers request events
  T11 manual round stop (/api/v1/log/{request_id}/{round}/stop) aborts an in-flight round

Creates disposable keys named DS-TEST-* and deletes them at the end. Never prints key material.

Run: python run_apikey_audit.py
"""

import atexit
import http.client
import json
import os
import sys
import threading
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")
RESULTS = []
CREATED = []


def mock_log_len():
    if not os.path.exists(MOCK_LOG):
        return 0
    with open(MOCK_LOG, encoding="utf-8") as fh:
        return sum(1 for _ in fh)


def mock_since(n0):
    hits = []
    if not os.path.exists(MOCK_LOG):
        return hits
    with open(MOCK_LOG, encoding="utf-8") as fh:
        for i, line in enumerate(fh):
            if i < n0:
                continue
            line = line.strip()
            if line:
                try:
                    hits.append(json.loads(line))
                except Exception:
                    pass
    return hits


def record(name, ok, detail):
    RESULTS.append((name, ok, detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


class Admin:
    def __init__(self):
        self.cookie = None
        self.call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    def call(self, method, path, payload=None, timeout=60):
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(ADMIN + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if self.cookie:
            req.add_header("Cookie", self.cookie)
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    self.cookie = v.split(";")[0]
            body = resp.read().decode()
            return json.loads(body) if body else {}

    def get(self, path):
        return self.call("GET", path).get("data")

    def new_key(self, name, **overrides):
        req = {"name": name, "enabled": True, "expire_at": 0, "max_cost": 0, "rpm": 0, "tpm": 0,
               "supported_models": []}
        req.update(overrides)
        r = self.call("POST", "/api/v1/apikey/create", req)
        key = r.get("data") or {}
        CREATED.append(key.get("id"))
        return key

    def update_key(self, key, **fields):
        payload = dict(key)
        payload.update(fields)
        return self.call("POST", "/api/v1/apikey/update", payload)

    def call_status(self, method, path, payload=None, timeout=60):
        """同 call(), 但返回 (状态码, 解析后的 body) —— 204 这类无 body 的响应也要拿到状态码。"""
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(ADMIN + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if self.cookie:
            req.add_header("Cookie", self.cookie)
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                body = resp.read().decode()
                return resp.status, (json.loads(body) if body else {})
        except urllib.error.HTTPError as e:
            return e.code, {"_raw": e.read().decode()[:200]}

    def drop_keys(self):
        for kid in list(CREATED):
            if kid:
                try:
                    self.call("DELETE", f"/api/v1/apikey/delete/{kid}")
                except Exception as e:
                    print(f"  cleanup warning: key {kid}: {e}")
        CREATED.clear()  # 清空后 atexit 不会再删一遍(重复删除只会拿到 "API key not found" 500)


def key_self_call(path, api_key, timeout=30):
    """/api/v1/apikey/stats 与 /api/v1/apikey/login 挂在 Admin 端口但用 APIKeyAuth() 鉴权: 用 key 本身调。"""
    req = urllib.request.Request(ADMIN + path)
    req.add_header("x-api-key", api_key)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            body = resp.read().decode()
            return resp.status, (json.loads(body) if body else {}), resp.getheader("Set-Cookie")
    except urllib.error.HTTPError as e:
        return e.code, {}, None


def relay(path, payload, api_key, stream=False, timeout=60):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + api_key}
    t0 = time.time()
    try:
        conn.request("POST", path, body=json.dumps(payload), headers=headers)
        resp = conn.getresponse()
        raw = resp.read().decode("utf-8", "replace") if not stream else resp.read().decode("utf-8", "replace")
        return {"status": resp.status, "raw": raw, "retry_after": resp.getheader("Retry-After"),
                "elapsed": time.time() - t0}
    finally:
        conn.close()


def sse_overview(events, stop):
    """读实时概览 SSE, 把 log 事件塞进 events 列表。"""
    req = urllib.request.Request(ADMIN + "/api/v1/log/overview/stream")
    req.add_header("Cookie", ADMIN_COOKIE[0])
    req.add_header("Accept", "text/event-stream")
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            for raw in resp:
                if stop.is_set():
                    break
                line = raw.decode("utf-8", "replace").strip()
                if line.startswith("data:"):
                    try:
                        events.append(json.loads(line[5:].strip()))
                    except Exception:
                        pass
    except Exception as e:
        events.append({"_error": str(e)})


ADMIN_COOKIE = [""]


def main():
    admin = Admin()
    atexit.register(admin.drop_keys)  # 任何退出路径(含异常)都清理本次创建的测试 Key
    ADMIN_COOKIE[0] = admin.cookie or ""

    # ---------- T1 fresh key ----------
    k_main = admin.new_key("DS-TEST-key")
    if not k_main.get("api_key"):
        record("T1 fresh key can relay", False, f"create returned no key: {json.dumps(k_main)[:120]}")
        admin.drop_keys()
        return 1
    good = relay("/v1/chat/completions",
                 {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}], "stream": False},
                 k_main["api_key"])
    record("T1 fresh key can relay", good["status"] == 200, f"HTTP {good['status']} key_id={k_main['id']}")

    # ---------- T2 per-key stats ----------
    time.sleep(2)
    st_self, self_body, _ = key_self_call("/api/v1/apikey/stats", k_main["api_key"])
    env = (self_body or {}).get("data") or {}
    self_stats = env.get("stats") or {}
    self_info = env.get("info") or {}
    total_key = next((r for r in (admin.get("/api/v1/stats/apikey") or []) if r.get("api_key_id") == k_main["id"]), None)
    ok = (st_self == 200 and self_info.get("id") == k_main["id"]
          and (self_stats.get("request_success") or 0) >= 1 and total_key
          and (total_key.get("request_success") or 0) == (self_stats.get("request_success") or 0))
    record("T2 per-key stats agree between key self-service and admin view",
           bool(ok), f"key_id={k_main['id']} self(stats={self_stats and (self_stats.get('request_success'), self_stats.get('input_token'))}) "
                     f"admin={total_key and (total_key.get('request_success'), total_key.get('input_token'))} HTTP {st_self}")
    st_login, login_body, login_cookie = key_self_call("/api/v1/apikey/login", k_main["api_key"])
    record("T12 key login endpoint accepts the key",
           st_login == 200, f"HTTP {st_login} set_cookie={'yes' if login_cookie else 'no'} body={json.dumps(login_body, ensure_ascii=False)[:80]}")

    # ---------- T3 RPM ----------
    k_rpm = admin.new_key("DS-TEST-rpm", rpm=2)
    codes = []
    for _ in range(4):
        r = relay("/v1/chat/completions",
                  {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}], "stream": False},
                  k_rpm["api_key"])
        codes.append((r["status"], r["retry_after"]))
    record("T3 key RPM limit returns 429 + Retry-After",
           codes[:2] == [(200, None), (200, None)] and all(c[0] == 429 and c[1] == "60" for c in codes[2:]),
           f"codes={codes}")

    # ---------- T4 TPM ----------
    admin.update_key(k_rpm, tpm=1)
    tpm_hit = relay("/v1/chat/completions",
                    {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}], "stream": False},
                    k_rpm["api_key"])
    record("T4 key TPM limit returns 429 + Retry-After",
           tpm_hit["status"] == 429 and tpm_hit["retry_after"] == "60",
           f"HTTP {tpm_hit['status']} retry_after={tpm_hit['retry_after']}")

    # ---------- T5 expired ----------
    k_exp = admin.new_key("DS-TEST-exp", expire_at=int(time.time()) - 3600)
    r = relay("/v1/chat/completions", {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}]},
              k_exp["api_key"])
    record("T5 expired key -> 401", r["status"] == 401 and "expired" in r["raw"], f"HTTP {r['status']} {r['raw'][:90]}")

    # ---------- T6 disabled ----------
    k_off = admin.new_key("DS-TEST-off", enabled=False)
    listed = next((k for k in (admin.get("/api/v1/apikey/list") or []) if k.get("id") == k_off.get("id")), {})
    r = relay("/v1/chat/completions", {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}]},
              k_off["api_key"])
    record("T6 disabled key -> 401 (enabled:false is honoured at create time)",
           k_off.get("enabled") is False and listed.get("enabled") is False and r["status"] == 401 and "disabled" in r["raw"],
           f"created.enabled={k_off.get('enabled')} persisted={listed.get('enabled')} "
           f"relay=HTTP {r['status']} {r['raw'][:80]}")

    # ---------- T7 max cost ----------
    k_cost = admin.new_key("DS-TEST-cost")
    real = relay("/v1/chat/completions",
                 {"model": "deepseek-v4-flash", "messages": [{"role": "user", "content": "只回答两个字：你好"}],
                  "stream": False, "max_tokens": 64}, k_cost["api_key"], timeout=180)
    admin.update_key(k_cost, max_cost=0.0000001)
    r = relay("/v1/chat/completions", {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}]},
              k_cost["api_key"])
    record("T7 key over max_cost -> 401",
           real["status"] == 200 and r["status"] == 401 and "max cost" in r["raw"],
           f"real_call={real['status']} then HTTP {r['status']} {r['raw'][:90]}")

    # ---------- T8 unknown key ----------
    r = relay("/v1/chat/completions", {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}]},
              "sk-definitely-not-a-real-key")
    record("T8 unknown key -> 401", r["status"] == 401, f"HTTP {r['status']}")

    # ---------- T9 supported_models scope ----------
    k_scope = admin.new_key("DS-TEST-scope", supported_models=["DS-TEST-formats"])
    in_scope = relay("/v1/chat/completions",
                     {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}]}, k_scope["api_key"])
    out_scope = relay("/v1/chat/completions",
                      {"model": "DS-TEST-convert", "messages": [{"role": "user", "content": "hi"}]}, k_scope["api_key"])
    record("T9 supported_models scope enforced",
           in_scope["status"] == 200 and out_scope["status"] >= 400,
           f"in_scope=HTTP {in_scope['status']} out_scope=HTTP {out_scope['status']} {out_scope['raw'][:80]}")

    # ---------- T10 live overview SSE ----------
    events, stop = [], threading.Event()
    t = threading.Thread(target=sse_overview, args=(events, stop), daemon=True)
    t.start()
    time.sleep(1.5)
    relay("/v1/chat/completions", {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}],
                                   "stream": False}, k_main["api_key"])
    time.sleep(2.5)
    stop.set()
    t.join(timeout=5)
    live = [e for e in events if isinstance(e, dict) and "id" in e and "DS-TEST" not in str(e.get("_error"))]
    record("T10 live overview SSE delivers request state events",
           len(live) >= 1 and all("status" in e for e in live),
           f"events={len(events)} sample={[(e.get('id'), e.get('status'), e.get('model')) for e in live[:3]]}")

    # ---------- T11 manual round stop ----------
    # 语义: 该接口中止的是"当前这一轮上游调用", 请求本身会按配置继续下一轮/等待(handler 里
    # 人工中止不进冷却、不计失败, 直接 continue), 所以断言点是"该轮被提前结束"而不是"请求返回"。
    events2, stop2 = [], threading.Event()
    t2 = threading.Thread(target=sse_overview, args=(events2, stop2), daemon=True)
    t2.start()
    time.sleep(1.5)
    hung = {}

    def fire_hung():
        hung["r"] = relay("/v1/chat/completions",
                          {"model": "DS-TEST-allslow", "messages": [{"role": "user", "content": "hi"}],
                           "stream": False}, k_main["api_key"], timeout=12)

    th = threading.Thread(target=fire_hung, daemon=True)
    th.start()
    target = None
    deadline = time.time() + 20
    while time.time() < deadline and not target:
        time.sleep(0.5)
        for e in list(events2):
            if e.get("model") == "DS-TEST-allslow" and e.get("id"):
                target = e
    if target:
        rid, rnd = target.get("id"), max(1, target.get("round") or 1)
        n_before = mock_log_len()
        st_stop, stop_body = admin.call_status("POST", f"/api/v1/log/{rid}/{rnd}/stop")
        time.sleep(2.5)
        new_hits = len(mock_since(n_before))
        rounds_seen = [e.get("round") for e in events2 if e.get("id") == rid]
        round_advanced = any((r or 0) > rnd for r in rounds_seen)
        record("T11 manual round stop aborts the in-flight round (request keeps going by design)",
               st_stop == 204 and (new_hits >= 1 or round_advanced),
               f"stop=HTTP {st_stop} request_id={rid} round={rnd} new_upstream_attempts_within_2.5s={new_hits} "
               f"rounds_seen={rounds_seen[-4:]} stop_body={json.dumps(stop_body, ensure_ascii=False)[:60]}")
    else:
        record("T11 manual round stop aborts the in-flight round (request keeps going by design)", False,
               f"no live overview event for the hung request (events={len(events2)})")
    stop2.set()
    t2.join(timeout=5)

    admin.drop_keys()
    print(f"created/deleted test keys: {len(CREATED)}")
    failed = [n for n, ok, _ in RESULTS if not ok]
    print(f"\nAPIKEY_AUDIT total={len(RESULTS)} pass={len(RESULTS) - len(failed)} fail={len(failed)}")
    if failed:
        print("FAILED:", failed)
    print("APIKEY_AUDIT_EXIT=" + ("0" if not failed else "1"))
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
