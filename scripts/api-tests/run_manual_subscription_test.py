"""手动订阅（R-acct-004 / T-acct-005）活体套件。

口径：上游没有余额接口时人录一条套餐余额与有效期，它按与自动读数**同一口径**折算后并入总余额。
本套件要证明四件事（缺任何一条都不算交付）：

  M1 鉴权：管理面接口匿名不可读、不可写（它列出"我在哪买了什么套餐"）。
  M2 入账：绑到「无自动读数的渠道」的记录 → 该渠道由未知变已知（balance_source=manual），金额计入总额。
  M3 只算一次：同一渠道既有自动读数、又有人录 → 人录那条不计入（counted=false），总额不虚高。
  M4 到期/停用：都不计入总额，但明细照列并带 expired 标记（面板要能提醒续费）。
  M5 校验：空名、点数为负、绑定不存在的渠道都要 400（脏数据不该进总额）。
  M6 判据自检：用手录一个**已知金额**的通用额度（不绑渠道），断言总额恰好增加那笔钱 ——
     总额是聚合值，只有拿"差值等于手录值"来验，才能排除"别处的余额变化被当成了这条的功劳"。
"""
import http.client
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request

ROOT = r"D:\奇怪的软件\octopus"
DB = os.environ.get("OCTOPUS_DB", os.path.join(ROOT, "data", "data.db"))
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
ADMIN = "http://" + os.environ.get("OCTOPUS_ADMIN_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))

CHANNEL_MANUAL = "DS-TEST-subscription-manual"   # 无自动读数的渠道（不跑余额扫描）
CHANNEL_AUTO = "DS-TEST-subscription-auto"       # 有自动读数的渠道（号池刷新写入余额）
GROUP = "DS-TEST-subscription"
MODEL = "mock-good"
UNIT = 500000.0

COOKIE = {}
RESULTS = []
CREATED = []


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
            for header, value in resp.getheaders():
                if header.lower() == "set-cookie":
                    COOKIE["v"] = value.split(";")[0]
            body = resp.read().decode()
            return resp.status, (json.loads(body) if body else {})
    except urllib.error.HTTPError as exc:
        return exc.code, {"_raw": exc.read().decode()[:300]}
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


def summary():
    status, body = call("GET", "/api/v1/balance/summary")
    return status, (body or {}).get("data") or {}


def ensure_channel(name, model, key_name, secret):
    cid = scalar("select id from channels where name=?", (name,))
    if cid:
        call("DELETE", "/api/v1/channel/delete/%d" % cid)
    payload = {"name": name, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
               "keys": [{"name": key_name, "key": secret, "enabled": True}], "models": [model]}
    call("POST", "/api/v1/channel/create", payload)
    cid = scalar("select id from channels where name=?", (name,))
    if not cid:
        return None, None
    call("POST", "/api/v1/channel/update", dict(payload, id=cid, grants=[
        {"model_name": model, "key_name": key_name, "protocols": 14}]))
    grant = scalar("select g.id from channel_grants g join channel_models m on m.id = g.channel_model_id "
                   "join channel_keys k on k.id = g.channel_key_id join channels c on c.id = m.channel_id "
                   "where c.name = ? and m.name = ? and k.name = ?", (name, model, key_name))
    return cid, grant


def ensure_group(grant):
    gid = scalar("select id from groups where name=?", (GROUP,))
    if gid:
        call("DELETE", "/api/v1/group/delete/%d" % gid)
    status, body = call("POST", "/api/v1/group/create", {
        "name": GROUP, "mode": "failover", "items": [{"channel_grant_id": grant}],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_cooldown_seconds": 1, "member_affinity_seconds": 0}})
    return status == 200


def relay_call():
    """发一条真实转发：用于验证「手录余额的渠道」本身能正常转发（记录被人为改了也不影响转发）。"""
    key = scalar("select api_key from api_keys where enabled = 1 order by id limit 1") or ""
    body = {"model": GROUP, "max_tokens": 8, "messages": [{"role": "user", "content": "ping"}]}
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=45)
    conn.request("POST", "/v1/chat/completions", body=json.dumps(body),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    resp = conn.getresponse()
    resp.read()
    conn.close()
    return resp.status


def pool_refresh(channel_id):
    """经号池刷新该渠道凭据余额（mock 的 /api/user/self → quota 500 / used 120）。

    注意 refresh 的路径参数是**号池条目 id**（形如 "<渠道id>:<凭据名>"），不是渠道 id ——
    直接拿渠道 id 去 refresh 只会 404，余额快照不会变，于是「该渠道是否有自动读数」的前置条件不成立。
    """
    status, body = call("GET", "/api/v1/pool/entries?kind=channel")
    items = (body.get("data") or {}).get("items") or []
    entry = next((item for item in items if isinstance(item, dict)
                  and str(item.get("id", "")).startswith("%d:" % channel_id)), None)
    if entry is None:
        return 0, {"_raw": "号池里找不到该渠道的条目（items=%d）" % len(items)}
    return call("POST", "/api/v1/pool/entries/channel/%s/refresh" % entry.get("id"))


def api_key():
    conn = db()
    try:
        cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
        col = "api_key" if "api_key" in cols else "key"
        row = conn.execute("select %s from api_keys where enabled = 1 order by id limit 1" % col).fetchone()
        return row[0] if row else ""
    finally:
        conn.close()


def cleanup():
    for sid in CREATED:
        call("DELETE", "/api/v1/subscription/delete/%d" % sid)
    gid = scalar("select id from groups where name=?", (GROUP,))
    if gid:
        call("DELETE", "/api/v1/group/delete/%d" % gid)
    for name in (CHANNEL_MANUAL, CHANNEL_AUTO):
        cid = scalar("select id from channels where name=?", (name,))
        if cid:
            call("DELETE", "/api/v1/channel/delete/%d" % cid)


def main():
    # M1 必须**在登录之前**跑：一旦登录，cookie 会被后续请求带上，"匿名"就不再是匿名了。
    anon_read = call("GET", "/api/v1/subscription/list")[0]
    anon_write = call("POST", "/api/v1/subscription/create", {"name": "anon", "balance_points": 1})[0]
    record("M1 管理面鉴权（匿名读/写都拒）", anon_read == 401 and anon_write == 401,
           "匿名读=%s 匿名写=%s" % (anon_read, anon_write))

    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    cleanup()

    # 装置：一个无自动读数的渠道 + 一个带自动读数的渠道（后者用号池刷新写入余额）
    manual_cid, manual_grant = ensure_channel(CHANNEL_MANUAL, MODEL, "k1", "sk-sub-manual")
    auto_cid, _auto_grant = ensure_channel(CHANNEL_AUTO, MODEL, "k1", "sk-sub-auto")
    if not manual_cid or not auto_cid or not ensure_group(manual_grant):
        record("M0 装置就位", False, "渠道/分组创建失败")
        return 1
    record("M0 装置就位", True, "无读数渠道 %s / 带读数渠道 %s / 分组" % (manual_cid, auto_cid))

    status, snap = summary()
    if status != 200:
        record("M0 读数快照", False, "summary status=%s" % status)
        return 1
    total_before = snap.get("total", 0)
    manual_ids = {(row.get("web_id") or row.get("id")) for row in []}  # 占位，保持结构清晰

    # M5 校验（放在入账之前：脏数据不该有机会进总额）
    cases = [
        ("空名", {"name": "   ", "balance_points": 1000, "channel_id": manual_cid}),
        ("点数为负", {"name": "负", "balance_points": -1, "channel_id": manual_cid}),
        ("口径为负", {"name": "负口径", "balance_points": 1, "points_per_unit": -5}),
        ("渠道不存在", {"name": "孤儿", "balance_points": 1000, "channel_id": 999999}),
    ]
    bad = []
    for label, payload in cases:
        status, _body = call("POST", "/api/v1/subscription/create", dict(payload, enabled=True))
        if status == 200:
            bad.append(label)
    record("M5 校验（空名/负数/未知渠道都要 400）", not bad, "被放过的脏数据: %s" % (bad or "无"))

    # M2 入账：绑到无读数渠道 → 该渠道由未知变已知（manual），金额进总额
    status, body = call("POST", "/api/v1/subscription/create", {
        "name": "手录套餐", "channel_id": manual_cid, "balance_points": 2 * UNIT, "enabled": True})
    sid_manual = (body.get("data") or {}).get("id")
    if status != 200 or not sid_manual:
        record("M2 手录记录入账", False, "创建失败 status=%s body=%s" % (status, body))
        return 1
    CREATED.append(sid_manual)
    status, snap = summary()
    row = next((item for item in snap.get("channels", []) if item.get("channel_id") == manual_cid), None)
    # 通用额度（不绑渠道）用来做 M6 的差值自检
    status, body = call("POST", "/api/v1/subscription/create", {
        "name": "通用额度", "channel_id": 0, "balance_points": UNIT, "enabled": True})
    sid_general = (body.get("data") or {}).get("id")
    CREATED.append(sid_general)
    status, snap2 = summary()
    row2 = next((item for item in snap2.get("channels", []) if item.get("channel_id") == manual_cid), None)
    manual_ok = bool(row2) and row2.get("known") and row2.get("balance_source") == "manual" \
        and abs(row2.get("balance", 0) - 2.0) < 1e-9
    record("M2 无接口渠道由手录余额入账（来源 manual）",
           manual_ok,
           "渠道行=%s" % ({"known": row2.get("known"), "source": row2.get("balance_source"),
                          "balance": row2.get("balance")} if row2 else "缺"))

    # M6 判据自检：通用额度那笔让总额恰好 +1 个单位（差值口径，排除别处的余额变化）
    delta = round(snap2.get("total", 0) - snap.get("total", 0), 6)
    record("M6 差额自检（通用额度让总额恰好 +1 单位）", abs(delta - 1.0) < 1e-6,
           "总额 %.4f → %.4f（差 %s）" % (snap.get("total", 0), snap2.get("total", 0), delta))

    # M3 只算一次：给「已有自动读数的渠道」也录一条，它不该进总额
    pool_refresh(auto_cid)
    auto_row = next((item for item in summary()[1].get("channels", []) if item.get("channel_id") == auto_cid), None)
    auto_known = bool(auto_row) and auto_row.get("known")
    status, body = call("POST", "/api/v1/subscription/create", {
        "name": "重复的手录", "channel_id": auto_cid, "balance_points": 5 * UNIT, "enabled": True})
    sid_dup = (body.get("data") or {}).get("id")
    CREATED.append(sid_dup)
    status, snap3 = summary()
    dup = next((item for item in snap3.get("manual_subscriptions", []) if item.get("id") == sid_dup), None)
    dup_ok = bool(dup) and dup.get("counted") is False
    record("M3 已有自动读数的渠道不再重复计入（counted=false）",
           auto_known and dup_ok,
           "该渠道自动读数=%s；重复记录 counted=%s（自动读数为 0 或未读到则本条不成立）"
           % (auto_row.get("remaining") if auto_row else None, dup.get("counted") if dup else None))

    # M4 到期与停用都不计入，但明细照列
    status, body = call("POST", "/api/v1/subscription/create", {
        "name": "已过期", "channel_id": 0, "balance_points": 3 * UNIT, "enabled": True,
        "expire_at": int(__import__("time").time()) - 3600})
    sid_expired = (body.get("data") or {}).get("id")
    CREATED.append(sid_expired)
    status, body = call("POST", "/api/v1/subscription/create", {
        "name": "已停用", "channel_id": 0, "balance_points": 4 * UNIT, "enabled": False})
    sid_disabled = (body.get("data") or {}).get("id")
    CREATED.append(sid_disabled)
    status, snap4 = summary()
    rows = {item.get("id"): item for item in snap4.get("manual_subscriptions", [])}
    expired_row = rows.get(sid_expired) or {}
    disabled_row = rows.get(sid_disabled) or {}
    record("M4 过期/停用不计入总额（明细仍列出）",
           expired_row.get("expired") is True and expired_row.get("counted") is False
           and disabled_row.get("counted") is False and snap4.get("manual_expired", 0) >= 1,
           "过期 counted=%s expired=%s；停用 counted=%s；manual_expired=%s"
           % (expired_row.get("counted"), expired_row.get("expired"),
              disabled_row.get("counted"), snap4.get("manual_expired")))

    # M8 列表接口（面板不直接调它：首页读的是余额快照里的 manual_subscriptions；
    # 但接口本身是对外契约的一部分，必须能登录读到刚建的记录）。
    status, body = call("GET", "/api/v1/subscription/list")
    listed = {item.get("id") for item in ((body.get("data") or []) if isinstance(body.get("data"), list) else [])}
    record("M8 列表接口（登录后按主键读到刚建的记录）",
           status == 200 and {sid_manual, sid_general, sid_expired, sid_disabled} <= listed,
           "status=%s 命中=%s" % (status, sorted(listed & {sid_manual, sid_general, sid_expired, sid_disabled})))

    # 转发不受影响：手录余额的渠道照样能转发（余额是账，不是路由开关）
    code = relay_call()
    record("M7 手录余额不影响转发（该渠道仍能 200）", code == 200, "HTTP %s" % code)

    passed = sum(1 for _n, ok, _d in RESULTS if ok)
    print("\nMANUAL_SUBSCRIPTION_TEST total=%d pass=%d fail=%d" % (len(RESULTS), passed, len(RESULTS) - passed))
    return 0 if passed == len(RESULTS) else 1


if __name__ == "__main__":
    try:
        code = main()
    finally:
        cleanup()
    sys.exit(code)
