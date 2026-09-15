"""least_busy（最空闲）活体验证（NM-DS-006）。

成员：DS-TEST-mock 渠道的 mock-slow（上游 sleep 15s，priority 1）与 mock-good（正常，priority 2）。

并发场景：
  请求 A（非流式）打 DS-TEST-busy（least_busy）→ 冷启动平手 → 按 priority 选中 mock-slow，
  并把它的"在途"顶到 1（非流式要等上游整段响应，Sending 在 15s 内一直为真）；
  1.5s 后请求 B 打同一个分组 → mock-slow 在途 1、mock-good 在途 0 → 应改选 mock-good 并很快返回。

对照组 DS-TEST-busyf（failover、同成员）：同样的并发时序下，第二个请求仍按 priority 撞 mock-slow。

Run: python run_busy_test.py
"""

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
    except Exception as e:
        return 0, {"_raw": "%s: %s" % (type(e).__name__, e)}


def relay(model, key, timeout=90):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", "/v1/chat/completions",
                 body=json.dumps({"model": model, "messages": [{"role": "user", "content": "busy?"}], "stream": False}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    t0 = time.time()
    resp = conn.getresponse()
    resp.read()
    conn.close()
    return resp.status, round(time.time() - t0, 2)


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
            except Exception:
                continue
            if entry.get("method") == "POST":
                out.append(entry.get("model"))
    return out


def api_key():
    import sqlite3
    conn = sqlite3.connect(r"file:D:\奇怪的软件\octopus\data\data.db?mode=ro", uri=True)
    cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
    col = "api_key" if "api_key" in cols else "key"
    return conn.execute(f"select {col} from api_keys where enabled = 1 order by id limit 1").fetchone()[0]


def grants():
    import sqlite3
    conn = sqlite3.connect(r"file:D:\奇怪的软件\octopus\data\data.db?mode=ro", uri=True)
    rows = conn.execute("""
        select cg.id, cm.name from channel_grants cg
        join channel_models cm on cm.id = cg.channel_model_id
        join channels ch on ch.id = cm.channel_id
        where ch.name = 'DS-TEST-mock' and cm.name in ('mock-slow','mock-good')""").fetchall()
    return {name: gid for gid, name in rows}


def ensure_group(name, mode, grant_ids):
    _, lst = call("GET", "/api/v1/group/list")
    hit = next((g for g in (lst.get("data") or []) if g.get("name") == name), None)
    body = {"mode": mode, "items": [{"channel_grant_id": g} for g in grant_ids]}
    st, res = (call("POST", f"/api/v1/group/update/{hit['id']}", body) if hit
               else call("POST", "/api/v1/group/create", dict({"name": name}, **body)))
    return st, (res.get("data") or {}).get("mode")


def concurrent_pair(group, key, gap=1.5):
    """打出并发的一对请求：A 先冲、gap 秒后 B。返回 (A状态,A耗时,B状态,B耗时)。"""
    out = {}

    def run_a():
        out["a"] = relay(group, key)
    t = threading.Thread(target=run_a)
    t.start()
    time.sleep(gap)
    b = relay(group, key)
    t.join()
    return out["a"][0], out["a"][1], b[0], b[1]


def main():
    key = api_key()
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    g = grants()
    if "mock-slow" not in g or "mock-good" not in g:
        print("grants missing:", g)
        return 1
    slow, good = g["mock-slow"], g["mock-good"]

    st, mode = ensure_group("DS-TEST-busy", "least_busy", [slow, good])
    record("DS-TEST-busy: mode=least_busy 建档", st == 200 and mode == "least_busy", f"HTTP {st} mode={mode}")
    ensure_group("DS-TEST-busyf", "failover", [slow, good])

    # ---- 并发：A 压住 mock-slow（15s），1.5s 后的 B 应改选空闲的 mock-good ----
    base = log_len()
    a_st, a_dt, b_st, b_dt = concurrent_pair("DS-TEST-busy", key)
    hit = log_models(base)
    record("并发第 1 个请求（冷启动、无人占用）→ 按 priority 选中 mock-slow 并占住它",
           a_st == 200 and "mock-slow" in hit and a_dt > 10,
           f"A HTTP {a_st} 耗时 {a_dt}s（应 ≈15s）；mock 收到={hit}")
    record("并发第 2 个请求 → mock-slow 在途 1、mock-good 空闲 → 改选 mock-good 且很快返回",
           b_st == 200 and b_dt < 5 and hit.count("mock-slow") == 1,
           f"B HTTP {b_st} 耗时 {b_dt}s（应 <5s）；mock 收到={hit}（mock-slow 只应出现 1 次）")

    # ---- 对照：同并发时序下 failover 仍先撞 mock-slow ----
    base2 = log_len()
    c_st, c_dt, d_st, d_dt = concurrent_pair("DS-TEST-busyf", key)
    hit2 = log_models(base2)
    record("对照 failover 组：同样并发时序下第二个请求仍撞 mock-slow（证明差异来自模式）",
           c_st == 200 and d_st == 200 and hit2.count("mock-slow") >= 2,
           f"C HTTP {c_st} 耗时 {c_dt}s / D HTTP {d_st} 耗时 {d_dt}s；mock 收到={hit2}")

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("BUSY_TEST total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for n, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", n)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
