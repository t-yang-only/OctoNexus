"""Multi-format calls against REAL upstreams from the imported backup.

The mock matrix proves protocol handling; this run proves the imported channels,
keys, groups and price table work end to end and that token/cost accounting is
right for priced models (mock models have no price, so their cost is 0).

Picks the first available group from a cheap-first preference list, then runs:
  OpenAI Chat non-stream, OpenAI Chat stream, Anthropic Messages non-stream.

Run: python run_real_tests.py
"""

import os
import http.client
import json
import sqlite3
import sys
import time
import urllib.request

RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
# 门禁: 这个套件会打真实上游、会产生费用, 必须显式开启才跑（默认跳过, 见 scripts/api-tests/README.md）。
if os.environ.get("OCTOPUS_ALLOW_REAL") != "1":
    print("SKIPPED: this suite calls real upstreams and costs money; set OCTOPUS_ALLOW_REAL=1 "
          "(or run run_all.py --with-real) to run it.")
    raise SystemExit(0)

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
DB = r"D:\奇怪的软件\octopus\data\data.db"

PREFERRED = ["deepseek-v4.1-flash", "deepseek-v4-flash", "glm-5.3-flash", "deepseek-flash",
             "gemini-3.5-flash", "claude-haiku-4-5", "gpt-5.6-sol", "deepseek-v4-pro"]
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, ok, detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


class Admin:
    def __init__(self):
        self.cookie = None
        self.call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    def call(self, method, path, payload=None):
        data = json.dumps(payload).encode() if payload is not None else None
        req = urllib.request.Request(ADMIN + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        if self.cookie:
            req.add_header("Cookie", self.cookie)
        with urllib.request.urlopen(req, timeout=90) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    self.cookie = v.split(";")[0]
            body = resp.read().decode()
            return json.loads(body) if body else {}

    def get(self, path):
        return self.call("GET", path).get("data")

    def stats(self):
        return self.get("/api/v1/stats/total") or {}


def relay_api_key():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    rows = conn.execute("select id, name, api_key, supported_models from api_keys where enabled=1").fetchall()
    conn.close()
    for _id, name, key, sup in rows:
        if not sup or sup in ("[]", "null", ""):
            return key, name, _id
    if rows:
        return rows[0][2], rows[0][1], rows[0][0]
    raise SystemExit("no enabled api key")


def call_relay(path, payload, api_key, stream=False, timeout=180):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    headers = {"Content-Type": "application/json", "Authorization": "Bearer " + api_key}
    t0 = time.time()
    conn.request("POST", path, body=json.dumps(payload), headers=headers)
    resp = conn.getresponse()
    chunks = []
    if stream:
        while True:
            line = resp.readline()
            if not line:
                break
            chunks.append(line)
            if len(chunks) > 20000:
                break
    else:
        chunks.append(resp.read())
    elapsed = time.time() - t0
    status = resp.status
    conn.close()
    return {"status": status, "elapsed": elapsed, "raw": b"".join(chunks).decode("utf-8", "replace")}


def parse_sse_text(raw):
    text, events = [], 0
    for line in raw.splitlines():
        line = line.strip()
        if not line.startswith("data:"):
            continue
        data = line[5:].strip()
        if not data or data == "[DONE]":
            continue
        events += 1
        try:
            obj = json.loads(data)
        except Exception:
            continue
        for choice in obj.get("choices") or []:
            text.append(((choice.get("delta") or {}).get("content")) or "")
    return "".join(text), events


def price_table():
    """name -> (input, output) 每百万 token 单价, 取自本地价格表。"""
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    rows = conn.execute("select name, input, output from llm_infos").fetchall()
    conn.close()
    return {r[0]: (r[1] or 0.0, r[2] or 0.0) for r in rows}


def pick_group(admin, prices):
    """优先挑便宜的 flash 分组, 且成员上游模型必须有非零价格 (否则成本恒 0, 验不了计价)。"""
    groups = admin.get("/api/v1/group/list") or []
    by_name = {g["name"]: g for g in groups}

    def priced(g):
        for i in (g.get("items") or []):
            model = (i.get("model_name") or "").lower()
            if i.get("available") and prices.get(model, (0, 0))[0] > 0:
                return model
        return None

    for name in PREFERRED:
        g = by_name.get(name)
        if not g:
            continue
        model = priced(g)
        if model:
            return name, g, model
    for g in groups:
        model = priced(g)
        if model:
            return g["name"], g, model
    raise SystemExit("no available group with a priced member")


def main():
    key, key_name, key_id = relay_api_key()
    admin = Admin()
    prices = price_table()
    group, g, priced_model = pick_group(admin, prices)
    price_in, price_out = prices[priced_model]
    members = [(i.get("channel_name"), i.get("model_name")) for i in (g.get("items") or [])]
    print(f"relay key id={key_id} name={key_name} | group={group} mode={g.get('mode')}")
    print(f"priced member model={priced_model} price_in={price_in}/M price_out={price_out}/M members={members[:3]}")
    s0 = admin.stats()

    res = call_relay("/v1/chat/completions",
                     {"model": group, "messages": [{"role": "user", "content": "只回答两个字：你好"}],
                      "stream": False, "max_tokens": 64}, key)
    ok = res["status"] == 200
    body = {}
    if ok:
        try:
            body = json.loads(res["raw"])
        except Exception:
            ok = False
    content = ((body.get("choices") or [{}])[0].get("message") or {}).get("content") or ""
    usage = body.get("usage") or {}
    record("real upstream OpenAI Chat non-stream", ok and bool(content.strip()) and usage.get("total_tokens", 0) > 0,
           f"HTTP {res['status']} {res['elapsed']:.2f}s tokens={usage.get('prompt_tokens')}/{usage.get('completion_tokens')} "
           f"text={content.strip()[:40]!r}")

    res_s = call_relay("/v1/chat/completions",
                       {"model": group, "messages": [{"role": "user", "content": "只回答两个字：你好"}],
                        "stream": True, "max_tokens": 64}, key, stream=True)
    text, events = parse_sse_text(res_s["raw"])
    record("real upstream OpenAI Chat stream", res_s["status"] == 200 and events > 0 and "[DONE]" in res_s["raw"],
           f"HTTP {res_s['status']} {res_s['elapsed']:.2f}s sse_events={events} text={text.strip()[:40]!r}")

    res_m = call_relay("/v1/messages",
                       {"model": group, "max_tokens": 64,
                        "messages": [{"role": "user", "content": "只回答两个字：你好"}]}, key)
    ok_m = res_m["status"] == 200
    text_m, usage_m = "", {}
    if ok_m:
        try:
            obj = json.loads(res_m["raw"])
            usage_m = obj.get("usage") or {}
            text_m = "".join(part.get("text", "") for part in (obj.get("content") or []) if isinstance(part, dict))
        except Exception:
            ok_m = False
    record("real upstream Anthropic Messages (protocol bridge)",
           ok_m and bool(text_m.strip()),
           f"HTTP {res_m['status']} {res_m['elapsed']:.2f}s usage={usage_m} text={text_m.strip()[:40]!r}")

    time.sleep(3)
    s1 = admin.stats()
    d_succ = (s1.get("request_success", 0) or 0) - (s0.get("request_success", 0) or 0)
    d_in = (s1.get("input_token", 0) or 0) - (s0.get("input_token", 0) or 0)
    d_out = (s1.get("output_token", 0) or 0) - (s0.get("output_token", 0) or 0)
    d_cost = ((s1.get("input_cost", 0) or 0) + (s1.get("output_cost", 0) or 0)) - \
             ((s0.get("input_cost", 0) or 0) + (s0.get("output_cost", 0) or 0))
    record("real calls update tokens and cost", d_succ == 3 and d_in > 0 and d_out > 0 and d_cost > 0,
           f"success_delta={d_succ} input_delta={d_in} output_delta={d_out} cost_delta={d_cost:.8f}")

    hist = admin.get("/api/v1/log/history?limit=5&offset=0") or {}
    rows = [r for r in (hist.get("items") or []) if r.get("model") == group]
    record("real calls land in the log with cost", bool(rows) and any((r.get("cost") or 0) > 0 for r in rows),
           f"rows={[(r.get('status'), r.get('target_channel'), r.get('prompt_tokens'), r.get('completion_tokens'), round(r.get('cost') or 0, 8)) for r in rows[:3]]}")

    # 逐行核对成本 = (prompt_tokens*in + completion_tokens*out)/1e6
    checks = []
    for r in rows[:3]:
        expected = ((r.get("prompt_tokens") or 0) * price_in + (r.get("completion_tokens") or 0) * price_out) / 1_000_000
        got = r.get("cost") or 0
        checks.append((round(expected, 10), round(got, 10), abs(expected - got) < 1e-9))
    record("cost matches price table per row", bool(checks) and all(c[2] for c in checks),
           f"{[(c[0], c[1]) for c in checks]} (expected vs recorded, tol 1e-9)")

    failed = [n for n, ok, _ in RESULTS if not ok]
    print(f"\nREAL_TESTS total={len(RESULTS)} pass={len(RESULTS) - len(failed)} fail={len(failed)}")
    if failed:
        print("FAILED:", failed)
    print("REAL_TESTS_EXIT=" + ("0" if not failed else "1"))
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
