"""分组 CRUD 与选路模式白名单的活体审计（回归护栏）。

为什么要这个套件：分组模式是逐轮加出来的（lowest_cost / quality_first / lowest_latency / least_busy / lowest_tpm_rpm），
每加一个都必须同时改五处（常量 / IsValid / 三处 binding oneof / 导入校验 / 前端联合类型+下拉）。
漏掉 binding 的表现是"建档直接 400"——这个套件把"七种模式都必须被接口接受并原样读回"写成断言，
下次加模式时漏点会立刻红，而不是等用户建档失败。

同时覆盖：非法模式被拒、改名/改模式、缺名与重名、引用了不存在的授权、删除不存在的分组、未登录 401。

Run: python run_group_audit.py   （实例 + mock 上游必须在跑；会创建并清理 DS-TEST-grp-* 分组）
"""

import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))

MODES = ["manual", "failover", "lowest_cost", "quality_first", "lowest_latency", "least_busy", "lowest_tpm_rpm"]
PREFIX = "DS-TEST-grp-"

COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


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
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {"_raw": raw[:200]}
    except Exception as e:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(e).__name__, e)}


def groups():
    _, payload = call("GET", "/api/v1/group/list")
    return ((payload or {}).get("data") or []) if isinstance(payload, dict) else []


def find(name):
    return next((g for g in groups() if g.get("name") == name), None)


def grant_id():
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    row = conn.execute(
        "select g.id from channel_grants g join channel_models m on m.id = g.channel_model_id "
        "join channels c on c.id = m.channel_id where c.name = 'DS-TEST-mock' "
        "and m.name = 'mock-good' limit 1").fetchone()
    conn.close()
    return row[0] if row else None


def payload(name, mode, grant):
    return {
        "name": name,
        "mode": mode,
        "items": [{"channel_grant_id": grant}],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_non_stream_response_timeout_seconds": 120,
                         "member_stream_first_event_timeout_seconds": 30,
                         "member_cooldown_seconds": 5, "member_affinity_seconds": 0},
    }


def cleanup():
    for group in groups():
        if str(group.get("name", "")).startswith(PREFIX):
            call("DELETE", "/api/v1/group/delete/%d" % group["id"])


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    grant = grant_id()
    if not grant:
        record("fixture grant exists", False, "DS-TEST-mock/mock-good grant missing (run setup_test_entities.py)")
        return 1
    cleanup()

    # 1) 七种模式都必须被接口接受, 且读回时原样保留。
    accepted, readback = [], []
    for mode in MODES:
        name = PREFIX + mode
        status, body = call("POST", "/api/v1/group/create", payload(name, mode, grant))
        accepted.append((mode, status))
        if status == 200:
            hit = find(name)
            readback.append((mode, (hit or {}).get("mode")))
    missing = [mode for mode, status in accepted if status != 200]
    record("every routing mode is accepted by group/create", not missing,
           "rejected=%s statuses=%s" % (missing, accepted))
    wrong = [(mode, seen) for mode, seen in readback if seen != mode]
    record("the stored mode is read back unchanged", not wrong and len(readback) == len(MODES),
           "readback=%s mismatches=%s" % (readback, wrong))

    # 2) 非法模式必须被拒（binding oneof 与 IsValid 双保险）。
    status, _ = call("POST", "/api/v1/group/create", payload(PREFIX + "bogus", "bogus_mode", grant))
    record("an unknown mode is rejected", status == 400, "HTTP %d for mode=bogus_mode" % status)

    # 3) 改模式：manual → lowest_cost, 读回一致。
    target = PREFIX + "manual"
    hit = find(target)
    switched = 0
    if hit:
        status, _ = call("POST", "/api/v1/group/update/%d" % hit["id"], payload(target, "lowest_cost", grant))
        after = find(target)
        switched = status
        record("mode can be switched on update",
               status == 200 and (after or {}).get("mode") == "lowest_cost",
               "update HTTP %d mode=%s" % (status, (after or {}).get("mode")))
    else:
        record("mode can be switched on update", False, "manual group missing")

    # 4) 缺名 / 重名。
    body = payload(PREFIX + "noname", "failover", grant)
    body.pop("name")
    status, _ = call("POST", "/api/v1/group/create", body)
    record("a group without a name is rejected", status == 400, "HTTP %d" % status)
    status, _ = call("POST", "/api/v1/group/create", payload(PREFIX + "failover", "failover", grant))
    record("a duplicate group name is rejected", status != 200, "HTTP %d for a duplicate name" % status)

    # 5) 引用不存在的授权。
    status, _ = call("POST", "/api/v1/group/create", payload(PREFIX + "badgrant", "failover", 99999999))
    record("a group referencing an unknown grant is rejected", status != 200,
           "HTTP %d for channel_grant_id=99999999" % status)

    # 6) 删除：存在 → 200 且列表里消失；不存在 → 非 200。
    # 删一个本轮确实建成的分组（bogus/badgrant 都被接口正确拒绝了, 不能拿来当删除对象）。
    hit = find(PREFIX + "least_busy")
    if hit:
        status, _ = call("DELETE", "/api/v1/group/delete/%d" % hit["id"])
        record("deleting an existing group works", status == 200 and find(hit["name"]) is None,
               "HTTP %d name=%s" % (status, hit["name"]))
    else:
        record("deleting an existing group works", False, "no group to delete")
    status, _ = call("DELETE", "/api/v1/group/delete/99999999")
    record("deleting an unknown group reports an error", status != 200, "HTTP %d" % status)

    # 7) 未登录。
    saved = COOKIE.pop("v", None)
    status, _ = call("GET", "/api/v1/group/list")
    if saved:
        COOKIE["v"] = saved
    record("group endpoints require auth", status == 401, "HTTP %d without cookie" % status)

    cleanup()
    leftover = [g.get("name") for g in groups() if str(g.get("name", "")).startswith(PREFIX)]
    record("audit cleans up its fixtures", not leftover, "leftover=%s" % leftover)

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("GROUP_AUDIT total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
