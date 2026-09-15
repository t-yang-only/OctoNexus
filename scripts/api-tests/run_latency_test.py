"""lowest_latency（最低延迟）活体验证（NM-DS-006）。

成员：DS-TEST-mock 渠道的 mock-slow（上游 sleep 15s，priority 1）与 mock-good（正常，priority 2）。
分组 DS-TEST-latency 用 lowest_latency：

  第 1 次请求（双方都还没有耗时数据 → 都按 0 参与排序 → 平手由 priority 决定）→ 先撞 mock-slow，
  慢/超时后让位给 mock-good，本轮由此记录下 mock-slow 的"最近耗时"；
  第 2 次请求（mock-slow 已知很慢、mock-good 无数据按 0 乐观）→ 直接走 mock-good，
  mock 上游日志里**没有**新的 mock-slow 尝试，耗时从十几秒掉到亚秒级。

对照组 DS-TEST-latencyf（同成员、failover）仍旧按 priority 先撞 mock-slow。

Run: python run_latency_test.py
"""

import http.client
import json
import os
import sys
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
                 body=json.dumps({"model": model, "messages": [{"role": "user", "content": "latency?"}], "stream": False}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    t0 = time.time()
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw, round(time.time() - t0, 2)


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
    if hit:
        st, res = call("POST", f"/api/v1/group/update/{hit['id']}", body)
    else:
        st, res = call("POST", "/api/v1/group/create", dict({"name": name}, **body))
    return st, (res.get("data") or {}).get("mode")


def main():
    key = api_key()
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    g = grants()
    if "mock-slow" not in g or "mock-good" not in g:
        print("grants missing:", g)
        return 1
    slow, good = g["mock-slow"], g["mock-good"]

    st, mode = ensure_group("DS-TEST-latency", "lowest_latency", [slow, good])
    record("DS-TEST-latency: mode=lowest_latency 建档", st == 200 and mode == "lowest_latency", f"HTTP {st} mode={mode}")
    ensure_group("DS-TEST-latencyf", "failover", [slow, good])

    # 第 1 次：冷启动（都无耗时数据 → 平手 → priority 先撞 mock-slow）
    base = log_len()
    st1, _raw1, dt1 = relay("DS-TEST-latency", key)
    hit1 = log_models(base)
    record("冷启动第 1 次：先撞 mock-slow（慢成员）后由 mock-good 完成",
           st1 == 200 and "mock-slow" in hit1,
           f"HTTP {st1} 耗时 {dt1}s mock 收到={hit1}")

    # 第 2 次：mock-slow 已记录为很慢、mock-good 无数据按 0 乐观 → 直接走 mock-good
    base2 = log_len()
    st2, _raw2, dt2 = relay("DS-TEST-latency", key)
    hit2 = log_models(base2)
    record("第 2 次请求：直接走 mock-good，不再撞慢成员",
           st2 == 200 and "mock-good" in hit2 and "mock-slow" not in hit2 and dt2 < dt1,
           f"HTTP {st2} 耗时 {dt1}s → {dt2}s mock 收到={hit2}")

    # 第 3 次：两边都有耗时数据，最短者依旧当选
    base3 = log_len()
    st3, _raw3, dt3 = relay("DS-TEST-latency", key)
    hit3 = log_models(base3)
    record("第 3 次请求：按已知耗时继续选最快的成员",
           st3 == 200 and hit3 == ["mock-good"],
           f"HTTP {st3} 耗时 {dt3}s mock 收到={hit3}")

    # 对照：failover 组仍先撞慢成员
    base4 = log_len()
    st4, _raw4, dt4 = relay("DS-TEST-latencyf", key)
    hit4 = log_models(base4)
    record("对照 failover 组：同成员同优先级顺序下仍先撞 mock-slow（证明差异来自模式）",
           st4 == 200 and "mock-slow" in hit4,
           f"HTTP {st4} 耗时 {dt4}s mock 收到={hit4}")

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("LATENCY_TEST total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for n, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", n)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
