"""Decisive experiment for the usage_hourlies overwrite bug (NM-DS-002).

Sequence:
  1. read usage for the current hour (DB value after the last save tick)
  2. fire 2 mock-good calls -> they land in the in-memory bucket only
  3. read usage again -> the merge should already show DB + memory
  4. wait past the next stats-save tick (default 10 min cycle)
  5. read usage again:
       correct  -> value stays (or grows): DB row accumulates
       buggy    -> value collapses to just the post-flush delta (earlier counts lost)

Run: python usage_overwrite_experiment.py
"""

import os
import http.client
import json
import sqlite3
import sys
import time
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
DB = r"D:\奇怪的软件\octopus\data\data.db"


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


def mock_good_row(call):
    items = (call("GET", "/api/v1/stats/usage?range=24h").get("data") or {}).get("items") or []
    for i in items:
        if i.get("model_name") == "mock-good" and i.get("channel_name") == "DS-TEST-mock":
            return i
    return None


def relay_call(path, payload, key, stream=False, timeout=60):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", path, body=json.dumps(payload),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw


def db_row():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    row = conn.execute("select request_success, input_token, output_token from usage_hourlies "
                       "where model_name='mock-good' and channel_name='DS-TEST-mock'").fetchone()
    conn.close()
    return row


def main():
    admin = admin_session()
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    key = conn.execute("select api_key from api_keys where enabled=1 and (supported_models is null or supported_models='[]') limit 1").fetchone()[0]
    conn.close()

    before = mock_good_row(admin)
    print("step 1 usage(mock-good) before new calls:", before and (before["request_success"], before["input_token"]))
    print("          db row:", db_row())

    for _ in range(2):
        st, _raw = relay_call("/v1/chat/completions",
                              {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}],
                               "stream": False}, key)
        assert st == 200, st

    after_calls = mock_good_row(admin)
    print("step 3 usage(mock-good) after 2 calls (before save tick):",
          after_calls and (after_calls["request_success"], after_calls["input_token"]))

    print("step 4 waiting 75s for the stats-save tick ...")
    time.sleep(75)

    after_tick = mock_good_row(admin)
    print("step 5 usage(mock-good) after the save tick:",
          after_tick and (after_tick["request_success"], after_tick["input_token"]))
    print("          db row:", db_row())

    before_n = (before or {}).get("request_success", 0)
    after_n = (after_tick or {}).get("request_success", 0)
    expected_min = before_n + 2
    ok = after_n >= expected_min
    print(f"\nRESULT before={before_n} +2 calls => after_tick={after_n} expected>={expected_min} -> "
          f"{'OK (accumulates)' if ok else 'BUG (earlier same-hour counts lost by overwrite)'}")
    print("EXPERIMENT_EXIT=" + ("0" if ok else "1"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
