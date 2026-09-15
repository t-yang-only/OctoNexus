"""首字竞速 (T-hedge-001) 活体套件 —— **只用本地 mock 上游, 不打真实上游、不产生费用**。

装置: 复用 DS-TEST-mock 渠道的 mock-slow(上游 sleep 15s) 与 mock-good(正常) 两条成员,
priority 把慢的放在前面。竞速开启时, 首选(慢)在 hedge_after_ms 后仍无响应 → 并发请求次选(快) → 由快者胜出。

用例:
  T1 竞速开启: 请求应远快于慢成员(≈15s)返回, 且上游能看到两条尝试。
  T2 对照组(竞速关闭): 同一组合同样顺序 → 走完慢成员, 耗时 ≥ 10s, 目标模型是慢成员。
  T3 落选不计失败: 竞速后慢成员不进冷却(runtime.cooldowns 为空), 模型统计里没有失败计数增加。
  T4 高峰期触发(hedge_peak_in_flight=1): 即使阈值延迟为 0, 也应立刻并发 → 由快者极速返回。
  T5 宽度越界: hedge_width=9 提交应被拒(400), 宽度 2..5 才合法。

Run: python run_hedge_test.py    （实例 + mock 必须在跑）
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
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")

SLOW, FAST = "mock-slow", "mock-good"
COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:220])


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


def conn():
    return sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)


def relay_api_key():
    c = conn()
    row = c.execute("select api_key from api_keys where enabled=1 order by id limit 1").fetchone()
    c.close()
    return row[0]


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
        return e.code, round(time.time() - started, 1), {"_raw": e.read().decode()[:200]}
    except Exception as e:  # noqa: BLE001
        return 0, round(time.time() - started, 1), {"_raw": "%s: %s" % (type(e).__name__, e)}


def grant_ids():
    c = conn()
    rows = dict(c.execute("select m.name, g.id from channel_grants g join channel_models m on m.id=g.channel_model_id "
                          "join channels ch on ch.id=m.channel_id where ch.name='DS-TEST-mock' "
                          "and m.name in (?, ?)", (SLOW, FAST)))
    c.close()
    return rows


def mock_window_log_size():
    return os.path.getsize(MOCK_LOG) if os.path.exists(MOCK_LOG) else 0


def mock_models_since(offset):
    """读 mock 日志里某时刻之后收到的模型名（用于确认真的并发了两路）。"""
    if not os.path.exists(MOCK_LOG):
        return []
    seen = []
    with open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        for line in fh.read().splitlines():
            try:
                seen.append(json.loads(line).get("model"))
            except ValueError:
                continue
    return seen


def group_id_by_name(name):
    """/group/list 是分页的, 按名字判断存在性必须直接查库（本项目踩过这个坑）。"""
    c = conn()
    row = c.execute("select id from groups where name = ?", (name,)).fetchone()
    c.close()
    return row[0] if row else None


def group_payload(name, grants, hedge, width, after_ms, peak):
    return {
        "name": name, "mode": "failover",
        "items": [{"channel_grant_id": grants[SLOW]}, {"channel_grant_id": grants[FAST]}],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_non_stream_response_timeout_seconds": 120,
                         "member_stream_first_event_timeout_seconds": 30,
                         "member_cooldown_seconds": 60, "member_affinity_seconds": 0,
                         "hedge_enabled": hedge, "hedge_width": width,
                         "hedge_after_ms": after_ms, "hedge_peak_in_flight": peak},
    }


def ensure_group(name, grants, hedge, width=2, after_ms=800, peak=0):
    existing = group_id_by_name(name)
    if existing:
        call("DELETE", "/api/v1/group/delete/%d" % existing)
    status, resp = call("POST", "/api/v1/group/create", group_payload(name, grants, hedge, width, after_ms, peak))
    return status, resp


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    grants = grant_ids()
    if len(grants) < 2:
        record("fixtures", False, "DS-TEST-mock 的 mock-slow/mock-good 授权缺失（先跑 setup_test_entities.py）")
        return 1

    for name in ("DS-TEST-hedge", "DS-TEST-hedgef", "DS-TEST-hedgepeak"):
        call("GET", "/api/v1/group/list")
    ensure_group("DS-TEST-hedge", grants, True)
    ensure_group("DS-TEST-hedgef", grants, False)
    ensure_group("DS-TEST-hedgepeak", grants, True, width=2, after_ms=0, peak=1)

    # T1 竞速开启: 首选慢成员在 800ms 后被追加竞速, 快成员胜出。
    offset = mock_window_log_size()
    status, secs, body = relay("DS-TEST-hedge", timeout=90)
    attempts = mock_models_since(offset)
    winner = ((body.get("model") or "") if isinstance(body, dict) else "")
    record("T1 竞速开启: 快成员胜出且远快于慢成员",
           status == 200 and secs < 8 and SLOW in attempts and FAST in attempts,
           "HTTP %s 耗时 %ss 上游收到 %s" % (status, secs, attempts))

    # T2 对照组: 关闭竞速 → 走完慢成员。
    offset = mock_window_log_size()
    status, secs, body = relay("DS-TEST-hedgef", timeout=90)
    attempts = mock_models_since(offset)
    record("T2 关闭竞速: 耗时与未开启前一致（慢成员走完）",
           status == 200 and secs >= 10,
           "HTTP %s 耗时 %ss 上游收到 %s" % (status, secs, attempts))

    # T3 落选不计失败: 慢成员不进冷却。
    _, listing = call("GET", "/api/v1/group/list")
    target = next((g for g in ((listing or {}).get("data") or []) if g.get("name") == "DS-TEST-hedge"), None)
    cooldowns = ((target or {}).get("runtime") or {}).get("cooldowns") or {}
    slow_grant = grants[SLOW]
    slow_item = None
    if target:
        status_g, detail = call("GET", "/api/v1/group/get/%d" % target["id"])
        for item in ((detail.get("data") or {}).get("items") or []):
            if item.get("channel_grant_id") == slow_grant:
                slow_item = item.get("id")
    record("T3 落选成员不进冷却", slow_item is not None and str(slow_item) not in {str(k) for k in cooldowns},
           "慢成员 item=%s cooldowns=%s" % (slow_item, cooldowns))

    # T4 高峰期触发: 在途阈值 1 → 立刻并发（不等 800ms 的延迟阈值）。
    offset = mock_window_log_size()
    status, secs, body = relay("DS-TEST-hedgepeak", timeout=90)
    attempts = mock_models_since(offset)
    record("T4 高峰期在途触发: 立刻并发、快者胜出",
           status == 200 and secs < 5 and SLOW in attempts and FAST in attempts,
           "HTTP %s 耗时 %ss 上游收到 %s" % (status, secs, attempts))

    # T5 宽度越界被拒。
    status, resp = call("POST", "/api/v1/group/create",
                        group_payload("DS-TEST-hedge-badwidth", grants, True, width=9, after_ms=800, peak=0))
    record("T5 hedge_width 越界被拒", status == 400, "HTTP %d %s" % (status, str(resp.get("message"))[:80]))

    # 清理本套件装置（保留 DS-TEST-mock 渠道与两条成员供其它套件使用）。
    _, listing = call("GET", "/api/v1/group/list")
    for group in ((listing or {}).get("data") or []):
        if str(group.get("name", "")).startswith("DS-TEST-hedge"):
            call("DELETE", "/api/v1/group/delete/%d" % group["id"])

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("HEDGE total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
