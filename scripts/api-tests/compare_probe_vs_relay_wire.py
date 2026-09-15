"""对照「探针请求」与「转发请求」在网络上到底有什么不同（用 mock 上游截获真实报文）。

背景：真实渠道上探针（/channel/fetch-model probe=true）能通、转发却长时间没有首字节。
本地无法看到真实上游侧，但可以看**我们自己两种代码路径发出的报文是否一致**：
  - 若一致 → 差异在上游侧（限流/指纹拦截），转发侧无罪；
  - 若不一致（例如 headers、stream 标志、路径）→ 就是转发侧构造的请求触发了上游拦截。

mock 上游会把 method/path/headers/body 落进 requests.jsonl（Authorization 只存指纹）。
"""

import json
import os
import sqlite3
import time
import urllib.error
import urllib.request

ADMIN = "http://127.0.0.1:13303"
RELAY = "http://127.0.0.1:11234"
HERE = os.path.dirname(os.path.abspath(__file__))
MOCK_LOG = os.path.join(HERE, "requests.jsonl")
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
DB = os.environ.get("OCTOPUS_DB", os.path.join(HERE, "..", "..", "data", "data.db"))
COOKIE = {}


def call(method, path, payload=None, timeout=120):
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
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {"_raw": raw[:200]}
    except Exception as e:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(e).__name__, e)}


def relay_api_key():
    c = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    row = c.execute("select api_key from api_keys where enabled=1 order by id limit 1").fetchone()
    c.close()
    return row[0]


def mark():
    return os.path.getsize(MOCK_LOG) if os.path.exists(MOCK_LOG) else 0


def entries_since(offset):
    out = []
    if not os.path.exists(MOCK_LOG):
        return out
    with open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        for line in fh.read().splitlines():
            try:
                out.append(json.loads(line))
            except ValueError:
                continue
    return out


def summarize(entry):
    keys = {k.lower(): v for k, v in (entry.get("headers") or entry.get("header") or {}).items()}
    interesting = {k: keys.get(k) for k in ("content-type", "accept", "user-agent", "anthropic-version", "authorization")
                   if keys.get(k) is not None}
    body = entry.get("body") or {}
    stream = body.get("stream") if isinstance(body, dict) else None
    return {
        "method": entry.get("method"), "path": entry.get("path"), "model": entry.get("model"),
        "headers": interesting, "stream": stream,
        "body_keys": sorted(body.keys())[:10] if isinstance(body, dict) else None,
    }


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    # 1) 探针路径: 让后端对 mock 渠道做一次三协议实测（这本身就是真实最小请求）
    offset = mark()
    status, resp = call("POST", "/api/v1/channel/fetch-model",
                        {"channel": {"name": "DS-TEST-mock", "base_url": MOCK_BASE.rstrip("/")},
                         "key": "mock-secret-not-a-real-key", "probe": True}, timeout=180)
    probe_entries = entries_since(offset)
    print("探针: HTTP %d, mock 收到 %d 条" % (status, len(probe_entries)))

    # 2) 转发路径: 非流式与流式各一次
    key = relay_api_key()
    relay_entries = []
    for stream in (False, True):
        offset = mark()
        req = urllib.request.Request(RELAY + "/v1/chat/completions",
                                     data=json.dumps({"model": "DS-TEST-mock/mock-good", "max_tokens": 8,
                                                      "stream": stream,
                                                      "messages": [{"role": "user", "content": "ping"}]}).encode(),
                                     method="POST")
        req.add_header("Content-Type", "application/json")
        req.add_header("Authorization", "Bearer " + key)
        try:
            with urllib.request.urlopen(req, timeout=60) as r:
                r.read()
            relay_status = 200
        except urllib.error.HTTPError as e:
            relay_status = e.code
        except Exception as e:  # noqa: BLE001
            relay_status = type(e).__name__
        time.sleep(0.5)
        got = entries_since(offset)
        relay_entries.append((stream, relay_status, got))
        print("转发 stream=%-5s HTTP %s, mock 收到 %d 条" % (stream, relay_status, len(got)))

    # 3) 逐条打印关键差异
    print("\n=== 探针发出的报文 ===")
    for entry in probe_entries[:3]:
        print("  ", json.dumps(summarize(entry), ensure_ascii=False)[:320])
    print("=== 转发发出的报文 ===")
    for stream, status, got in relay_entries:
        for entry in got[:2]:
            print("  ", json.dumps(summarize(entry), ensure_ascii=False)[:320])

    print("\n=== 结论线索 ===")
    probe_paths = {e.get("path") for e in probe_entries}
    relay_paths = {e.get("path") for _, _, got in relay_entries for e in got}
    probe_headers = {json.dumps(summarize(e)["headers"], sort_keys=True) for e in probe_entries}
    relay_headers = {json.dumps(summarize(e)["headers"], sort_keys=True) for _, _, got in relay_entries for e in got}
    print("  探针路径:", probe_paths, " 转发路径:", relay_paths)
    print("  探针 headers 组合数:", len(probe_headers), " 转发 headers 组合数:", len(relay_headers))
    print("  headers 是否一致:", probe_headers == relay_headers)


if __name__ == "__main__":
    main()
