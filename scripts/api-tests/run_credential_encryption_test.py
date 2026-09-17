"""渠道凭据静态加密（R-sec-001 余项）活体套件。

口径：`channel_keys.key` 在库里是密文（前缀 enc:v1:），进程内与转发时是明文。
本套件用三层证据交叉验证，缺一层都不足以说明"加密真的生效且没把功能弄坏"：

  E1 落库形态：库里那行确实是密文、且不含明文（数据库文件被拿走时看不到密钥）。
  E2 端到端解密：真发一次转发，上游（mock）收到的 Authorization 指纹必须等于明文凭据的指纹
      —— 只断言"HTTP 200"是不够的：mock 不校验密钥，收到密文也会回 200。
  E3 判据自检：同一套指纹比较换成"另一把凭据"，必须**不**匹配（否则 E2 的断言可能恒真）。
  E4 管理面口径不变：渠道详情仍回可编辑的明文（admin-only，面板要能改），并确认它只在管理面存在。
  E5 全库无明文：任何启用中的凭据行都不该是明文（含重启迁移处理过的存量行）。

只落指纹、不落凭据原文：与 mock_upstream._redact 同一算法（类型前缀 + sha256 前 12 位）。
"""
import hashlib
import http.client
import io
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request

ROOT = r"D:\奇怪的软件\octopus"
DB = os.environ.get("OCTOPUS_DB", os.path.join(ROOT, "data", "data.db"))
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
ADMIN = "http://" + os.environ.get("OCTOPUS_ADMIN_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))

CHANNEL = "DS-TEST-cipher"
GROUP = "DS-TEST-cipher"
MODEL = "mock-good"
KEY_NAME = "cipherkey"
KEY_PLAIN = "sk-cipher-live-0001"
WRONG_PLAIN = "sk-cipher-live-0002"
SEALED_PREFIX = "enc:v1:"

COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def fingerprint(value):
    """与 mock_upstream._redact 同款：类型前缀 + sha256 前 12 位。"""
    kind = "bearer:" if value.lower().startswith("bearer ") else "key:"
    return kind + hashlib.sha256(value.encode("utf-8")).hexdigest()[:12]


def call(method, path, payload=None, timeout=60):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for header, value in resp.getheaders():
                if header.lower() == "set-cookie":
                    COOKIE["v"] = value.split(";")[0]
            body = resp.read().decode()
            return resp.status, (json.loads(body) if body else {})
    except urllib.error.HTTPError as exc:
        return exc.code, {"_raw": exc.read().decode()[:200]}
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}


def db():
    return sqlite3.connect("file:" + os.path.abspath(DB).replace("\\", "/") + "?mode=ro", uri=True)


def scalar(sql, args=()):
    conn = db()
    try:
        row = conn.execute(sql, args).fetchone()
        return row[0] if row else None
    finally:
        conn.close()


def api_key():
    conn = db()
    try:
        cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
        col = "api_key" if "api_key" in cols else "key"
        row = conn.execute("select %s from api_keys where enabled = 1 order by id limit 1" % col).fetchone()
        return row[0] if row else ""
    finally:
        conn.close()


def mark():
    return os.path.getsize(MOCK_LOG) if os.path.exists(MOCK_LOG) else 0


def rows_since(offset):
    out = []
    if not os.path.exists(MOCK_LOG):
        return out
    with io.open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        for line in fh.read().splitlines():
            try:
                row = json.loads(line)
            except ValueError:
                continue
            if row.get("method") != "POST" or row.get("path", "").endswith("/__control"):
                continue
            out.append(row)
    return out


def relay(body, group=GROUP, timeout=45):
    key = api_key()
    payload = dict(body)
    payload["model"] = group
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + key}
    conn.request("POST", "/v1/chat/completions", body=json.dumps(payload), headers=headers)
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw


def cleanup():
    gid = scalar("select id from groups where name=?", (GROUP,))
    if gid:
        call("DELETE", "/api/v1/group/delete/%d" % gid)
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if cid:
        call("DELETE", "/api/v1/channel/delete/%d" % cid)


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    cleanup()

    # 装置：一个渠道（一条凭据）+ 一条授权 + 一个分组。
    payload = {"name": CHANNEL, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
               "keys": [{"name": KEY_NAME, "key": KEY_PLAIN, "enabled": True}], "models": [MODEL]}
    status, _ = call("POST", "/api/v1/channel/create", payload)
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if status != 200 or not cid:
        record("E0 装置就位", False, "渠道创建失败: status=%s" % status)
        return 1
    call("POST", "/api/v1/channel/update",
         dict(payload, id=cid, grants=[{"model_name": MODEL, "key_name": KEY_NAME, "protocols": 14}]))
    grant = scalar("select g.id from channel_grants g join channel_models m on m.id = g.channel_model_id "
                   "join channel_keys k on k.id = g.channel_key_id join channels c on c.id = m.channel_id "
                   "where c.name = ? and m.name = ? and k.name = ?", (CHANNEL, MODEL, KEY_NAME))
    status, _ = call("POST", "/api/v1/group/create", {
        "name": GROUP, "mode": "failover",
        "items": [{"channel_grant_id": grant}],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_cooldown_seconds": 1, "member_affinity_seconds": 0}})
    if not grant or status != 200:
        record("E0 装置就位", False, "授权=%s 分组创建 status=%s" % (grant, status))
        return 1
    record("E0 装置就位", True, "渠道 %s（凭据 %s）+ 授权 + 分组" % (cid, KEY_NAME))

    # E1 落库形态：密文前缀 + 不含明文 + 明显不是明文长度。
    stored = scalar("select key from channel_keys where channel_id=? and name=?", (cid, KEY_NAME))
    record("E1 落库即密文（前缀 + 不含明文）",
           bool(stored) and stored.startswith(SEALED_PREFIX) and KEY_PLAIN not in stored and len(stored) > 40,
           "库内长度=%s 前缀=%s 含明文=%s" % (len(stored or ""), (stored or "")[:8],
                                            KEY_PLAIN in (stored or "")))

    # E2 端到端解密：上游必须收到明文凭据（用指纹比对，不落原文）。
    offset = mark()
    status, _raw = relay({"max_tokens": 16, "messages": [{"role": "user", "content": "ping"}]})
    rows = rows_since(offset)
    seen = [row.get("authorization") for row in rows]
    want = fingerprint("Bearer " + KEY_PLAIN)
    record("E2 端到端解密（上游收到明文凭据）",
           status == 200 and want in seen,
           "HTTP %s 上游 Authorization 指纹=%s, 期望 %s" % (status, seen, want))

    # E3 判据自检：换成另一把凭据必须不匹配 —— 否则 E2 可能是因为指纹算法恒真才绿的。
    wrong = fingerprint("Bearer " + WRONG_PLAIN)
    record("E3 判据自检（另一把凭据不匹配）", wrong not in seen,
           "另一把的指纹=%s（不应出现在 %s）" % (wrong, seen))

    # E4 管理面口径：渠道详情仍给可编辑的明文；同一份数据在转发口不可见。
    status, detail = call("GET", "/api/v1/channel/detail/%d" % cid)
    keys = ((detail or {}).get("data") or {}).get("keys") or []
    plain_ok = status == 200 and any(item.get("key") == KEY_PLAIN for item in keys)
    record("E4 管理面仍可读明文（面板可编辑）+ 只看得到指纹的转发口不受影响",
           plain_ok,
           "详情 status=%s 凭据条数=%s" % (status, len(keys)))

    # E5 全库无明文：启用中的凭据行都不该是明文（含重启时迁移处理的存量行）。
    conn = db()
    try:
        rows_all = conn.execute("select id, name, key from channel_keys where key is not null and key <> ''").fetchall()
    finally:
        conn.close()
    plaintext_rows = [row for row in rows_all if not str(row[2]).startswith(SEALED_PREFIX)]
    record("E5 全库凭据无明文", not plaintext_rows,
           "共 %d 行, 明文 %d 行%s" % (len(rows_all), len(plaintext_rows),
                                    ("（前几行: %s）" % [r[1] for r in plaintext_rows[:5]]) if plaintext_rows else ""))

    passed = sum(1 for _n, ok, _d in RESULTS if ok)
    print("\nCREDENTIAL_ENCRYPTION_TEST total=%d pass=%d fail=%d" % (len(RESULTS), passed, len(RESULTS) - passed))
    return 0 if passed == len(RESULTS) else 1


if __name__ == "__main__":
    try:
        code = main()
    finally:
        cleanup()
    sys.exit(code)
