"""Live A/B for R-route-001 iteration 1 (GroupMode lowest_cost).

Two groups over the SAME two members of channel 53HK-L, with the PRICIER member first
(= priority 1 in both groups, so the legacy failover path prefers it):

  * DS-TEST-costmode    mode=lowest_cost -> must pick glm-5.3-flash        (unit 0.325)
  * DS-TEST-costfailover mode=failover   -> must pick deepseek-v4-flash-0731 (unit 0.75)

Run: python run_costmode_test.py
"""

import os
import http.client
import json
import sys
import time
import urllib.error
import urllib.request

# 门禁: 这个套件会打真实上游、会产生费用, 必须显式开启才跑（默认跳过, 见 scripts/api-tests/README.md）。
if os.environ.get("OCTOPUS_ALLOW_REAL") != "1":
    print("SKIPPED: this suite calls real upstreams and costs money; set OCTOPUS_ALLOW_REAL=1 "
          "(or run run_all.py --with-real) to run it.")
    raise SystemExit(0)

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
API_KEY = "DS-TEST"  # placeholder, real key is read from the DB below
GRANT_PRICIER = 24  # 53HK-L / deepseek-v4-flash-0731, unit 0.75
GRANT_CHEAP = 47    # 53HK-L / glm-5.3-flash,         unit 0.325
RESULTS = []
COOKIE = {}
CREATED = []


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
                 body=json.dumps({"model": model, "messages": [{"role": "user", "content": "cost order?"}],
                                  "stream": False, "max_tokens": 16}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    t0 = time.time()
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw, time.time() - t0


def newest_log(model_name, tries=8, interval=0.5):
    """日志行在响应之后落库, 故这里轮询等它出现, 避免把"还没写"误判成"没路由"。"""
    for _ in range(tries):
        _, hist = call("GET", "/api/v1/log/history?limit=20&offset=0")
        items = ((hist or {}).get("data") or {}).get("items") or []
        for row in items:
            if row.get("model") == model_name:
                return row
        time.sleep(interval)
    return None


def main():
    # API key: take the user's first key straight from the DB (never printed).
    import sqlite3
    conn = sqlite3.connect(r"file:D:\奇怪的软件\octopus\data\data.db?mode=ro", uri=True)
    cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
    key_col = "api_key" if "api_key" in cols else "key"
    key = conn.execute(f"select {key_col} from api_keys where enabled = 1 order by id limit 1").fetchone()[0]

    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    members = [{"channel_grant_id": GRANT_PRICIER}, {"channel_grant_id": GRANT_CHEAP}]
    plan = [("DS-TEST-costmode", "lowest_cost", "glm-5.3-flash", "deepseek-v4-flash-0731"),
            ("DS-TEST-costfailover", "failover", "deepseek-v4-flash-0731", "glm-5.3-flash")]

    for name, mode, want, unwanted in plan:
        _, existing = call("GET", "/api/v1/group/list")
        rows = existing.get("data") or []
        hit = next((g for g in rows if g.get("name") == name), None)
        if hit:
            st, created = call("POST", f"/api/v1/group/update/{hit['id']}", {"mode": mode, "items": members})
        else:
            st, created = call("POST", "/api/v1/group/create", {"name": name, "mode": mode, "items": members})
            if created.get("data"):
                CREATED.append(created["data"].get("id") or name)
        record(f"{name}: mode={mode} accepted and persisted", st == 200 and (created.get("data") or {}).get("mode") == mode,
               f"HTTP {st} mode={(created.get('data') or {}).get('mode')} body={json.dumps(created, ensure_ascii=False)[:120]}")

        time.sleep(1)
        status, raw, elapsed = relay(name, key)
        row = newest_log(name) or {}
        got = row.get("target_model")
        record(f"{name}: relay routed to the {mode} winner",
               status == 200 and got == want,
               f"HTTP {status} in {elapsed:.1f}s target_model={got} (want {want}); "
               f"channel={row.get('target_channel')} cost={row.get('cost')} "
               f"tokens={row.get('prompt_tokens')}/{row.get('completion_tokens')}")
        record(f"{name}: did not fall back to the priority-first member {unwanted}",
               got != unwanted, f"target_model={got}")

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print(f"COSTMODE_TEST total={total} pass={passed} fail={total - passed}")
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
