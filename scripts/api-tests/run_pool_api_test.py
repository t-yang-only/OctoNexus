"""R-pool-ext-001 号池扩展层第一批接口的活体套件（只读写本地实例 + 本地数据库夹具）。

覆盖:
  P1 GET /api/v1/pool/kinds     —— 适配器自描述（能力位 / 字段说明 / secret 标记）
  P2 GET /api/v1/pool/entries   —— 统一视图能列出官方账号池条目, 且**绝不回显凭据**
  P3 GET /api/v1/pool/entries?kind=official —— 指定 kind 只回这一种
  P4 GET /api/v1/pool/entries?kind=nope     —— 未知 kind 明确报错, 不是静默空列表
  P5 GET /api/v1/pool/stats     —— 聚合计数与条目一致
  P6 未登录访问                   —— 401（号池接口全在 Admin 端口 + Auth 之后）

Run: python run_pool_api_test.py   （实例必须在跑; 会直连本地 sqlite 造/清夹具）
"""

import datetime
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))
RUN_TAG = "POOLAPI-" + datetime.datetime.now().strftime("%H%M%S")
# 这个串只写进数据库, 绝不应该出现在任何接口响应里；它同时是"接口不回显凭据"的探针。
FAKE_CIPHER = "cipher-probe-" + RUN_TAG
COOKIE = {}
RESULTS = []


def record(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:220])


def call(method, path, payload=None, timeout=60, with_cookie=True):
    """返回 (HTTP 状态, 解包后的 data, 原始报文)。

    原始报文单独回一份是给"凭据探针"用的：断言只在**报文层**成立才算数
    （解包后的结构可能丢字段，探针要看的是接口到底吐了什么出去）。
    """
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if with_cookie and COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    COOKIE["v"] = v.split(";")[0]
            raw = resp.read().decode()
            return resp.status, _unwrap(raw), raw
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        return exc.code, _unwrap(raw), raw
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}, ""


def _unwrap(raw):
    if not raw:
        return {}
    try:
        body = json.loads(raw)
    except ValueError:
        return {"_raw": raw[:200]}
    # 本项目响应统一是 {code,message,data}：套件一律按 data 断言，避免各套件自己拆一层。
    return body.get("data", body) if isinstance(body, dict) else body


def db():
    return sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)


def seed_accounts():
    """造三条账号夹具（access/refresh 用探针串, 不含任何真实凭据）。"""
    conn = sqlite3.connect(os.path.abspath(DB))
    now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    rows = [
        (RUN_TAG + "-g1@example.com", "gemini", "active", 1),
        (RUN_TAG + "-g2@example.com", "gemini", "revoked", 0),
        (RUN_TAG + "-c1@example.com", "claude", "active", 0),
    ]
    for name, provider, status, healthy in rows:
        conn.execute(
            "insert into official_accounts (provider, external_name, status, access_cipher, refresh_cipher,"
            " plan_tier, window_5h, window_7d, healthy, last_error, created_at, updated_at)"
            " values (?, ?, ?, ?, ?, 'Pro', '62%', '88%', ?, '', ?, ?)",
            (provider, name, status, FAKE_CIPHER, FAKE_CIPHER, healthy, now, now))
    conn.commit()
    conn.close()
    return [row[0] for row in rows]


def cleanup_accounts():
    conn = sqlite3.connect(os.path.abspath(DB))
    conn.execute("delete from official_accounts where external_name like ?", (RUN_TAG + "-%",))
    conn.commit()
    conn.close()


def main():
    status, _, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    if status != 200:
        record("登录", False, "HTTP %s" % status)
        return 1
    names = seed_accounts()
    try:
        # P1 适配器自描述
        status, payload, _ = call("GET", "/api/v1/pool/kinds")
        items = (payload or {}).get("items") or []
        official = next((item for item in items if item.get("kind") == "official"), None)
        caps = set((official or {}).get("capabilities") or [])
        fields = (official or {}).get("fields") or []
        secret_field = next((f for f in fields if f.get("name") == "access_token"), None)
        record("P1 号池后端自描述（能力位 + 字段说明）",
               status == 200 and official is not None and official.get("builtin") is True
               and {"list", "get", "probe", "refresh", "sync"} <= caps
               and secret_field is not None and secret_field.get("secret") is True,
               "HTTP %s kinds=%s caps=%s" % (status, [i.get("kind") for i in items], sorted(caps)))

        # P2 统一视图能列出夹具账号，且响应里绝不出现凭据
        status, payload, raw = call("GET", "/api/v1/pool/entries")
        entries = (payload or {}).get("items") or []
        mine = [e for e in entries if str(e.get("name", "")).startswith(RUN_TAG)]
        record("P2 统一视图列出官方账号池条目（3 条夹具全部在列）",
               status == 200 and len(mine) == 3 and all(e.get("kind") == "official" for e in mine),
               "HTTP %s 命中 %d/3；示例=%s" % (status, len(mine), mine[0] if mine else None))

        leaked = FAKE_CIPHER in raw or "access_cipher" in raw or "refresh_cipher" in raw
        record("P2b 条目接口绝不回显凭据（探针串未出现在响应里）",
               not leaked, "探针=%s 泄漏=%s" % (FAKE_CIPHER, leaked))

        # P2c 账号→凭据的映射状态可读（active 且未物化时 key_exists=false, status 原样回）
        by_name = {e.get("name"): e for e in mine}
        active = by_name.get(RUN_TAG + "-g1@example.com") or {}
        revoked = by_name.get(RUN_TAG + "-g2@example.com") or {}
        record("P2c 映射状态可读（状态/健康/明细齐全）",
               active.get("status") == "active" and active.get("healthy") is True
               and (active.get("detail") or {}).get("is_active") is True
               and revoked.get("status") == "revoked" and (revoked.get("detail") or {}).get("is_active") is False,
               "active=%s revoked=%s" % (active.get("status"), revoked.get("status")))

        # P3 kind 过滤
        status, payload, _ = call("GET", "/api/v1/pool/entries?kind=official")
        filtered = (payload or {}).get("items") or []
        record("P3 kind=official 只回这一种后端",
               status == 200 and len(filtered) >= 3 and all(e.get("kind") == "official" for e in filtered),
               "HTTP %s 条目=%d" % (status, len(filtered)))

        # P4 未知 kind 明确报错（不是静默空表）
        status, payload, raw = call("GET", "/api/v1/pool/entries?kind=nope")
        record("P4 未知 kind 明确报错", status == 400 and "unknown pool kind" in raw,
               "HTTP %s body=%s" % (status, raw[:90]))

        # P5 聚合计数与条目一致
        status, payload, _ = call("GET", "/api/v1/pool/stats")
        by_kind = ((payload or {}).get("by_kind") or {}).get("official") or {}
        _, entries_payload, _ = call("GET", "/api/v1/pool/entries?kind=official")
        count = len((entries_payload or {}).get("items") or [])
        record("P5 聚合计数与条目一致",
               status == 200 and by_kind.get("entries") == count and (payload or {}).get("total", 0) >= count,
               "stats=%s 条目=%d" % (by_kind, count))

        # P6 未登录必须 401
        status, _, _ = call("GET", "/api/v1/pool/entries", with_cookie=False)
        record("P6 未登录访问被拒（401）", status == 401, "HTTP %s" % status)
    finally:
        cleanup_accounts()

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("POOLAPI total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
