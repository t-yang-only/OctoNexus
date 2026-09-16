"""Live audit for R-alert-001 (multi-channel notifications + real test delivery).

What it proves, on a running instance:
  * every provider gets its own payload shape (generic webhook / Feishu / DingTalk / WeCom);
  * delivery judges business error codes instead of trusting HTTP 200 (Feishu answers 200 + code!=0);
  * SMTP really sends a UTF-8 mail (a local SMTP sink receives the DATA section);
  * `POST /api/v1/setting/notify/test` reports per-channel results, isolated failures included;
  * a REAL event (proactive-probe recovery) goes through the same fan-out and reaches the webhook;
  * the audit restores every setting it touched (no test sink left configured).

Run: python run_notify_audit.py   (instance + mock upstream must be running)
"""

import base64
import http.client
import json
import os
import socket
import sqlite3
import sys
import threading
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
MOCK_ROOT = MOCK_BASE[:-3] if MOCK_BASE.endswith("/v1") else MOCK_BASE
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))
LOG_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")

GROUP = "DS-TEST-notify-probe"
BAD_MODEL = "mock-bad"
GOOD_MODEL = "mock-good"

SETTING_KEYS = [
    "route_probe_enabled", "route_probe_interval_seconds",
    "alert_channels", "alert_webhook_url", "alert_feishu_webhook", "alert_dingtalk_webhook",
    "alert_wecom_webhook",
    "alert_serverchan_sendkey", "alert_smtp_host", "alert_smtp_port", "alert_smtp_user",
    "alert_smtp_from", "alert_smtp_to",
]

RESULTS = []
COOKIE = {}


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
        return e.code, {"_raw": e.read().decode()[:300]}


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def set_setting(key, value):
    return call("POST", "/api/v1/setting/set", {"key": key, "value": value})


def settings_map():
    _, listed = call("GET", "/api/v1/setting/list")
    return {row.get("key"): row.get("value") for row in ((listed or {}).get("data") or [])}


def relay(model, key, timeout=90):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", "/v1/chat/completions",
                 body=json.dumps({"model": model, "messages": [{"role": "user", "content": "notify audit"}],
                                  "stream": False, "max_tokens": 16}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw


def mock_control(model, behavior):
    req = urllib.request.Request(MOCK_ROOT + "/__control",
                                 data=json.dumps({"model": model, "behavior": behavior}).encode(),
                                 method="POST")
    req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read().decode())


def mock_log_offset():
    try:
        return os.path.getsize(LOG_PATH)
    except OSError:
        return 0


def mock_requests_since(offset):
    entries = []
    try:
        with open(LOG_PATH, "r", encoding="utf-8") as fh:
            fh.seek(offset)
            for line in fh:
                line = line.strip()
                if line:
                    try:
                        entries.append(json.loads(line))
                    except ValueError:
                        pass
    except OSError:
        pass
    return entries


class SMTPSink(threading.Thread):
    """够用的 SMTP 服务端: 收下 DATA 段落, 供用例断言邮件真的发出去了。"""

    def __init__(self):
        super().__init__(daemon=True)
        self.sock = socket.socket()
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("127.0.0.1", 0))
        self.sock.listen(5)
        self.port = self.sock.getsockname()[1]
        self.messages = []

    def run(self):
        while True:
            try:
                conn, _ = self.sock.accept()
            except OSError:
                return
            threading.Thread(target=self.handle, args=(conn,), daemon=True).start()

    def handle(self, conn):
        try:
            stream = conn.makefile("rwb")

            def send(line):
                stream.write((line + "\r\n").encode())
                stream.flush()

            send("220 octopus-test ESMTP")
            collecting = False
            buffer = []
            for raw in stream:
                line = raw.decode("utf-8", "replace").rstrip("\r\n")
                if collecting:
                    if line == ".":
                        self.messages.append("\n".join(buffer))
                        collecting = False
                        send("250 OK: queued")
                    else:
                        buffer.append(line)
                    continue
                upper = line.upper()
                if upper.startswith("EHLO") or upper.startswith("HELO"):
                    send("250-octopus-test")
                    send("250 SIZE 10485760")
                elif upper.startswith("MAIL FROM") or upper.startswith("RCPT TO"):
                    send("250 OK")
                elif upper.startswith("DATA"):
                    send("354 End data with <CR><LF>.<CR><LF>")
                    collecting = True
                elif upper.startswith("QUIT"):
                    send("221 Bye")
                    break
                else:
                    send("250 OK")
        finally:
            conn.close()

    def last(self):
        return self.messages[-1] if self.messages else ""

    def body_text(self):
        """取出 base64 正文并解码, 便于断言邮件内容。"""
        message = self.last()
        if "\n\n" not in message:
            return ""
        encoded = message.split("\n\n", 1)[1].strip()
        try:
            return base64.b64decode(encoded.encode()).decode("utf-8", "replace")
        except Exception:  # noqa: BLE001
            return ""


def grant_ids():
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    rows = conn.execute(
        "select m.name, g.id from channel_grants g "
        "join channel_models m on m.id = g.channel_model_id "
        "join channels c on c.id = m.channel_id where c.name = 'DS-TEST-mock'").fetchall()
    conn.close()
    return {name: gid for name, gid in rows}


def cooldowns(group_name):
    _, listed = call("GET", "/api/v1/group/list")
    for row in ((listed or {}).get("data") or []):
        if row.get("name") == group_name:
            return (row.get("runtime") or {}).get("cooldowns") or {}
    return {}


def wait_for(predicate, seconds, interval=1.0):
    deadline = time.time() + seconds
    while time.time() < deadline:
        value = predicate()
        if value:
            return value
        time.sleep(interval)
    return None


def main():
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
    key_col = "api_key" if "api_key" in cols else "key"
    key = conn.execute("select %s from api_keys where enabled = 1 order by id limit 1" % key_col).fetchone()[0]
    conn.close()

    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    original = settings_map()
    sink = SMTPSink()
    sink.start()
    grants = grant_ids()

    def restore():
        """收尾务必还原: 不能让测试用的本地 mock 留在用户的告警配置里。"""
        for setting_key in SETTING_KEYS:
            if setting_key in original:
                set_setting(setting_key, original[setting_key])
        try:
            mock_control(BAD_MODEL, "clear")
        except Exception:  # noqa: BLE001
            pass

    try:
        # ---------- T1 配置五条渠道 ----------
        wanted = {
            "alert_channels": "webhook,feishu,dingtalk,wecom,smtp",
            "alert_webhook_url": MOCK_ROOT + "/notify/echo",
            "alert_feishu_webhook": MOCK_ROOT + "/notify/feishu",
            "alert_dingtalk_webhook": MOCK_ROOT + "/notify/dingtalk",
            "alert_wecom_webhook": MOCK_ROOT + "/notify/wecom",
            "alert_serverchan_sendkey": "SCT-DS-TEST-sendkey",
            "alert_smtp_host": "127.0.0.1",
            "alert_smtp_port": str(sink.port),
            "alert_smtp_user": "",
            "alert_smtp_from": "octopus@example.com",
            "alert_smtp_to": "ops@example.com",
        }
        codes = {k: set_setting(k, v)[0] for k, v in wanted.items()}
        record("all channel settings accepted", all(code == 200 for code in codes.values()),
               json.dumps(codes, ensure_ascii=False))

        # ---------- T2 渠道状态接口 ----------
        status, channels = call("GET", "/api/v1/setting/notify/channels")
        rows = (channels or {}).get("data") or []
        kinds = [row.get("kind") for row in rows]
        configured = all(row.get("configured") for row in rows)
        record("channel status lists every kind and marks them configured",
               status == 200
               and kinds == ["webhook", "feishu", "dingtalk", "wecom", "smtp", "serverchan"] and configured,
               "kinds=%s configured=%s" % (kinds, [row.get("configured") for row in rows]))
        record("channel status never leaks the webhook credential",
               "notify/echo" not in json.dumps(channels, ensure_ascii=False),
               "payload keys=%s" % sorted(rows[0].keys()) if rows else "no rows")

        # ---------- T3 发送前真实测试 ----------
        offset = mock_log_offset()
        status, tested = call("POST", "/api/v1/setting/notify/test")
        results = (tested or {}).get("data") or []
        sent = [r for r in results if r.get("sent")]
        sent_kinds = {r.get("kind") for r in sent}
        result_kinds = {r.get("kind") for r in results}
        # 实例是否跑在"测试模式"（Server酱 的站点被指向本地桩），决定 serverchan 能不能真的发成功：
        # 生产模式下套件用的是假 sendkey，官方站点会回 400，此时只要求它参与投递并给出结论。
        serverchan_via_stub = any(
            (e.get("path") or "").endswith(".send") for e in mock_requests_since(offset))
        record("test delivery reaches every configured channel",
               status == 200
               and {"webhook", "feishu", "dingtalk", "wecom", "smtp"} <= sent_kinds
               and ("serverchan" in sent_kinds if serverchan_via_stub else "serverchan" in result_kinds),
               "results=%s 桩=%s" % (
                   json.dumps([{r.get("kind"): r.get("sent")} for r in results], ensure_ascii=False),
                   serverchan_via_stub))

        # ---------- T4 provider payload shapes ----------
        posts = [e for e in mock_requests_since(offset) if "/notify/" in (e.get("path") or "")]
        by_path = {}
        for entry in posts:
            by_path.setdefault(entry.get("path"), []).append(entry.get("body") or {})
        echo = (by_path.get("/notify/echo") or [{}])[0]
        feishu = (by_path.get("/notify/feishu") or [{}])[0]
        dingtalk = (by_path.get("/notify/dingtalk") or [{}])[0]
        wecom = (by_path.get("/notify/wecom") or [{}])[0]
        record("generic webhook receives the raw event json", echo.get("type") == "notify_test",
               "echo body type=%s" % echo.get("type"))
        record("feishu receives msg_type/content.text",
               feishu.get("msg_type") == "text" and "[octopus]" in ((feishu.get("content") or {}).get("text") or ""),
               "feishu=%s" % json.dumps(feishu, ensure_ascii=False)[:120])
        record("dingtalk and wecom receive msgtype/text.content",
               dingtalk.get("msgtype") == "text" and wecom.get("msgtype") == "text"
               and "[octopus]" in ((dingtalk.get("text") or {}).get("content") or "")
               and "[octopus]" in ((wecom.get("text") or {}).get("content") or ""),
               "dingtalk=%s wecom=%s" % (json.dumps(dingtalk, ensure_ascii=False)[:80],
                                         json.dumps(wecom, ensure_ascii=False)[:80]))

        # ---- Server酱：推送入口是 <base>/<SendKey>.send, 正文是表单 ----
        serverchan_posts = [e for e in mock_requests_since(offset) if (e.get("path") or "").endswith(".send")]
        serverchan_raw = (((serverchan_posts or [{}])[0].get("body") or {}).get("_raw")) or ""
        serverchan_result = next((r for r in results if r.get("kind") == "serverchan"), {})
        if serverchan_via_stub:
            # 实例跑在测试模式（OCTOPUS_SERVERCHAN_BASE_URL 指向本地桩）：报文形状可以逐字钉住。
            record("serverchan receives the form payload on <base>/<SendKey>.send",
                   len(serverchan_posts) == 1
                   and (serverchan_posts[0].get("path") or "").endswith("SCT-DS-TEST-sendkey.send")
                   and "title=" in serverchan_raw and "desp=" in serverchan_raw and "tags=" in serverchan_raw,
                   "posts=%d path=%s body=%s" % (
                       len(serverchan_posts),
                       (serverchan_posts[0].get("path") if serverchan_posts else None),
                       serverchan_raw[:140]))
        else:
            # 生产模式：请求打到官方站点，桩看不到。此时只能验"渠道确实参与投递且给出了结论"，
            # 并如实标注这条断言没有覆盖到报文形状（报文形状由单测与测试模式的套件覆盖）。
            record("serverchan participates in delivery (stub not in use: instance is not in test mode)",
                   serverchan_result.get("kind") == "serverchan"
                   and (serverchan_result.get("sent") is False or serverchan_result.get("sent") is True)
                   and bool(serverchan_result.get("detail")) or serverchan_result.get("sent") is True,
                   "result=%s（未走桩：生产模式不覆盖报文形状）" % json.dumps(serverchan_result, ensure_ascii=False))

        # ---------- T5 SMTP ----------
        body = sink.body_text()
        record("smtp sink received the rendered utf-8 mail", "[octopus]" in body and "通知渠道测试" in body,
               "decoded body=%r" % body[:120])

        # ---------- T6 business error code on HTTP 200 ----------
        set_setting("alert_feishu_webhook", MOCK_ROOT + "/notify/feishu-fail")
        status, tested = call("POST", "/api/v1/setting/notify/test")
        results = {r.get("kind"): r for r in ((tested or {}).get("data") or [])}
        feishu_result = results.get("feishu") or {}
        others_ok = all((results.get(k) or {}).get("sent") for k in ("webhook", "dingtalk", "wecom", "smtp"))
        record("provider business error is a failure even on http 200",
               feishu_result.get("sent") is False and "19024" in (feishu_result.get("detail") or ""),
               "feishu result=%s" % json.dumps(feishu_result, ensure_ascii=False))
        record("one failing channel does not stop the others", others_ok,
               "others=%s" % json.dumps({k: (results.get(k) or {}).get("sent") for k in ("webhook", "dingtalk", "wecom", "smtp")}))

        # ---- Server酱 也把错误码藏在 200 里: data.errno 非 0 必须判失败 ----
        set_setting("alert_serverchan_sendkey", "SCT-DS-TEST-fail-sendkey")
        status, tested = call("POST", "/api/v1/setting/notify/test")
        results = {r.get("kind"): r for r in ((tested or {}).get("data") or [])}
        failed_serverchan = results.get("serverchan") or {}
        failed_detail = failed_serverchan.get("detail") or ""
        record("serverchan provider error is a failure even on http 200",
               failed_serverchan.get("sent") is False
               and (("errno=1001" in failed_detail) if serverchan_via_stub
                    else ("endpoint returned" in failed_detail or "errno=" in failed_detail)),
               "serverchan result=%s 桩=%s" % (
                   json.dumps(failed_serverchan, ensure_ascii=False), serverchan_via_stub))

        # ---- 没配 SendKey 时给的是"缺哪一项"，不是含糊的失败 ----
        set_setting("alert_serverchan_sendkey", "")
        status, tested = call("POST", "/api/v1/setting/notify/test")
        results = {r.get("kind"): r for r in ((tested or {}).get("data") or [])}
        unset_serverchan = results.get("serverchan") or {}
        record("serverchan without a sendkey reports the missing key",
               unset_serverchan.get("sent") is False
               and "alert_serverchan_sendkey is empty" in (unset_serverchan.get("detail") or ""),
               "serverchan result=%s" % json.dumps(unset_serverchan, ensure_ascii=False))
        set_setting("alert_serverchan_sendkey", "SCT-DS-TEST-sendkey")

        # ---------- T7 configuration gap is reported, not guessed ----------
        set_setting("alert_smtp_host", "")
        status, tested = call("POST", "/api/v1/setting/notify/test")
        results = {r.get("kind"): r for r in ((tested or {}).get("data") or [])}
        smtp_result = results.get("smtp") or {}
        record("unconfigured smtp reports the missing key",
               smtp_result.get("sent") is False and "alert_smtp_host" in (smtp_result.get("detail") or ""),
               "smtp result=%s" % json.dumps(smtp_result, ensure_ascii=False))

        # ---------- T8 a REAL event goes through the same fan-out ----------
        if BAD_MODEL in grants and GOOD_MODEL in grants:
            set_setting("alert_smtp_host", "127.0.0.1")
            set_setting("alert_feishu_webhook", MOCK_ROOT + "/notify/feishu")
            _, existing = call("GET", "/api/v1/group/list")
            hit = next((g for g in ((existing or {}).get("data") or []) if g.get("name") == GROUP), None)
            payload = {
                "mode": "failover",
                "items": [{"channel_grant_id": grants[BAD_MODEL]}, {"channel_grant_id": grants[GOOD_MODEL]}],
                "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                                 "member_non_stream_response_timeout_seconds": 120,
                                 "member_stream_first_event_timeout_seconds": 30,
                                 "member_cooldown_seconds": 120, "member_affinity_seconds": 0},
            }
            if hit:
                call("POST", "/api/v1/group/update/%d" % hit["id"], payload)
            else:
                call("POST", "/api/v1/group/create", dict(payload, name=GROUP))
            set_setting("route_probe_enabled", "true")
            set_setting("route_probe_interval_seconds", "10")
            mock_control(BAD_MODEL, "bad")
            status, raw = relay(GROUP, key)
            cooled = wait_for(lambda: cooldowns(GROUP) or None, 10)
            offset = mock_log_offset()
            mock_control(BAD_MODEL, "ok")
            lifted = wait_for(lambda: not cooldowns(GROUP), 40, interval=1.5)
            events = [e for e in mock_requests_since(offset) if "/notify/echo" in (e.get("path") or "")]
            rendered = [e for e in mock_requests_since(offset) if "/notify/feishu" in (e.get("path") or "")]
            recovered = [e for e in events if (e.get("body") or {}).get("type") == "route_probe_recovered"]
            record("a real probe-recovery event reaches the notification channels",
                   bool(cooled) and bool(lifted) and len(recovered) >= 1,
                   "cooled=%s lifted=%s webhook events=%d types=%s"
                   % (bool(cooled), bool(lifted), len(events), [ (e.get('body') or {}).get('type') for e in events ]))
            if recovered:
                body = recovered[-1].get("body") or {}
                # 通用 webhook 拿的是原始事件 JSON(没有渲染标题), IM 渠道拿的是渲染文本 —— 两边都断言。
                feishu_texts = [((e.get("body") or {}).get("content") or {}).get("text") or "" for e in rendered]
                titled = any("冷却成员已恢复" in text for text in feishu_texts)
                record("recovery event carries the group/model context and a rendered title",
                       (body.get("detail") or {}).get("model") == BAD_MODEL and titled,
                       "detail=%s feishu_title_rendered=%s" % (json.dumps(body.get("detail"), ensure_ascii=False), titled))
        else:
            record("a real probe-recovery event reaches the notification channels", False,
                   "DS-TEST-mock fixtures missing (run setup_test_entities.py first)")
    finally:
        restore()

    # ---------- T9 restore ----------
    after = settings_map()
    untouched = all(after.get(k) == original.get(k) for k in SETTING_KEYS if k in original)
    record("every touched setting is restored", untouched,
           "alert_channels=%s probe=%s" % (after.get("alert_channels"), after.get("route_probe_enabled")))

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("NOTIFY_TEST total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
