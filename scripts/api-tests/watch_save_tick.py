"""Watch the usage/stats tables across the next stats-save tick (NM-DS-002).

Samples every 15s for ~9 minutes and prints, for the current hour:
  - usage_hourlies row for mock-good (DB, raw)
  - /api/v1/stats/usage value for mock-good (DB + in-memory merge)
  - stats_dailies / stats_hourlies values from the DB
  - relay_logs row count + success count for the hour
A tick that OVERWRITES instead of accumulating shows up as the usage row
collapsing to just the post-flush delta.

Run: python watch_save_tick.py
"""

import os
import datetime
import json
import sqlite3
import sys
import time
import urllib.request

DB = r"D:\奇怪的软件\octopus\data\data.db"
ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")


def admin_session():
    cookie = {}

    def call(method, path, payload=None):
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(ADMIN + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if cookie.get("v"):
            req.add_header("Cookie", cookie["v"])
        with urllib.request.urlopen(req, timeout=30) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    cookie["v"] = v.split(";")[0]
            body = resp.read().decode()
            return json.loads(body) if body else {}

    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    return call


def sample(call):
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    hour = datetime.datetime.now().strftime("%Y%m%d%H")
    today = datetime.datetime.now().strftime("%Y%m%d")
    db_good = conn.execute("select request_success, input_token, output_token from usage_hourlies "
                           "where model_name='mock-good' and channel_name='DS-TEST-mock'").fetchone()
    db_usage_rows = conn.execute("select count(*), coalesce(sum(request_success),0) from usage_hourlies "
                                 "where hour=?", (hour,)).fetchone()
    db_logs = conn.execute("select count(*), coalesce(sum(case when status='success' then 1 else 0 end),0), "
                           "coalesce(sum(prompt_tokens),0), coalesce(sum(completion_toks),0) "
                           "from relay_logs where strftime('%Y%m%d%H', started_at/1000, 'unixepoch', 'localtime')=?",
                           (hour,)).fetchone()
    db_daily = conn.execute("select request_success, request_failed from stats_dailies where date=?", (today,)).fetchone()
    db_hourly = conn.execute("select request_success, request_failed from stats_hourlies where hour=? and date=?",
                             (int(hour[-2:]), today)).fetchone()
    conn.close()

    items = (call("GET", "/api/v1/stats/usage?range=24h").get("data") or {}).get("items") or []
    api_good = None
    for i in items:
        if i.get("model_name") == "mock-good" and i.get("channel_name") == "DS-TEST-mock":
            api_good = (i.get("request_success"), i.get("input_token"), i.get("output_token"))

    return {
        "db_usage_mock_good": db_good,
        "api_usage_mock_good": api_good,
        "db_usage_rows": db_usage_rows,
        "db_logs(hour)": db_logs,
        "db_daily": db_daily,
        "db_hourly": db_hourly,
    }


def main():
    call = admin_session()
    minutes = float(sys.argv[1]) if len(sys.argv) > 1 else 9.0
    deadline = time.time() + minutes * 60
    last = None
    while time.time() < deadline:
        s = sample(call)
        tag = "" if s == last else "   <== CHANGED"
        print(time.strftime("%H:%M:%S"), json.dumps(s, default=str), tag, flush=True)
        last = s
        time.sleep(15)
    print("WATCH_DONE")


if __name__ == "__main__":
    sys.exit(main())
