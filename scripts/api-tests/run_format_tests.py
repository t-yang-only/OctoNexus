"""Multi-format call matrix against the local octopus relay (port 11234).

Covers 3 inbound protocols x {non-stream, stream} on a member that speaks all
three, plus forced cross-protocol conversion on a chat-only member.

Reads a relay API key straight out of the local sqlite DB (never printed).
Run:  python run_format_tests.py
"""

import os
import http.client
import json
import sqlite3
import sys
import time

RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
DB = r"D:\奇怪的软件\octopus\data\data.db"

RESULTS = []


def relay_api_key():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    rows = conn.execute("select id, name, api_key, supported_models from api_keys where enabled=1").fetchall()
    conn.close()
    for _id, name, key, sup in rows:
        if not sup or sup in ("[]", "null", ""):
            return key, name, _id
    if rows:
        return rows[0][2], rows[0][1], rows[0][0]
    raise SystemExit("no enabled api key in local DB")


def admin_call(method, path, payload=None):
    import urllib.request
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    with urllib.request.urlopen(req, timeout=60) as resp:
        return json.loads(resp.read().decode())


def admin_login_call(method, path, payload=None, cookie=None):
    import urllib.request
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if cookie:
        req.add_header("Cookie", cookie)
    with urllib.request.urlopen(req, timeout=60) as resp:
        hdrs = dict(resp.getheaders())
        return json.loads(resp.read().decode()), hdrs.get("Set-Cookie", "").split(";")[0]


def stats_total(cookie):
    r, _ = admin_login_call("GET", "/api/v1/stats/total", cookie=cookie)
    return r.get("data") or {}


def call_relay(path, payload, api_key, stream, timeout=90):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    body = json.dumps(payload)
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + api_key,
               "Accept": "text/event-stream" if stream else "application/json"}
    t0 = time.time()
    conn.request("POST", path, body=body, headers=headers)
    resp = conn.getresponse()
    status = resp.status
    chunks = []
    if stream:
        while True:
            line = resp.readline()
            if not line:
                break
            chunks.append(line)
            if len(chunks) > 4000:
                break
    else:
        chunks.append(resp.read())
    elapsed = time.time() - t0
    conn.close()
    raw = b"".join(chunks).decode("utf-8", "replace")
    ctype = resp.getheader("Content-Type") or ""
    return {"status": status, "elapsed": elapsed, "raw": raw, "ctype": ctype}


def parse_sse(raw):
    events = []
    for line in raw.splitlines():
        line = line.strip()
        if line.startswith("data:"):
            data = line[5:].strip()
            if data and data != "[DONE]":
                try:
                    events.append(json.loads(data))
                except Exception:
                    events.append({"_unparsed": data[:120]})
    return events


def record(name, ok, detail):
    RESULTS.append((name, ok, detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def main():
    key, key_name, key_id = relay_api_key()
    _, cookie = admin_login_call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    before = stats_total(cookie)
    print(f"relay key: id={key_id} name={key_name} (value not printed)")
    print("stats before:", json.dumps(before, ensure_ascii=False))

    matrix = [
        ("chat non-stream", "/v1/chat/completions",
         {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}], "stream": False},
         False, lambda r: "choices" in r and r["choices"][0]["message"]["content"]),
        ("chat stream", "/v1/chat/completions",
         {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}], "stream": True},
         True, None),
        ("responses non-stream", "/v1/responses",
         {"model": "DS-TEST-formats", "input": "hi", "stream": False}, False,
         lambda r: r.get("object") == "response" and "output" in r),
        ("responses stream", "/v1/responses",
         {"model": "DS-TEST-formats", "input": "hi", "stream": True}, True, None),
        ("messages non-stream", "/v1/messages",
         {"model": "DS-TEST-formats", "max_tokens": 64, "messages": [{"role": "user", "content": "hi"}],
          "stream": False}, False, lambda r: r.get("type") == "message" and "content" in r),
        ("messages stream", "/v1/messages",
         {"model": "DS-TEST-formats", "max_tokens": 64, "messages": [{"role": "user", "content": "hi"}],
          "stream": True}, True, None),
    ]

    for label, path, payload, stream, shape in matrix:
        res = call_relay(path, payload, key, stream)
        if res["status"] != 200:
            record(label, False, f"HTTP {res['status']} body={res['raw'][:200]}")
            continue
        if stream:
            events = parse_sse(res["raw"])
            types = [e.get("type") or e.get("object") for e in events]
            if path.endswith("chat/completions"):
                ok = any(e.get("object") == "chat.completion.chunk" for e in events)
            elif path.endswith("/responses"):
                ok = any(t and str(t).startswith("response.") for t in types)
            else:
                ok = any(e.get("type") == "message_start" for e in events)
            record(label, ok, f"HTTP 200 {res['elapsed']:.2f}s sse_events={len(events)} types={types[:4]}")
        else:
            try:
                obj = json.loads(res["raw"])
            except Exception as e:
                record(label, False, f"unparsable body: {e}: {res['raw'][:200]}")
                continue
            try:
                detail = str(shape(obj))[:80]
                record(label, True, f"HTTP 200 {res['elapsed']:.2f}s -> {detail}")
            except Exception as e:
                record(label, False, f"shape mismatch {e}: {res['raw'][:240]}")

    # ---- forced cross-protocol conversion (member speaks chat only) ----
    cross = [
        ("convert anthropic->openai (/v1/messages)", "/v1/messages",
         {"model": "DS-TEST-convert", "max_tokens": 64, "messages": [{"role": "user", "content": "hi"}],
          "stream": False}, lambda r: r.get("type") == "message"),
        ("convert responses->openai (/v1/responses)", "/v1/responses",
         {"model": "DS-TEST-convert", "input": "hi", "stream": False},
         lambda r: r.get("object") == "response"),
    ]
    for label, path, payload, shape in cross:
        res = call_relay(path, payload, key, False)
        if res["status"] != 200:
            record(label, False, f"HTTP {res['status']} body={res['raw'][:300]}")
            continue
        try:
            obj = json.loads(res["raw"])
            record(label, bool(shape(obj)), f"HTTP 200 {res['elapsed']:.2f}s keys={sorted(obj.keys())[:8]}")
        except Exception as e:
            record(label, False, f"unparsable: {e}: {res['raw'][:200]}")

    # ---- auth negative cases ----
    res = call_relay("/v1/chat/completions", {"model": "DS-TEST-formats",
                                              "messages": [{"role": "user", "content": "hi"}]}, "", False)
    record("relay rejects empty key", res["status"] == 401, f"HTTP {res['status']}")
    res = call_relay("/v1/chat/completions", {"model": "DS-TEST-formats",
                                              "messages": [{"role": "user", "content": "hi"}]}, "not-a-key", False)
    record("relay rejects bogus key", res["status"] == 401, f"HTTP {res['status']}")
    res = call_relay("/v1/chat/completions", {"model": "no-such-group-xyz",
                                              "messages": [{"role": "user", "content": "hi"}]}, key, False)
    record("unknown model -> 4xx", 400 <= res["status"] < 500, f"HTTP {res['status']} body={res['raw'][:150]}")

    time.sleep(3)
    after = stats_total(cookie)
    print("stats after:", json.dumps(after, ensure_ascii=False))
    delta = {k: (after.get(k, 0) or 0) - (before.get(k, 0) or 0) for k in
             ("request_success", "request_failed", "input_token", "output_token", "wait_time")}
    print("stats delta:", json.dumps(delta, ensure_ascii=False))

    failed = [n for n, ok, _ in RESULTS if not ok]
    print(f"\nFORMAT_TESTS total={len(RESULTS)} pass={len(RESULTS) - len(failed)} fail={len(failed)}")
    if failed:
        print("FAILED:", failed)
    print("FORMAT_TESTS_EXIT=" + ("0" if not failed else "1"))
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
