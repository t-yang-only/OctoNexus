"""模型名智能重写（吸收上游 lingyuins/octopus 的 exact/wildcard/regex 三态设计）活体套件。

口径：客户端常写死带版本后缀的模型名（claude-3-5-sonnet-20241022），而本地分组名是简名
（claude-sonnet）。规则命中客户端请求名时改写成目标分组名，再按改写后的名字找分组；
**未命中任何规则时逐字保持原名**——没配规则的项目行为与改造前完全一致。

本套件要证明六件事（缺任何一条都不算交付）：

  M1 鉴权：管理面接口匿名不可读、不可写（规则表能反推出内部模型拓扑）。
  M2 直通不变量：**没有任何规则命中时，转发必须与改造前逐字一致**——用"没配规则时
     原分组名照常 200、乱造的名字照常 model not found"来验，这是升级安全性的判据。
  M3 重写生效：配一条 exact 规则后，用**客户端长名**发转发请求必须成功，且落到目标分组。
  M4 优先级：同一条输入在两条规则都能命中时，由优先级高的那条决定（先命中先返回）。
  M5 停用即失效：把规则停用后，同一个客户端长名必须重新变成 model not found。
  M6 试跑端点：/test 报告命中的是哪条规则；/dry-run 能在保存前回答"会不会误伤"。
"""
import http.client
import json
import os
import sqlite3
import urllib.error
import urllib.request

ROOT = r"D:\奇怪的软件\octopus"
DB = os.environ.get("OCTOPUS_DB", os.path.join(ROOT, "data", "data.db"))
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
ADMIN = "http://" + os.environ.get("OCTOPUS_ADMIN_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))

CHANNEL = "DS-TEST-modelmap"
GROUP = "DS-TEST-mm-short"        # 本地简名分组（真实存在的分组）
GROUP_ALT = "DS-TEST-mm-alt"      # 第二条规则的目标（验证优先级）
MODEL = "mock-good"
CLIENT_LONG = "claude-3-5-sonnet-20241022"   # 客户端写死的长名（本地没有这个分组）
WILDCARD = "gpt-4o-*"

COOKIE = {}
RESULTS = []
CREATED_RULES = []


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


def call_anon(method, path, payload=None, timeout=30):
    """不带 Cookie 调用：验证鉴权确实拦住了匿名访问。"""
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode()[:200]
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()[:200]
    except Exception as exc:  # noqa: BLE001
        return 0, str(exc)


def db():
    return sqlite3.connect("file:" + os.path.abspath(DB).replace("\\", "/") + "?mode=ro", uri=True)


def scalar(sql, args=()):
    conn = db()
    try:
        row = conn.execute(sql, args).fetchone()
        return row[0] if row else None
    finally:
        conn.close()


def login():
    status, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    return status == 200


def ensure_channel():
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if cid:
        call("DELETE", "/api/v1/channel/delete/%d" % cid)
    payload = {"name": CHANNEL, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
               "keys": [{"name": "mockkey", "key": "sk-test", "enabled": True}], "models": [MODEL]}
    call("POST", "/api/v1/channel/create", payload)
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if not cid:
        return None
    call("POST", "/api/v1/channel/update", dict(payload, id=cid, grants=[
        {"model_name": MODEL, "key_name": "mockkey", "protocols": 14}]))
    return scalar("select g.id from channel_grants g join channel_models m on m.id = g.channel_model_id "
                  "join channel_keys k on k.id = g.channel_key_id join channels c on c.id = m.channel_id "
                  "where c.name = ? and m.name = ? and k.name = ?", (CHANNEL, MODEL, "mockkey"))


def ensure_group(name, grant):
    gid = scalar("select id from groups where name=?", (name,))
    if gid:
        call("DELETE", "/api/v1/group/delete/%d" % gid)
    status, _ = call("POST", "/api/v1/group/create", {
        "name": name, "mode": "failover", "items": [{"channel_grant_id": grant}],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_cooldown_seconds": 1, "member_affinity_seconds": 0}})
    return status == 200


def relay_call(model_name):
    """用指定模型名发一条真实转发，返回 (status, body_snippet)。"""
    key = scalar("select api_key from api_keys where enabled = 1 order by id limit 1") or ""
    body = {"model": model_name, "max_tokens": 8, "messages": [{"role": "user", "content": "ping"}]}
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=45)
    try:
        conn.request("POST", "/v1/chat/completions", json.dumps(body),
                     {"Content-Type": "application/json", "Authorization": "Bearer " + key})
        resp = conn.getresponse()
        text = resp.read().decode()
        return resp.status, text[:200]
    except Exception as exc:  # noqa: BLE001
        return 0, str(exc)
    finally:
        conn.close()


def create_rule(name, pattern, match_type, target, priority, enabled=True):
    status, body = call("POST", "/api/v1/model-mapping/create", {
        "name": name, "pattern": pattern, "match_type": match_type,
        "target_model": target, "priority": priority, "enabled": enabled})
    rule = (body or {}).get("data") or {}
    if rule.get("id"):
        CREATED_RULES.append(rule["id"])
    return status, rule


def cleanup_rules():
    for rid in CREATED_RULES:
        call("DELETE", "/api/v1/model-mapping/delete/%d" % rid)
    CREATED_RULES.clear()


def main():
    if not login():
        record("M0 登录", False, "admin 登录失败")
        return 1
    record("M0 登录", True, "admin/admin 登录成功")

    grant = ensure_channel()
    if not grant:
        record("M0 装置", False, "建渠道失败")
        return 1
    if not ensure_group(GROUP, grant) or not ensure_group(GROUP_ALT, grant):
        record("M0 装置", False, "建分组失败")
        return 1
    record("M0 装置", True, "渠道/两条分组就绪")

    cleanup_rules()

    # --- M1 鉴权 -------------------------------------------------------------
    status, _ = call_anon("GET", "/api/v1/model-mapping/list")
    anon_read_ok = status in (401, 403)
    status2, _ = call_anon("POST", "/api/v1/model-mapping/create",
                           {"name": "x", "pattern": "y", "match_type": "exact", "target_model": "z"})
    anon_write_ok = status2 in (401, 403)
    record("M1 管理面拒绝匿名", anon_read_ok and anon_write_ok,
           "读=%s 写=%s" % (status, status2))

    # --- M2 直通不变量（升级安全性） ----------------------------------------
    # 没配规则时：真实分组名照常 200。
    status, text = relay_call(GROUP)
    baseline_ok = status == 200
    # 没配规则时：客户端长名照常 model not found（这就是要修的那个问题）。
    status_miss, text_miss = relay_call(CLIENT_LONG)
    miss_ok = status_miss != 200 and "model not found" in text_miss
    record("M2 无规则时直通不变量", baseline_ok and miss_ok,
           "简名=%s 长名=%s(%s)" % (status, status_miss, text_miss[:60]))

    # --- M3 重写生效 ---------------------------------------------------------
    status, rule = create_rule("客户端长名→简名", CLIENT_LONG, "exact", GROUP, 10)
    if status != 200 or not rule.get("id"):
        record("M3 建规则", False, "create=%s %s" % (status, rule))
        cleanup_rules()
        return 1
    status, text = relay_call(CLIENT_LONG)
    rewrite_ok = status == 200
    # 原名仍然可用（重写不该破坏既有调用）。
    status_short, _ = relay_call(GROUP)
    record("M3 客户端长名被重写并转发成功", rewrite_ok and status_short == 200,
           "长名=%s 简名=%s" % (status, status_short))

    # 通配符规则
    status_w, rule_w = create_rule("gpt 全版本", WILDCARD, "wildcard", GROUP, 10)
    status, text = relay_call("gpt-4o-2024-11-20")
    record("M3 通配符规则命中", status_w == 200 and status == 200,
           "create=%s 转发=%s" % (status_w, status))

    # 正则规则
    status_r, _ = create_rule("sonnet 正则", r"^claude-.*-sonnet-\d{8}$", "regex", GROUP, 10)
    status, _ = relay_call("claude-3-7-sonnet-20250219")
    record("M3 正则规则命中", status_r == 200 and status == 200,
           "create=%s 转发=%s" % (status_r, status))

    # 非法正则必须被写入口拒绝
    status_bad, _ = create_rule("坏正则", "claude-([unclosed", "regex", GROUP, 10)
    record("M3 非法正则被拒", status_bad == 400, "create=%s" % status_bad)

    # --- M4 优先级 -----------------------------------------------------------
    # 加一条优先级更高的规则，把同一个长名指向另一条分组。
    status_hi, rule_hi = create_rule("高优先级改道", CLIENT_LONG, "exact", GROUP_ALT, 100)
    status, text = relay_call(CLIENT_LONG)
    hi_ok = status_hi == 200 and status == 200
    # 用 /test 确认命中的是高优先级那条（比只看 200 更强：200 两条规则都能给）。
    status_t, body_t = call("POST", "/api/v1/model-mapping/test", {"model_name": CLIENT_LONG})
    matched = ((body_t or {}).get("data") or {})
    matched_rule = matched.get("matched_rule") or {}
    record("M4 高优先级先命中", hi_ok and matched.get("target_model") == GROUP_ALT
           and matched_rule.get("name") == "高优先级改道",
           "target=%s rule=%s" % (matched.get("target_model"), matched_rule.get("name")))

    # --- M5 停用即失效 -------------------------------------------------------
    if rule_hi.get("id"):
        call("POST", "/api/v1/model-mapping/toggle/%d" % rule_hi["id"], {"enabled": False})
    status_off, body_off = call("POST", "/api/v1/model-mapping/test", {"model_name": CLIENT_LONG})
    off_target = ((body_off or {}).get("data") or {}).get("target_model")
    # 停用高优先级后，应回落到原来那条 exact 规则（目标回到 GROUP）。
    record("M5 停用后回落到下一条规则", off_target == GROUP,
           "toggle后 target=%s" % off_target)

    # 把两条都停用 → 必须重新 model not found（证明停用是真的失效，而不是被别的规则兜住）。
    for rid in list(CREATED_RULES):
        call("POST", "/api/v1/model-mapping/toggle/%d" % rid, {"enabled": False})
    status, text = relay_call(CLIENT_LONG)
    record("M5 全部停用后恢复 model not found",
           status != 200 and "model not found" in text,
           "status=%s %s" % (status, text[:60]))

    # --- M6 试跑端点 ---------------------------------------------------------
    status_d, body_d = call("POST", "/api/v1/model-mapping/dry-run",
                            {"match_type": "wildcard", "pattern": "claude-*-sonnet-*",
                             "model_name": "claude-3-5-sonnet-20241022"})
    dry_hit = ((body_d or {}).get("data") or {}).get("matched")
    status_d2, body_d2 = call("POST", "/api/v1/model-mapping/dry-run",
                              {"match_type": "wildcard", "pattern": "gpt-*",
                               "model_name": "claude-3-5-sonnet-20241022"})
    dry_miss = ((body_d2 or {}).get("data") or {}).get("matched")
    record("M6 dry-run 能回答会不会误伤",
           status_d == 200 and dry_hit is True and dry_miss is False,
           "命中=%s 未命中=%s" % (dry_hit, dry_miss))

    # 列表端点
    status_l, body_l = call("GET", "/api/v1/model-mapping/list")
    listed = ((body_l or {}).get("data") or [])
    record("M6 列表端点返回规则", status_l == 200 and len(listed) >= 3,
           "list=%d 条" % len(listed))

    cleanup_rules()

    print("\n" + "=" * 50)
    total = len(RESULTS)
    failed = [r for r in RESULTS if not r[1]]
    for name, ok, detail in RESULTS:
        if not ok:
            print("FAILED  " + name + " :: " + detail)
    print("TOTAL %d, FAILED %d" % (total, len(failed)))
    return 0 if not failed else 1


if __name__ == "__main__":
    raise SystemExit(main())
