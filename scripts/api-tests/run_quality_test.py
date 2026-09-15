"""按质量自动切换的活体验证（NM-DS-004 迭代）。

成员：DS-TEST-mock 渠道的 mock-bad（上游返回 500，priority 1）与 mock-good（正常，priority 2）。
分组 DS-TEST-quality 用 quality_first：

  第一次请求（冷启动、双方无样本）→ 按 priority 先试 mock-bad → 失败重试 → 冷却让位 → mock-good 成功；
  第二次请求（mock-bad 已有 0% 成功率、mock-good 100%）→ 质量定序直接把请求交给 mock-good，
  且 mock 上游日志里**没有**新的 mock-bad 尝试 —— 这就是"按质量自动切换"。

对比组 DS-TEST-qualityf（同成员、failover 模式）在第二次请求后仍旧按 priority 先撞 mock-bad。

Run: python run_quality_test.py
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


def relay(model, key, timeout=180):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", "/v1/chat/completions",
                 body=json.dumps({"model": model, "messages": [{"role": "user", "content": "quality?"}], "stream": False}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    t0 = time.time()
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw, round(time.time() - t0, 2)


def log_lines():
    if not os.path.exists(MOCK_LOG):
        return []
    with open(MOCK_LOG, "r", encoding="utf-8", errors="ignore") as fh:
        return fh.readlines()


def log_targets(lines):
    out = []
    for line in lines:
        try:
            out.append(json.loads(line).get("model"))
        except Exception:
            pass
    return out


def last_log(model_name, tries=10):
    for _ in range(tries):
        _, hist = call("GET", "/api/v1/log/history?limit=20&offset=0")
        for row in ((hist.get("data") or {}).get("items") or []):
            if row.get("model") == model_name:
                return row
        time.sleep(0.5)
    return {}


def api_key():
    import sqlite3
    conn = sqlite3.connect(r"file:D:\奇怪的软件\octopus\data\data.db?mode=ro", uri=True)
    cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
    col = "api_key" if "api_key" in cols else "key"
    return conn.execute(f"select {col} from api_keys where enabled = 1 order by id limit 1").fetchone()[0]


def grants_for_mock():
    import sqlite3
    conn = sqlite3.connect(r"file:D:\奇怪的软件\octopus\data\data.db?mode=ro", uri=True)
    rows = conn.execute("""
        select cg.id, cm.name, cg.protocols
        from channel_grants cg
        join channel_models cm on cm.id = cg.channel_model_id
        join channels ch on ch.id = cm.channel_id
        where ch.name = 'DS-TEST-mock' and cm.name in ('mock-bad', 'mock-good')
        order by cm.name""").fetchall()
    return {name: gid for gid, name, _ in rows}


def ensure_group(name, mode, grant_ids):
    _, lst = call("GET", "/api/v1/group/list")
    hit = next((g for g in (lst.get("data") or []) if g.get("name") == name), None)
    items = [{"channel_grant_id": gid} for gid in grant_ids]
    if hit:
        return call("POST", f"/api/v1/group/update/{hit['id']}", {"mode": mode, "items": items})
    return call("POST", "/api/v1/group/create", {"name": name, "mode": mode, "items": items})


def main():
    key = api_key()
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    grants = grants_for_mock()
    if "mock-bad" not in grants or "mock-good" not in grants:
        print("mock grants missing:", grants)
        return 1
    bad, good = grants["mock-bad"], grants["mock-good"]
    print("mock grants: bad=%d good=%d" % (bad, good))

    # 质量优先组 + 对照 failover 组（同成员、同优先级顺序）
    st, res = ensure_group("DS-TEST-quality", "quality_first", [bad, good])
    ok = st == 200 and (res.get("data") or {}).get("mode") == "quality_first"
    record("DS-TEST-quality: mode=quality_first 建档", ok,
           f"HTTP {st} mode={(res.get('data') or {}).get('mode')}")
    ensure_group("DS-TEST-qualityf", "failover", [bad, good])

    # ---- 第一次请求：冷启动按 priority 先撞 mock-bad；实例已有样本（暖态）时质量定序会直接跳过它 ----
    # 成员样本窗口 300s 且随进程存活，同一实例连续跑两次时第一次请求就可能已是暖态：
    # 两种都算符合预期，真正的判据是"最终由 mock-good 成功、且不反复撞已知坏成员"（见第 2 次请求）。
    base = len(log_lines())
    st1, raw1, dt1 = relay("DS-TEST-quality", key)
    row1 = last_log("DS-TEST-quality")
    touched = log_targets(log_lines()[base:])
    cold = "mock-bad" in touched
    record("第 1 次请求：最终由 mock-good 成功（冷启动会先探测 mock-bad，暖态直接跳过，均属预期）",
           st1 == 200 and row1.get("target_model") == "mock-good",
           f"HTTP {st1} in {dt1}s target={row1.get('target_model')} status={row1.get('status')} "
           f"mock尝试={touched} 观测态={'冷启动' if cold else '暖态'}")

    # ---- 第二次请求：质量定序直接交给 mock-good，不再撞 mock-bad ----
    base2 = len(log_lines())
    st2, raw2, dt2 = relay("DS-TEST-quality", key)
    touched2 = log_targets(log_lines()[base2:])
    row2 = last_log("DS-TEST-quality")
    record("第 2 次请求：质量定序直接选中 mock-good（mock-bad 已 0% 成功率）",
           st2 == 200 and row2.get("target_model") == "mock-good" and "mock-bad" not in touched2,
           f"HTTP {st2} in {dt2}s target={row2.get('target_model')} mock尝试={touched2} "
           f"(第 1 次耗时 {dt1}s → 第 2 次 {dt2}s)")

    # ---- 对照组：同成员 failover 仍先撞 mock-bad ----
    base3 = len(log_lines())
    st3, raw3, dt3 = relay("DS-TEST-qualityf", key)
    touched3 = log_targets(log_lines()[base3:])
    row3 = last_log("DS-TEST-qualityf")
    record("对照 failover 组：同成员同优先级顺序下仍先撞 mock-bad（证明差异来自模式）",
           st3 == 200 and "mock-bad" in touched3,
           f"HTTP {st3} in {dt3}s target={row3.get('target_model')} mock尝试={touched3}")

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("QUALITY_TEST total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for n, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", n)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
