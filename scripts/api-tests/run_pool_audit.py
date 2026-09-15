"""号池（谷歌/GPT/Claude）统一视图的 API 契约测试（NM-DS-009 新套件）。

覆盖：
  · GET /pool/list 一次给出三个服务商的合并视图（账号数/启用凭据/模型/授权 + 逐账号映射明细）；
  · POST /pool/sync 指定服务商只同步该家；空体=三家一起同步（号池合并的核心动作）；
  · 非法服务商 → 200 + 逐条 notes（"一家出错不吞掉其余结果"的契约）；
  · 未登录访问 → 401；
  · 映射明细的语义：active 账号有启用凭据；从未 active 的账号没有凭据行（"尚无凭据，先同步"）。

关于凭据物化：本地没有真实官方账号（OAuth 需要真凭据），故本套件用**合成账号行**验证视图与契约；
"账号 → 凭据内容"的物化语义由 internal/op 的 Go 单测覆盖（那里有加密 helper 可造真实密文）。
合成账号的 access/refresh 密文留空，故同步时该账号会被记入 notes（解密失败），这本身也是契约的一部分。

Run: python scripts/api-tests/run_pool_audit.py
"""

import datetime
import http.client
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "data", "data.db"))
POOL_PREFIX = "官方账号池-"
PROVIDERS = ["openai", "gemini", "claude"]
RUN_TAG = "ds-test-" + datetime.datetime.now().strftime("%H%M%S")
COOKIE = {}
RESULTS = []


def record(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:300])


def call(method, path, payload=None, timeout=60, with_cookie=True):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if with_cookie and COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for key, value in resp.getheaders():
                if key.lower() == "set-cookie":
                    COOKIE["v"] = value.split(";")[0]
            body = resp.read().decode("utf-8")
            payload = json.loads(body) if body else {}
            # 后端统一信封 {code, message, data}：这里按 data 解包，与前端 apiRequest 同口径。
            return resp.status, (payload.get("data", payload) if isinstance(payload, dict) else payload)
    except urllib.error.HTTPError as exc:
        body = exc.read().decode("utf-8", "replace")
        try:
            return exc.code, json.loads(body)
        except Exception:  # noqa: BLE001
            return exc.code, {"_raw": body[:200]}
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}


def pools_by_provider(payload):
    return {item.get("provider"): item for item in (payload.get("items") or [])}


def seed_accounts():
    """种入合成账号行（access/refresh 留空密文）：验证视图与契约，不碰任何真实凭据。"""
    conn = sqlite3.connect(DB)
    now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    rows = [
        (RUN_TAG + "-g1@example.com", "gemini", "active"),
        (RUN_TAG + "-g2@example.com", "gemini", "revoked"),
        (RUN_TAG + "-o1@example.com", "openai", "pending"),
    ]
    for name, provider, status in rows:
        conn.execute(
            "insert into official_accounts (provider, external_name, status, access_cipher, refresh_cipher,"
            " plan_tier, window_5h, window_7d, healthy, last_error, created_at, updated_at)"
            " values (?, ?, ?, '', '', 'Pro', '62%', '88%', 0, '', ?, ?)",
            (provider, name, status, now, now))
    conn.commit()
    conn.close()
    return [row[0] for row in rows]


def cleanup_accounts():
    conn = sqlite3.connect(DB)
    conn.execute("delete from official_accounts where external_name like ?", (RUN_TAG + "-%",))
    conn.commit()
    conn.close()


def main():
    if not os.path.exists(DB):
        print("找不到本地库：%s" % DB)
        return 2
    status, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    if status != 200:
        print("登录失败：HTTP %s" % status)
        return 2

    before = pools_by_provider(call("GET", "/api/v1/account/official/pool/list")[1])
    gemini_channel_before = (before.get("gemini") or {}).get("channel_id", 0)

    created = seed_accounts()
    try:
        # ---------- 统一视图 ----------
        status, payload = call("GET", "/api/v1/account/official/pool/list")
        pools = pools_by_provider(payload)
        record("GET /pool/list 返回三个服务商的合并视图",
               status == 200 and set(pools) == set(PROVIDERS),
               "HTTP %s providers=%s" % (status, sorted(pools)))
        record("合并视图逐服务商带 members 明细数组",
               all(isinstance((pools.get(p) or {}).get("members"), list) for p in PROVIDERS),
               {p: len((pools.get(p) or {}).get("members") or []) for p in PROVIDERS})

        gemini = pools.get("gemini") or {}
        mine = [m for m in (gemini.get("members") or []) if str(m.get("external_name", "")).startswith(RUN_TAG)]
        record("合成账号出现在所属号池明细里（谷歌 2 个、GPT 1 个）",
               len(mine) == 2 and len([m for m in (pools.get("openai") or {}).get("members") or []
                                       if str(m.get("external_name", "")).startswith(RUN_TAG)]) == 1,
               "gemini 明细=%d openai 明细=%d" % (len(mine), len((pools.get("openai") or {}).get("members") or [])))
        record("未同步的服务商凭据标记为「尚无凭据」（key_exists=false）",
               all(m.get("key_exists") is False for m in mine),
               [(m.get("external_name"), m.get("key_exists")) for m in mine])
        record("账号快照字段透传（套餐/窗口/状态）",
               any(m.get("plan_tier") == "Pro" and m.get("window_5h") == "62%" for m in mine),
               [(m.get("plan_tier"), m.get("window_5h"), m.get("status")) for m in mine][:2])

        # ---------- 单服务商同步 ----------
        status, payload = call("POST", "/api/v1/account/official/pool/sync", {"provider": "gemini"})
        items = payload.get("items") or []
        record("POST /pool/sync 指定服务商只同步该家",
               status == 200 and len(items) == 1 and items[0].get("provider") == "gemini",
               "HTTP %s items=%s" % (status, [(i.get("provider"), i.get("channel_name")) for i in items]))
        item = items[0] if items else {}
        record("号池渠道名按服务商命名（官方账号池-<provider>）",
               item.get("channel_name") == POOL_PREFIX + "gemini",
               item.get("channel_name"))
        record("合成账号密文为空时逐条给出 notes，不吞掉结果",
               bool(item.get("notes")),
               (item.get("notes") or [""])[0][:160])

        status, payload = call("GET", "/api/v1/account/official/pool/list")
        gemini = pools_by_provider(payload).get("gemini") or {}
        record("同步后合并视图出现号池渠道与账号计数",
               gemini.get("channel_id", 0) != 0 and gemini.get("accounts", 0) >= 2,
               "channel_id=%s accounts=%s active_keys=%s models=%s grants=%s"
               % (gemini.get("channel_id"), gemini.get("accounts"), gemini.get("active_keys"),
                  gemini.get("models"), gemini.get("grants")))
        record("空密文账号不会产出启用凭据（active_keys 不虚增）",
               gemini.get("active_keys") == 0,
               "active_keys=%s" % gemini.get("active_keys"))

        # ---------- 三家一起同步（合并动作） ----------
        status, payload = call("POST", "/api/v1/account/official/pool/sync", {})
        items = payload.get("items") or []
        record("POST /pool/sync 空体 = 三家一起同步（号池合并的核心动作）",
               status == 200 and [i.get("provider") for i in items] == PROVIDERS,
               "HTTP %s providers=%s" % (status, [i.get("provider") for i in items]))

        # ---------- 非法服务商 ----------
        status, payload = call("POST", "/api/v1/account/official/pool/sync", {"provider": "nope"})
        notes = " ".join((payload.get("items") or [{}])[0].get("notes") or [])
        record("非法服务商 → 200 + notes 说明（不返回 500）",
               status == 200 and "unknown official account provider" in notes,
               "HTTP %s notes=%s" % (status, notes[:120]))

        # ---------- 鉴权 ----------
        status, _ = call("GET", "/api/v1/account/official/pool/list", with_cookie=False)
        record("未登录访问号池接口 → 401", status == 401, "HTTP %s" % status)
    finally:
        cleanup_accounts()
        if gemini_channel_before == 0:
            status, payload = call("GET", "/api/v1/account/official/pool/list")
            channel_id = (pools_by_provider(payload).get("gemini") or {}).get("channel_id", 0)
            if channel_id:
                del_status, _ = call("DELETE", "/api/v1/channel/delete/%d" % channel_id)
                print("  cleanup: 删除本套件创建的号池渠道 %d -> HTTP %s" % (channel_id, del_status))

    status, payload = call("GET", "/api/v1/account/official/pool/list")
    gemini = pools_by_provider(payload).get("gemini") or {}
    leftovers = [m for m in (gemini.get("members") or []) if str(m.get("external_name", "")).startswith(RUN_TAG)]
    record("清理后合成账号不再出现在视图里", not leftovers, "残留=%d" % len(leftovers))

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("POOL_AUDIT total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
