"""Diagnose three suspect findings with full response bodies (NM-DS-002 continuation).

  D1  create with enabled=false: does the flag persist / does the relay reject it?
  D2  DELETE /api/v1/apikey/delete/{id}: print the exact error body (earlier run got HTTP 500)
  D3  GET /api/v1/apikey/stats (key self-service): print the raw envelope shape

Run: python diag_keys.py
"""

import os
import json
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
COOKIE = {}


def call(method, path, payload=None, raise_on_error=False):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    COOKIE["v"] = v.split(";")[0]
            body = resp.read().decode()
            return resp.status, (json.loads(body) if body else {})
    except urllib.error.HTTPError as e:
        return e.code, {"_raw": e.read().decode()[:300]}
    except Exception as e:
        return 0, {"_raw": f"{type(e).__name__}: {e}"}


def key_call(path, api_key):
    req = urllib.request.Request(ADMIN + path)
    req.add_header("x-api-key", api_key)
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return resp.status, resp.read().decode()[:600]
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()[:300]


def relay(api_key):
    import http.client
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=60)
    conn.request("POST", "/v1/chat/completions",
                 body=json.dumps({"model": "DS-TEST-formats",
                                  "messages": [{"role": "user", "content": "hi"}], "stream": False}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + api_key})
    r = conn.getresponse()
    raw = r.read().decode("utf-8", "replace")
    conn.close()
    return r.status, raw[:120]


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    print("=== D1 create with enabled=false ===")
    st, created = call("POST", "/api/v1/apikey/create",
                       {"name": "DS-TEST-diag-off", "enabled": False, "expire_at": 0, "max_cost": 0,
                        "rpm": 0, "tpm": 0, "supported_models": []})
    key = created.get("data") or {}
    print("create:", st, "-> id=%s enabled=%s (as returned)" % (key.get("id"), key.get("enabled")))
    _, listing = call("GET", "/api/v1/apikey/list")
    row = next((k for k in (listing.get("data") or []) if k.get("id") == key.get("id")), {})
    print("list  : persisted enabled =", row.get("enabled"))
    code, body = relay(key.get("api_key", ""))
    print("relay :", code, body)

    print("\n=== D3 self-service stats shape ===")
    print("GET /api/v1/apikey/stats ->", key_call("/api/v1/apikey/stats", key.get("api_key", "")))

    print("\n=== D2 delete the fresh key ===")
    st, body = call("DELETE", f"/api/v1/apikey/delete/{key.get('id')}")
    print("delete:", st, json.dumps(body, ensure_ascii=False)[:300])

    print("\n=== D2b delete an untouched key (control) ===")
    _, c2 = call("POST", "/api/v1/apikey/create", {"name": "DS-TEST-diag-plain", "enabled": True})
    k2 = c2.get("data") or {}
    st2, body2 = call("DELETE", f"/api/v1/apikey/delete/{k2.get('id')}")
    print("delete plain:", st2, json.dumps(body2, ensure_ascii=False)[:300])

    _, listing = call("GET", "/api/v1/apikey/list")
    print("\nremaining keys:", [(k["id"], k["name"]) for k in (listing.get("data") or [])])
    return 0


if __name__ == "__main__":
    sys.exit(main())
