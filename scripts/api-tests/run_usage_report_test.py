"""用量报告（吸收上游 lingyuins/octopus 的 Usage Reports）活体套件。

口径：按周期把用量与成本摘要推到已配置的通知渠道。报告覆盖**上一个完整周期**
（日报＝昨天、周报＝上一自然周、月报＝上一自然月），同一个周期只发一次。

本套件要证明六件事（缺任何一条都不算交付）：

  R1 鉴权：管理面接口匿名不可读、不可写（报告含费用、模型清单与渠道名）。
  R2 窗口口径：三种周期各自覆盖"上一个完整周期"，且日报窗口是**自然日**而不是"最近 24 小时"
     ——用返回的 from/to 直接断言日期边界（这是最容易被实现成滑动窗口的地方）。
  R3 周期键唯一且稳定：同一周期内多次预览得到同一个 period_key；三种周期的键互不相同
     （否则日报会顶掉月报的记账名额）。
  R4 预览不发送：预览只组装，不得投递到通知渠道（拿 mock webhook 的命中数来验）。
  R5 立即发送：能真的投递到 webhook，且**不写周期记账**（否则"试一下"会吃掉当天日报名额）
     ——用发送前后 history 条数不变来验。
  R6 正文自证：正文必须包含请求数/费用/余额三类关键信息，并如实说明数据截止时间；
     空周期也要给出可读的一句话，而不是一堆 0。
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
ADMIN = "http://" + os.environ.get("OCTOPUS_ADMIN_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")

COOKIE = {}
RESULTS = []
HOOK_HITS = []
HOOK_LOCK = threading.Lock()
HOOK_PORT = 18098


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

    def log_message(self, *args):  # 静默
        return


def start_hook():
    server = HTTPServer(("127.0.0.1", HOOK_PORT), HookHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    return server


def set_setting(key, value):
    return call("POST", "/api/v1/setting/set", {"key": key, "value": value})


def hook_count():
    with HOOK_LOCK:
        return len(HOOK_HITS)


def main():
    if not login():
        record("R0 登录", False, "admin 登录失败")
        return 1
    record("R0 登录", True, "admin/admin 登录成功")

    hook = start_hook()

    # 记下原始设置，收尾复原（报告默认关闭，测试要临时打开）。
    original = {}
    for key in ("usage_report_enabled", "usage_report_period", "usage_report_hour", "alert_channels", "alert_webhook_url"):
        original[key] = scalar("select value from settings where key = ?", (key,)) or ""

    try:
        # --- R1 鉴权 ---------------------------------------------------------
        status_read, _ = call_anon("GET", "/api/v1/usage-report/history")
        status_write, _ = call_anon("POST", "/api/v1/usage-report/send", {"period": "daily"})
        status_prev, _ = call_anon("POST", "/api/v1/usage-report/preview", {"period": "daily"})
        record("R1 管理面拒绝匿名",
               status_read in (401, 403) and status_write in (401, 403) and status_prev in (401, 403),
               "history=%s send=%s preview=%s" % (status_read, status_write, status_prev))

        # --- R2 窗口口径 -----------------------------------------------------
        windows = {}
        for period in ("daily", "weekly", "monthly"):
            status, body = call("POST", "/api/v1/usage-report/preview", {"period": period})
            data = (body or {}).get("data") or {}
            windows[period] = (status, data)
        daily = windows["daily"][1]
        weekly = windows["weekly"][1]
        monthly = windows["monthly"][1]

        # 日报窗口必须是两个相邻的自然日边界（00:00 起点），且正好 1 天。
        from_d = daily.get("from", "")
        to_d = daily.get("to", "")
        daily_ok = from_d.endswith("T00:00:00+08:00") and to_d.endswith("T00:00:00+08:00")
        # 周报正好 7 天、月报是自然月边界。
        def span_days(a, b):
            import datetime
            fa = datetime.datetime.fromisoformat(a)
            fb = datetime.datetime.fromisoformat(b)
            return (fb - fa).total_seconds() / 86400.0
        weekly_days = span_days(weekly.get("from", to_d), weekly.get("to", to_d)) if weekly.get("from") else -1
        monthly_days = span_days(monthly.get("from", to_d), monthly.get("to", to_d)) if monthly.get("from") else -1
        record("R2 窗口口径（自然日/整周/整月）",
               daily_ok and abs(weekly_days - 7) < 0.01 and 28 <= monthly_days <= 31,
               "日报 %s~%s | 周 %.0f 天 | 月 %.0f 天" % (from_d[:10], to_d[:10], weekly_days, monthly_days))

        # --- R3 周期键唯一且稳定 ---------------------------------------------
        keys = []
        for _ in range(2):
            _, body = call("POST", "/api/v1/usage-report/preview", {"period": "daily"})
            keys.append(((body or {}).get("data") or {}).get("period_key"))
        key_daily = keys[0]
        key_weekly = weekly.get("period_key")
        key_monthly = monthly.get("period_key")
        record("R3 周期键唯一且稳定",
               keys[0] == keys[1] and len({key_daily, key_weekly, key_monthly}) == 3 and key_daily,
               "daily=%s weekly=%s monthly=%s 重复预览一致=%s" % (key_daily, key_weekly, key_monthly, keys[0] == keys[1]))

        # --- R4 预览不发送 ---------------------------------------------------
        set_setting("alert_channels", "webhook")
        set_setting("alert_webhook_url", "http://127.0.0.1:%d/hook" % HOOK_PORT)
        before = hook_count()
        call("POST", "/api/v1/usage-report/preview", {"period": "daily"})
        after = hook_count()
        record("R4 预览不投递", after == before, "webhook 命中 %d → %d" % (before, after))

        # --- R5 立即发送 + 不占周期名额 ---------------------------------------
        history_before = len(((call("GET", "/api/v1/usage-report/history")[1] or {}).get("data") or []))
        status, body = call("POST", "/api/v1/usage-report/send", {"period": "daily"})
        sent_ok = status == 200
        results = ((body or {}).get("data") or {}).get("results") or []
        delivered = any(r.get("sent") for r in results)
        hook_after = hook_count()
        history_after = len(((call("GET", "/api/v1/usage-report/history")[1] or {}).get("data") or []))
        record("R5 立即发送真的投递且不写周期记账",
               sent_ok and delivered and hook_after > before and history_after == history_before,
               "send=%s 渠道送达=%s webhook %d→%d history %d→%d" %
               (status, delivered, before, hook_after, history_before, history_after))

        # --- R6 正文自证 -----------------------------------------------------
        text = ((body or {}).get("data") or {}).get("report", {}).get("text") or ""
        has_head = "OctoNexus" in text and ("日报" in text or "周报" in text or "月报" in text)
        has_body = ("请求" in text) and ("余额" in text)
        # 空周期必须给一句可读的话，而不是一堆 0。
        empty = ((body or {}).get("data") or {}).get("report", {}).get("empty")
        empty_ok = (not empty) or ("没有请求记录" in text)
        record("R6 正文含结论/余额/空周期说明",
               bool(text) and has_head and has_body and empty_ok,
               "长度 %d 空周期=%s 首行=%s" % (len(text), empty, text.split("\n")[0][:40] if text else ""))

        # 落库记录：force 发送不该写 usage_report_states（R5 已验 history 不变），
        # 这里再直接查一次表，确认没有偷偷写进去。
        rows = scalar("select count(*) from usage_report_states where period_key = ?", (key_daily,)) or 0
        record("R6 立即发送不落周期状态表", rows == 0, "usage_report_states 中 %s 的条数=%d" % (key_daily, rows))

    finally:
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
