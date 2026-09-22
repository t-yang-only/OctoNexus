"""告警规则（吸收上游 lingyuins/octopus 的 Alerts）活体套件。

口径：按指标（错误率 / 平均耗时）判定，逐渠道独立，并受两条防噪音机制约束
（样本量下限、按渠道独立的冷却）。

本套件要证明七件事（缺任何一条都不算交付）：

  A1 鉴权：管理面接口匿名不可读、不可写（规则与触发历史会暴露上游拓扑与故障状况）。
  A2 样本量下限：窗口内请求数低于下限时**不触发**——这是告警噪音的头号来源
     （一次偶然失败 = 100% 错误率）。用真实转发造出"1 次失败"来验。
  A3 真实触发：样本足够且越阈值时，/run 真的发到 webhook，并落一条触发记录。
  A4 冷却生效：紧接着再跑一轮，同渠道不重复触发（fired 计数不增加）。
  A5 试算解释：/evaluate 把"会不会报、为什么"说清楚（reason 取 within_threshold /
     insufficient_samples / cooldown / breached 之一）。
  A6 校验：0 阈值、错误率阈值 >100、scope=channel 缺渠道名都要 400。
  A7 更新在最终形态上校验：把 scope 改成 channel 但不给渠道名必须被拒。
"""
import http.client
import json
import os
import sqlite3
import sys
import threading
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

ROOT = r"D:\奇怪的软件\octopus"
DB = os.environ.get("OCTOPUS_DB", os.path.join(ROOT, "data", "data.db"))
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
ADMIN = "http://" + os.environ.get("OCTOPUS_ADMIN_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))

CHANNEL = "DS-TEST-alert"
GROUP = "DS-TEST-alert"
MODEL = "mock-bad"          # mock 上游里必定失败的模型
HOOK_PORT = 18097

COOKIE = {}
RESULTS = []
HOOK_HITS = []
HOOK_LOCK = threading.Lock()
CREATED_RULES = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def call(method, path, payload=None, timeout=90):
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


class HookHandler(BaseHTTPRequestHandler):
    def do_POST(self):  # noqa: N802
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length).decode("utf-8", "replace")
        with HOOK_LOCK:
            HOOK_HITS.append(body)
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"ok":true}')

    def log_message(self, *args):
        return


def hook_count():
    with HOOK_LOCK:
        return len(HOOK_HITS)


def set_setting(key, value):
    return call("POST", "/api/v1/setting/set", {"key": key, "value": value})


def login():
    status, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    return status == 200


def ensure_channel():
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if cid:
        call("DELETE", "/api/v1/channel/delete/%d" % cid)
    payload = {"name": CHANNEL, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
               "keys": [{"name": "k1", "key": "sk-test", "enabled": True}], "models": [MODEL]}
    call("POST", "/api/v1/channel/create", payload)
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if not cid:
        return None
    call("POST", "/api/v1/channel/update", dict(payload, id=cid, grants=[
        {"model_name": MODEL, "key_name": "k1", "protocols": 14}]))
    return scalar("select g.id from channel_grants g join channel_models m on m.id = g.channel_model_id "
                  "join channel_keys k on k.id = g.channel_key_id join channels c on c.id = m.channel_id "
                  "where c.name = ? and m.name = ? and k.name = ?", (CHANNEL, MODEL, "k1"))


def ensure_group(grant):
    gid = scalar("select id from groups where name=?", (GROUP,))
    if gid:
        call("DELETE", "/api/v1/group/delete/%d" % gid)
    status, _ = call("POST", "/api/v1/group/create", {
        "name": GROUP, "mode": "failover", "items": [{"channel_grant_id": grant}],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_cooldown_seconds": 1, "member_affinity_seconds": 0}})
    return status == 200


def relay_call():
    key = scalar("select api_key from api_keys where enabled = 1 order by id limit 1") or ""
    body = {"model": GROUP, "max_tokens": 8, "messages": [{"role": "user", "content": "ping"}]}
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=45)
    try:
        conn.request("POST", "/v1/chat/completions", json.dumps(body),
                     {"Content-Type": "application/json", "Authorization": "Bearer " + key})
        resp = conn.getresponse()
        resp.read()
        return resp.status
    except Exception:  # noqa: BLE001
        return 0
    finally:
        conn.close()


def create_rule(name, threshold, min_requests, cooldown=60, scope="channel", scope_value=CHANNEL):
    status, body = call("POST", "/api/v1/alert-rule/create", {
        "name": name, "metric": "error_rate", "scope": scope, "scope_value": scope_value,
        "threshold": threshold, "window_minutes": 15, "min_requests": min_requests,
        "cooldown_minutes": cooldown, "enabled": True})
    rule = (body or {}).get("data") or {}
    if rule.get("id"):
        CREATED_RULES.append(rule["id"])
    return status, rule


def cleanup():
    for rid in CREATED_RULES:
        call("DELETE", "/api/v1/alert-rule/delete/%d" % rid)
    CREATED_RULES.clear()


def main():
    if not login():
        record("A0 登录", False, "admin 登录失败")
        return 1
    record("A0 登录", True, "admin/admin 登录成功")

    hook = HTTPServer(("127.0.0.1", HOOK_PORT), HookHandler)
    threading.Thread(target=hook.serve_forever, daemon=True).start()

    original = {}
    for key in ("alert_channels", "alert_webhook_url"):
        original[key] = scalar("select value from settings where key = ?", (key,)) or ""

    try:
        cleanup()
        grant = ensure_channel()
        if not grant or not ensure_group(grant):
            record("A0 装置", False, "建渠道/分组失败")
            return 1
        record("A0 装置", True, "渠道/分组就绪（mock-bad 必定失败）")

        set_setting("alert_channels", "webhook")
        set_setting("alert_webhook_url", "http://127.0.0.1:%d/hook" % HOOK_PORT)

        # --- A1 鉴权 ---------------------------------------------------------
        s1, _ = call_anon("GET", "/api/v1/alert-rule/list")
        s2, _ = call_anon("POST", "/api/v1/alert-rule/create", {"name": "x"})
        s3, _ = call_anon("POST", "/api/v1/alert-rule/run", {})
        record("A1 管理面拒绝匿名",
               all(s in (401, 403) for s in (s1, s2, s3)),
               "list=%s create=%s run=%s" % (s1, s2, s3))

        # --- A2 样本量下限 ---------------------------------------------------
        # 先造 1 次失败（样本量 1），规则下限设 5 → 不该触发。
        relay_call()
        status, rule = create_rule("样本不足不报", 30, 5)
        if status != 200 or not rule.get("id"):
            record("A2 建规则", False, "create=%s %s" % (status, rule))
            cleanup()
            return 1
        before = hook_count()
        call("POST", "/api/v1/alert-rule/run", {})
        after = hook_count()
        evals = ((call("POST", "/api/v1/alert-rule/evaluate", {"rule_id": rule["id"]})[1] or {}).get("data") or [])
        reason = evals[0].get("reason") if evals else "(no eval)"
        record("A2 样本不足不触发", after == before and reason == "insufficient_samples",
               "webhook %d→%d evaluate reason=%s" % (before, after, reason))

        # --- A3 真实触发 -----------------------------------------------------
        # 把下限降到 1，样本量 1 且失败 → 错误率 100% > 30% → 触发。
        call("POST", "/api/v1/alert-rule/update/%d" % rule["id"], {"min_requests": 1})
        fires_before = scalar("select count(*) from alert_fires where rule_id = ?", (rule["id"],)) or 0
        before = hook_count()
        status, body = call("POST", "/api/v1/alert-rule/run", {})
        fired = ((body or {}).get("data") or {}).get("fired")
        after = hook_count()
        fires_after = scalar("select count(*) from alert_fires where rule_id = ?", (rule["id"],)) or 0
        record("A3 越阈值真的触发并落记录",
               status == 200 and fired and fired >= 1 and after > before and fires_after > fires_before,
               "run fired=%s webhook %d→%d alert_fires %d→%d" % (fired, before, after, fires_before, fires_after))

        # --- A4 冷却生效 -----------------------------------------------------
        before = hook_count()
        status, body = call("POST", "/api/v1/alert-rule/run", {})
        fired2 = ((body or {}).get("data") or {}).get("fired")
        after = hook_count()
        record("A4 冷却期内不重复触发", after == before and (fired2 == 0),
               "第二次 run fired=%s webhook %d→%d" % (fired2, before, after))

        # --- A5 试算解释 -----------------------------------------------------
        # 刚触发过，所以这里必须看到 cooldown —— 试算要回答的是"**现在**会不会报"，
        # 而不是"阈值有没有被越过"（后者会显示"会触发"但实际不发，是最难自查的困惑）。
        evals = ((call("POST", "/api/v1/alert-rule/evaluate", {"rule_id": rule["id"]})[1] or {}).get("data") or [])
        reasons = {e.get("reason") for e in evals}
        fired_flags = [e.get("fired") for e in evals]
        record("A5 试算回答现在会不会报（含冷却）",
               len(evals) >= 1 and reasons <= {"breached", "cooldown", "within_threshold", "insufficient_samples"}
               and reasons == {"cooldown"} and not any(fired_flags),
               "reasons=%s fired=%s" % (",".join(sorted(reasons)), fired_flags))

        # --- A6 校验 ---------------------------------------------------------
        bad = [
            ("0 阈值", {"name": "x", "metric": "error_rate", "scope": "all", "threshold": 0}),
            ("错误率>100", {"name": "x", "metric": "error_rate", "scope": "all", "threshold": 150}),
            ("channel 缺渠道名", {"name": "x", "metric": "error_rate", "scope": "channel", "threshold": 50}),
            ("非法指标", {"name": "x", "metric": "vibes", "scope": "all", "threshold": 50}),
        ]
        codes = []
        for label, payload in bad:
            code, _ = call("POST", "/api/v1/alert-rule/create", payload)
            codes.append("%s=%s" % (label, code))
        record("A6 非法配置被拒", all("=400" in c for c in codes), ", ".join(codes))

        # --- A7 更新在最终形态上校验 -----------------------------------------
        status, allrule = create_rule("全渠道规则", 50, 5, scope="all", scope_value="")
        if status != 200:
            record("A7 建全渠道规则", False, "create=%s" % status)
        else:
            code, _ = call("POST", "/api/v1/alert-rule/update/%d" % allrule["id"], {"scope": "channel"})
            record("A7 更新在最终形态上校验", code == 400, "改成 channel 但不给渠道名 → %s" % code)

    finally:
        cleanup()
        hook.shutdown()
        for key, value in original.items():
            set_setting(key, value)

    print("\n" + "=" * 50)
    failed = [r for r in RESULTS if not r[1]]
    for name, ok, detail in RESULTS:
        if not ok:
            print("FAILED  " + name + " :: " + detail)
    print("TOTAL %d, FAILED %d" % (len(RESULTS), len(failed)))
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
