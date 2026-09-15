"""T-proto-002 协议无损转化测试（NM-DS-005）。

口径：客户端三种协议 × 上游三种协议（用只带单个协议位的成员把上游协议钉死）= 9 例。
  · 上游收到的请求体（mock 落 requests.jsonl）必须**不丢字段**：system/instructions 标记、
    多轮顺序、max_tokens、temperature、tools 名称与 schema 全在；
  · 同协议那 3 例另做"逐字段相等"检查（转换层不得改写直通请求）；
  · 客户端拿到的响应必须是**客户端协议形状**，且携带来上游那份内容文本（响应侧也不丢内容）。
另加 1 例流式：Anthropic 入 / Chat 上游出，SSE 里要能看到上游增量文本。

Run: python run_lossless_tests.py
"""

import http.client
import json
import os
import sys
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")
CHANNEL = "DS-TEST-proto3"
MOCK_BASE = "http://127.0.0.1:18099"

MARK_SYS, MARK_U1, MARK_A1, MARK_U2 = "SYS-MARKER-1", "USER-MARKER-1", "ASSIST-MARKER-1", "USER-MARKER-2"
MAXTOK, TEMP, TOOL = 321, 0.25, "probe_tool"
SCHEMA = {"type": "object", "properties": {"x": {"type": "string"}}, "required": ["x"]}

UPSTREAM_TEXT = {"chat": "mock chat ok", "responses": "mock responses ok", "messages": "mock messages ok"}
PINS = [("chat", 2), ("responses", 4), ("messages", 8)]
CLIENTS = ["chat", "responses", "messages"]
GROUPS = {"chat": "DS-TEST-proto-chat", "responses": "DS-TEST-proto-resp", "messages": "DS-TEST-proto-msg"}
COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


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
        return e.code, {"_raw": e.read().decode()[:300]}
    except Exception as e:
        return 0, {"_raw": "%s: %s" % (type(e).__name__, e)}


def api_key():
    import sqlite3
    conn = sqlite3.connect(r"file:D:\奇怪的软件\octopus\data\data.db?mode=ro", uri=True)
    cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
    col = "api_key" if "api_key" in cols else "key"
    return conn.execute(f"select {col} from api_keys where enabled = 1 order by id limit 1").fetchone()[0]


def build_client_body(client, group, stream=False):
    """按客户端协议构造同一份语义载荷：system + 两轮 user + 一轮 assistant + 参数 + 一个工具。"""
    if client == "chat":
        return {"model": group, "stream": stream, "max_tokens": MAXTOK, "temperature": TEMP,
                "messages": [{"role": "system", "content": MARK_SYS},
                             {"role": "user", "content": MARK_U1},
                             {"role": "assistant", "content": MARK_A1},
                             {"role": "user", "content": MARK_U2}],
                "tools": [{"type": "function", "function": {"name": TOOL, "parameters": SCHEMA}}]}
    if client == "responses":
        return {"model": group, "stream": stream, "max_output_tokens": MAXTOK, "temperature": TEMP,
                "instructions": MARK_SYS,
                "input": [{"role": "user", "content": [{"type": "input_text", "text": MARK_U1}]},
                          {"role": "assistant", "content": [{"type": "output_text", "text": MARK_A1}]},
                          {"role": "user", "content": [{"type": "input_text", "text": MARK_U2}]}],
                "tools": [{"type": "function", "name": TOOL, "parameters": SCHEMA}]}
    return {"model": group, "stream": stream, "max_tokens": MAXTOK, "temperature": TEMP,
            "system": MARK_SYS,
            "messages": [{"role": "user", "content": [{"type": "text", "text": MARK_U1}]},
                         {"role": "assistant", "content": [{"type": "text", "text": MARK_A1}]},
                         {"role": "user", "content": [{"type": "text", "text": MARK_U2}]}],
            "tools": [{"name": TOOL, "input_schema": SCHEMA}]}


def send(client, group, key, stream=False, timeout=120):
    path = {"chat": "/v1/chat/completions", "responses": "/v1/responses", "messages": "/v1/messages"}[client]
    body = build_client_body(client, group, stream)
    headers = {"Content-Type": "application/json"}
    if client == "messages":
        headers["x-api-key"] = key
        headers["anthropic-version"] = "2023-06-01"
    else:
        headers["Authorization"] = "Bearer " + key
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", path, body=json.dumps(body), headers=headers)
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    conn.close()
    return resp.status, raw, body


def log_len():
    if not os.path.exists(MOCK_LOG):
        return 0
    with open(MOCK_LOG, "r", encoding="utf-8", errors="ignore") as fh:
        return len(fh.readlines())


def last_upstream_body(since):
    with open(MOCK_LOG, "r", encoding="utf-8", errors="ignore") as fh:
        lines = fh.readlines()[since:]
    for line in reversed(lines):
        try:
            entry = json.loads(line)
        except Exception:
            continue
        if entry.get("method") == "POST":
            return entry
    return {}


def shape_ok(client, raw):
    """客户端拿到的响应必须是该协议的形状。"""
    try:
        data = json.loads(raw)
    except Exception:
        return False, "响应不是 JSON"
    if client == "chat":
        ok = isinstance(data.get("choices"), list) and data["choices"]
        return ok, "choices[0].message.content=%r" % (data.get("choices", [{}])[0].get("message", {}).get("content") if ok else None)
    if client == "responses":
        ok = data.get("object") == "response" or isinstance(data.get("output"), list)
        return ok, "object=%r output_types=%r" % (data.get("object"), [o.get("type") for o in (data.get("output") or [])])
    ok = isinstance(data.get("content"), list) and data.get("type") == "message"
    return ok, "type=%r content_types=%r stop_reason=%r" % (data.get("type"), [c.get("type") for c in (data.get("content") or [])], data.get("stop_reason"))


def text_of(raw):
    try:
        data = json.loads(raw)
    except Exception:
        return ""
    if isinstance(data.get("choices"), list) and data["choices"]:
        return str(data["choices"][0].get("message", {}).get("content") or "")
    if isinstance(data.get("content"), list):
        return " ".join(str(c.get("text") or "") for c in data["content"])
    if isinstance(data.get("output"), list):
        out = []
        for item in data["output"]:
            for c in (item.get("content") or []):
                out.append(str(c.get("text") or ""))
        return " ".join(out)
    return ""


def ensure_fixtures():
    """钉死上游协议：同一模型配三把 key，各给一个协议位。"""
    # 幂等：已存在就复用（渠道被分组引用时删除会失败），不存在才新建。
    _, lst = call("GET", "/api/v1/channel/list")
    exists = any(c.get("name") == CHANNEL for c in (lst.get("data") or []))
    if not exists:
        # /channel/list 可能分页（渠道多时测试渠道可能不在首页），再用 stats 兜一次；
        # 漏判会走到 create 并撞 channels.name 唯一约束（实测 500），故这里必须兜住。
        _, stats = call("GET", "/api/v1/channel/stats")
        exists = any(c.get("channel_name") == CHANNEL for c in (stats.get("data") or []))
    if not exists:
        create_fixture_channel()
    import sqlite3
    conn = sqlite3.connect(r"file:D:\奇怪的软件\octopus\data\data.db?mode=ro", uri=True)
    rows = conn.execute("""
        select cg.id, ck.name from channel_grants cg
        join channel_models cm on cm.id = cg.channel_model_id
        join channel_keys ck on ck.id = cg.channel_key_id
        join channels ch on ch.id = cm.channel_id
        where ch.name = ? and cm.name = 'mock-good'""", (CHANNEL,)).fetchall()
    by_key = {name.replace("pin-", ""): gid for gid, name in rows}
    if len(by_key) != 3:
        print("fixture grants unexpected:", rows)
        return None
    for proto, gid in by_key.items():
        items = [{"channel_grant_id": gid}]
        _, glist = call("GET", "/api/v1/group/list")
        hit = next((g for g in (glist.get("data") or []) if g.get("name") == GROUPS[proto]), None)
        if hit:
            call("POST", f"/api/v1/group/update/{hit['id']}", {"mode": "failover", "items": items})
        else:
            call("POST", "/api/v1/group/create", {"name": GROUPS[proto], "mode": "failover", "items": items})
    print("fixtures ready:", by_key)
    return by_key


def create_fixture_channel():
    detail = {"id": 0, "name": CHANNEL, "base_url": MOCK_BASE, "dialect": "generic", "enabled": True,
              "keys": [{"name": "pin-chat", "key": "mock-any", "enabled": True},
                       {"name": "pin-responses", "key": "mock-any", "enabled": True},
                       {"name": "pin-messages", "key": "mock-any", "enabled": True}],
              "models": ["mock-good"],
              "grants": [{"model_name": "mock-good", "key_name": "pin-" + p, "protocols": bit} for p, bit in PINS]}
    st, res = call("POST", "/api/v1/channel/create", detail)
    if st != 200:
        print("fixture channel failed:", st, json.dumps(res, ensure_ascii=False)[:300])


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    if ensure_fixtures() is None:
        return 1
    key = api_key()
    time.sleep(1)

    for client in CLIENTS:
        for pin, _bit in PINS:
            group = GROUPS[pin]
            since = log_len()
            st, raw, sent = send(client, group, key)
            entry = last_upstream_body(since)
            body_text = json.dumps(entry.get("body") or {}, ensure_ascii=False)
            upstream_path = entry.get("path") or "?"

            missing = [m for m in (MARK_SYS, MARK_U1, MARK_A1, MARK_U2) if m not in body_text]
            idx = [body_text.find(m) for m in (MARK_U1, MARK_A1, MARK_U2)]
            ordered = -1 not in idx and idx == sorted(idx)
            # 参数逐项核验：既报出"是否保留"，也报出上游实际收到的值，便于定性。
            body = entry.get("body", {}) or {}
            # 各协议/各方向的 max tokens 字段名都算数：max_tokens / max_output_tokens / max_completion_tokens。
            got_tokens = body.get("max_output_tokens", body.get("max_completion_tokens", body.get("max_tokens")))
            got_temp = body.get("temperature")
            kept_tokens, kept_temp = got_tokens == MAXTOK, got_temp == TEMP
            params = kept_tokens and kept_temp
            tool = (TOOL in body_text) and ('"x"' in body_text)
            ok_shape, shape_note = shape_ok(client, raw)
            text = text_of(raw)
            upstream_ok = UPSTREAM_TEXT[pin] in text

            detail = (f"客户端={client} 上游路径={upstream_path} 缺失字段={missing} 顺序保留={ordered} "
                      f"max_tokens保留={kept_tokens}(实际{got_tokens}) 温度保留={kept_temp}(实际{got_temp}) "
                      f"工具保留={tool} 响应形状={shape_note} 上游正文透传={upstream_ok}")
            record(f"无损[{client} → {pin}]", st == 200 and not missing and ordered and params and tool and ok_shape and upstream_ok, detail)

            if client == pin:
                # 同协议直通：除 model（分组名 → 上游模型名，设计内映射）外，逐字段不得丢/改。
                got = entry.get("body") or {}
                diff = {k: (sent[k], got.get(k)) for k in sent if k != "model"
                        and json.dumps(sent[k], sort_keys=True) != json.dumps(got.get(k), sort_keys=True)}
                mapped = got.get("model") == "mock-good"
                record(f"直通逐字段相等[{client}]", not diff and mapped,
                       ("除 model 外逐字段一致，且 model 已映射为上游模型名 %r" % got.get("model")) if (not diff and mapped)
                       else ("差异=" + json.dumps({k: v for k, v in list(diff.items())[:3]}, ensure_ascii=False)[:200] + " model=%r" % got.get("model")))

    # 流式：Anthropic 入 → Chat 上游出，SSE 必须带上游增量文本
    since = log_len()
    st, raw, _ = send("messages", GROUPS["chat"], key, stream=True)
    entry = last_upstream_body(since)
    upstream_stream = bool((entry.get("body") or {}).get("stream"))
    has_text = ("chat ok" in raw) or ("mock chat" in raw)
    has_terminal = ("stop_reason" in raw) or ("message_stop" in raw) or ("end_turn" in raw)
    record("流式无损[messages → chat]", st == 200 and upstream_stream and has_text and has_terminal,
           f"HTTP {st} 上游 stream={upstream_stream} SSE含上游文本={has_text} 含终止事件={has_terminal} 片段={raw[:120]!r}")

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("LOSSLESS_TEST total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for n, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", n)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
