"""Create the DS-TEST channel + groups in the local octopus instance.

Run:  python setup_test_entities.py [--admin http://127.0.0.1:13303]

Creates (idempotent: an existing DS-TEST-* entity is reused/replaced):
  channel DS-TEST-mock  -> base_url http://127.0.0.1:18099/v1
      models: mock-good / mock-slow / mock-bad      protocols 14 (chat|responses|messages)
              mock-chatonly                          protocols 2  (chat only -> forces conversion)
  group DS-TEST-formats  failover, single member mock-good      (format matrix)
  group DS-TEST-convert  failover, single member mock-chatonly   (Anthropic in -> OpenAI out)
  group DS-TEST-failover failover, mock-bad -> mock-good         (500 failover)
  group DS-TEST-timeout  failover, mock-slow -> mock-good        (timeout switchover)

Never prints channel keys or API keys.
"""

import os
import json
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
PROTO_CHAT, PROTO_RESP, PROTO_MSG = 1 << 1, 1 << 2, 1 << 3
ALL_PROTO = PROTO_CHAT | PROTO_RESP | PROTO_MSG

# Timeout budget for the switching tests: 4s per member attempt.
FAST_RELAY = {
    "member_max_attempts": 2,
    "member_retry_interval_seconds": 1,
    "member_non_stream_response_timeout_seconds": 4,
    "member_stream_first_event_timeout_seconds": 4,
    "member_cooldown_seconds": 30,
    "member_affinity_seconds": 0,
}
PLAIN_RELAY = {
    "member_max_attempts": 1,
    "member_retry_interval_seconds": 1,
    "member_non_stream_response_timeout_seconds": 60,
    "member_stream_first_event_timeout_seconds": 60,
    "member_cooldown_seconds": 30,
    "member_affinity_seconds": 0,
}


class Client:
    def __init__(self, admin=ADMIN):
        self.admin = admin
        self.cookie = None

    def call(self, method, path, payload=None, raw=False):
        url = self.admin + path
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(url, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if self.cookie:
            req.add_header("Cookie", self.cookie)
        try:
            with urllib.request.urlopen(req, timeout=120) as resp:
                body = resp.read().decode()
                for h, v in resp.getheaders():
                    if h.lower() == "set-cookie":
                        self.cookie = v.split(";")[0]
                return json.loads(body) if body else {}
        except urllib.error.HTTPError as e:
            raise SystemExit(f"HTTP {e.code} on {method} {path}: {e.read().decode()[:400]}")

    def login(self):
        r = self.call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
        assert r.get("code") == 200, r
        return r


def find_channel(client, name):
    """No /channel/list route in this build: /channel/stats is the channel index."""
    r = client.call("GET", "/api/v1/channel/stats")
    for ch in r.get("data") or []:
        if ch.get("channel_name") == name:
            detail = client.call("GET", f"/api/v1/channel/detail/{ch['channel_id']}")
            return detail.get("data")
    return None


def find_group(client, name):
    r = client.call("GET", "/api/v1/group/list")
    for g in r.get("data") or []:
        if g.get("name") == name:
            return g
    return None


def main():
    client = Client()
    client.login()

    existing = find_channel(client, "DS-TEST-mock")
    if existing:
        cid = existing["id"]
        client.call("POST", "/api/v1/channel/update", {
            "id": cid, "name": "DS-TEST-mock", "dialect": "generic", "enabled": True,
            "base_url": MOCK_BASE, "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
            "models": ["mock-good", "mock-slow", "mock-bad", "mock-chatonly"],
            "grants": [
                {"model_name": "mock-good", "key_name": "mockkey", "protocols": ALL_PROTO},
                {"model_name": "mock-slow", "key_name": "mockkey", "protocols": ALL_PROTO},
                {"model_name": "mock-bad", "key_name": "mockkey", "protocols": ALL_PROTO},
                {"model_name": "mock-chatonly", "key_name": "mockkey", "protocols": PROTO_CHAT},
            ],
        })
    else:
        client.call("POST", "/api/v1/channel/create", {
            "name": "DS-TEST-mock", "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
            "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
            "models": ["mock-good", "mock-slow", "mock-bad", "mock-chatonly"],
            "grants": [
                {"model_name": "mock-good", "key_name": "mockkey", "protocols": ALL_PROTO},
                {"model_name": "mock-slow", "key_name": "mockkey", "protocols": ALL_PROTO},
                {"model_name": "mock-bad", "key_name": "mockkey", "protocols": ALL_PROTO},
                {"model_name": "mock-chatonly", "key_name": "mockkey", "protocols": PROTO_CHAT},
            ],
        })

    detail = find_channel(client, "DS-TEST-mock")
    if not detail:
        raise SystemExit("channel DS-TEST-mock not found after create")
    cid = detail["id"]
    # ChannelDetail.grants carries no id; /api/v1/channel/grants is the id-bearing index.
    cand = client.call("GET", "/api/v1/channel/grants")
    grants = {c["model_name"]: c["id"] for c in (cand.get("data") or [])
              if c.get("channel_id") == cid and c.get("key_name") == "mockkey"}
    print("channel id:", cid, "enabled:", detail.get("enabled"))
    print("grants:", grants)
    missing = {"mock-good", "mock-slow", "mock-bad", "mock-chatonly"} - set(grants)
    if missing:
        raise SystemExit(f"missing grants: {missing}")

    specs = [
        ("DS-TEST-formats", PLAIN_RELAY, ["mock-good"]),
        ("DS-TEST-convert", PLAIN_RELAY, ["mock-chatonly"]),
        ("DS-TEST-failover", FAST_RELAY, ["mock-bad", "mock-good"]),
        ("DS-TEST-timeout", FAST_RELAY, ["mock-slow", "mock-good"]),
    ]
    for name, relay_config, models in specs:
        items = [{"channel_grant_id": grants[m]} for m in models]
        existing_group = find_group(client, name)
        if existing_group:
            client.call("POST", f"/api/v1/group/update/{existing_group['id']}",
                        {"mode": "failover", "relay_config": relay_config, "items": items})
            print(f"group {name}: updated id={existing_group['id']} members={models}")
        else:
            r = client.call("POST", "/api/v1/group/create",
                            {"name": name, "mode": "failover", "relay_config": relay_config, "items": items})
            gid = (r.get("data") or {}).get("group", {}).get("id") or (r.get("data") or {}).get("id")
            print(f"group {name}: created id={gid} members={models}")

    print("SETUP_OK")


if __name__ == "__main__":
    sys.exit(main())
