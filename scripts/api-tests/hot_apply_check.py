"""A/B check: does stats_save_interval apply without a restart? (NM-DS-002)

Before the fix, setSetting only re-registered the model-info and quota-scan tasks, so a new
save interval silently waited for the next restart. This script sets the interval to 1 minute
through the public API, fires one relay call, and then watches the usage_hourlies row: if the
row updates within ~90s the new cadence took effect immediately (a 10-minute tick could not
have fired that early).

Run: python hot_apply_check.py
"""

import os
import datetime
import json
import sqlite3
import sys
import time
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
DB = r"D:\奇怪的软件\octopus\data\data.db"
WATCH_SECONDS = 100


def admin_session():
    cookie = {}

    def call(method, path, payload=None):
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(ADMIN + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if cookie.get("v"):
            req.add_header("Cookie", cookie["v"])
        with urllib.request.urlopen(req, timeout=60) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    cookie["v"] = v.split(";")[0]
            body = resp.read().decode()
            return json.loads(body) if body else {}

    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    return call


def db_usage(model_name):
    """只取当前整点桶: 跨小时的同名模型是另一行, 不按小时过滤会读到旧桶而误判。"""
    hour = datetime.datetime.now().strftime("%Y%m%d%H")
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    row = conn.execute("select request_success, input_token, output_token from usage_hourlies "
                       "where hour=? and model_name=? and channel_name='DS-TEST-mock'", (hour, model_name)).fetchone()
    conn.close()
    return row


def relay_key():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    row = conn.execute("select api_key from api_keys where enabled=1 "
                       "and (supported_models is null or supported_models='[]') limit 1").fetchone()
    conn.close()
    return row[0]


def relay_call(key):
    import http.client
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=60)
    conn.request("POST", "/v1/chat/completions",
                 body=json.dumps({"model": "DS-TEST-formats",
                                  "messages": [{"role": "user", "content": "hi"}], "stream": False}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    resp = conn.getresponse()
    raw = resp.read()
    conn.close()
    return resp.status, raw[:80]


def main():
    admin = admin_session()
    key = relay_key()

    before = db_usage("mock-good")
    print("DB usage(mock-good) before:", before)

    r = admin("POST", "/api/v1/setting/set", {"key": "stats_save_interval", "value": "1"})
    print("set stats_save_interval=1 ->", r.get("code"), r.get("message"))
    saved = [s for s in (admin("GET", "/api/v1/setting/list").get("data") or [])
             if s.get("key") == "stats_save_interval"]
    print("setting now:", saved[0].get("value") if saved else None)

    st, body = relay_call(key)
    print("relay call:", st, body)
    assert st == 200, st

    t0 = time.time()
    updated = None
    while time.time() - t0 < WATCH_SECONDS:
        time.sleep(5)
        now = db_usage("mock-good")
        if now != before:
            updated = (now, time.time() - t0)
            break
    if updated:
        print(f"DB row updated after {updated[1]:.0f}s -> {updated[0]}  <== new cadence applied WITHOUT restart")
    else:
        print(f"DB row unchanged after {WATCH_SECONDS}s (before={before})  <== interval did not hot-apply")

    admin("POST", "/api/v1/setting/set", {"key": "stats_save_interval", "value": "10"})
    restored = [s for s in (admin("GET", "/api/v1/setting/list").get("data") or [])
                if s.get("key") == "stats_save_interval"]
    print("restored stats_save_interval:", restored[0].get("value") if restored else None)

    ok = bool(updated)
    print("HOT_APPLY_EXIT=" + ("0" if ok else "1"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())

