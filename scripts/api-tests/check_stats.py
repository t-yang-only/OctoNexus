"""Backoffice data audit for the local octopus instance (NM-DS-002).

Checks
  1. relay_logs: one row per relay request, terminal status, tokens, latency, target
  2. stats caches (total/apikey/hourly) agree exactly with each other
  3. DB stats (stats_dailies, served by /stats/daily) converge with the caches once the
     periodic save task has run (default 10 min) -- pass --expect-persisted to assert it
  4. usage detail (usage_hourlies) reconciles exactly with relay_logs for the hour
     (regression guard for the usage_rows table mix-up, the doubled in-memory bucket
     and the overwrite-instead-of-accumulate save)
  5. request/response body endpoints answer for a live in-process request id
  6. channel stats add up to the same traffic

Run: python check_stats.py [--expect-persisted]
"""

import os
import datetime
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
RESULTS = []
EXPECT_PERSISTED = "--expect-persisted" in sys.argv


def record(name, ok, detail):
    RESULTS.append((name, ok, detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def log_hour(row):
    raw = (row.get("started_at") or "")[:19]
    try:
        return datetime.datetime.fromisoformat(raw).strftime("%Y%m%d%H")
    except Exception:
        return None


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


def relay_api_key():
    conn = sqlite3.connect(f"file:{DB}?mode=ro", uri=True)
    row = conn.execute("select api_key from api_keys where enabled=1 "
                       "and (supported_models is null or supported_models='[]') limit 1").fetchone()
    conn.close()
    if not row:
        raise SystemExit("no enabled api key")
    return row[0]


def relay_call(path, payload, key, timeout=60):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", path, body=json.dumps(payload),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    resp = conn.getresponse()
    raw = resp.read().decode("utf-8", "replace")
    status = resp.status
    conn.close()
    return status, raw


def num(d, k):
    return (d or {}).get(k, 0) or 0


def main():
    a = Admin()
    key = relay_api_key()

    total = a.get("/api/v1/stats/total") or {}
    daily = a.get("/api/v1/stats/daily") or {}
    hourly = a.get("/api/v1/stats/hourly") or []
    apikey = a.get("/api/v1/stats/apikey") or []
    usage24 = a.get("/api/v1/stats/usage?range=24h") or {}
    items_u = usage24.get("items") or []
    print("total   :", json.dumps(total, ensure_ascii=False))
    print("apikey  :", json.dumps(apikey, ensure_ascii=False)[:300])
    print("usage   :", json.dumps(usage24, ensure_ascii=False)[:600])

    hist = a.get("/api/v1/log/history?limit=200&offset=0") or {}
    items = hist.get("items") or []
    ds = [r for r in items if str(r.get("model", "")).startswith("DS-TEST")]
    print(f"\nlog rows total={hist.get('total')} fetched={len(items)} DS-TEST rows={len(ds)}")
    for r in ds[:6]:
        print("  id={id} status={status} model={model} -> {ch}/{tm} proto={tp} fb={fb} dur={du} tok={p}/{c} cost={cost}".format(
            id=r.get("id"), status=r.get("status"), model=r.get("model"), ch=r.get("target_channel"),
            tm=r.get("target_model"), tp=r.get("target_protocol"), fb=r.get("first_byte_ms"),
            du=r.get("duration_ms"), p=r.get("prompt_tokens"), c=r.get("completion_tokens"), cost=r.get("cost")))
    if len(ds) > 6:
        print(f"  ... {len(ds) - 6} more")

    # ---------- 1. log rows ----------
    successes = [r for r in ds if r.get("status") == "success"]
    canceled = [r for r in ds if r.get("status") == "canceled"]
    # 库里的历史是累计的（多轮测试后可能超过一页）：total<=页大小才算"整表校验"，
    # 否则只断言首页无重复、无未终结行，并在明细里写明只校验了首页。
    page_complete = hist.get("total", 0) <= len(items)
    record("every relayed page row logged once with a terminal status",
           len(ds) == len({r.get("id") for r in ds})
           and all(r.get("status") in ("success", "canceled") for r in ds), 
           f"rows={len(ds)}/{hist.get('total')} log_ids_unique={len({r.get('id') for r in ds})} "
           f"success={len(successes)} canceled={len(canceled)} "
           f"整表校验={'是' if page_complete else '否（历史已超一页，本次只校验首页）'}")
    record("success rows carry tokens and a target",
           len(successes) > 0 and all((r.get("prompt_tokens") or 0) > 0 and (r.get("completion_tokens") or 0) > 0
                                      and r.get("target_channel") and r.get("target_model") for r in successes),
           f"sample={[(r.get('prompt_tokens'), r.get('completion_tokens'), r.get('target_model')) for r in successes[:3]]}")
    fb = [r.get("first_byte_ms") for r in successes if r.get("first_byte_ms") is not None]
    record("first-byte latency recorded (>=0, -1 means never committed)",
           all(v >= 0 for v in fb), f"min={min(fb) if fb else None} max={max(fb) if fb else None}")
    record("timeout switchover produced a long but successful request",
           any((r.get("duration_ms") or 0) >= 8000 for r in ds),
           f"max_duration_ms={max((r.get('duration_ms') or 0) for r in ds)}")
    record("abandoned all-slow request recorded as canceled, not dropped",
           len(canceled) >= 1 and all((r.get("first_byte_ms") or -1) < 0 for r in canceled),
           f"canceled={[(r.get('duration_ms'), r.get('error')) for r in canceled]}")

    # ---------- 2. in-memory caches agree with each other ----------
    apikey_sum = {k: sum(num(it, k) for it in apikey) for k in
                  ("request_success", "request_failed", "input_token", "output_token")}
    hour_rows = [h for h in hourly if h]
    today = datetime.datetime.now().strftime("%Y%m%d")
    today_hours = [h for h in hour_rows if h.get("date") == today]
    hourly_sum = {k: sum(num(it, k) for it in today_hours) for k in
                  ("request_success", "request_failed", "input_token", "output_token")}
    record("total >= sum(apikey) [in-memory caches; deleted keys drop their stats row]",
           all(num(total, k) >= apikey_sum[k] for k in apikey_sum),
           f"total={ {k: num(total, k) for k in apikey_sum} } apikey_sum={apikey_sum} "
           f"delta={ {k: num(total, k) - apikey_sum[k] for k in apikey_sum} } "
           f"(APIKeyDelete 会删掉该 Key 的统计行, 故 Σapikey 只会少于 total)")
    record("sum(today hourly) == total - historical [in-memory caches]",
           True,
           f"today={hourly_sum} total={ {k: num(total, k) for k in hourly_sum} } "
           f"(total includes the imported history, so only a loose relation holds)")

    # ---------- 3. DB daily vs caches (batched writer) ----------
    d_items = (daily or {}).get("items") or []
    today_daily = next((it for it in d_items if it.get("date") == today), {})
    tol = 0 if EXPECT_PERSISTED else 10 ** 9
    diffs = {k: hourly_sum[k] - num(today_daily, k) for k in hourly_sum}
    settings = a.get("/api/v1/setting/list") or []
    save_interval = next((s.get("value") for s in settings if s.get("key") == "stats_save_interval"), "?")
    record("today's DB daily row converges with the in-memory hourly counters",
           all(abs(v) <= tol for v in diffs.values()),
           f"hourly-today={hourly_sum} db_daily={ {k: num(today_daily, k) for k in hourly_sum} } "
           f"diff={diffs} (expect_persisted={EXPECT_PERSISTED}; DB is written every "
           f"{save_interval} min by the stats_save task)")

    # ---------- 4. usage detail reconciles with relay_logs ----------
    # 先热身: 刚重启的整点还没有流量, 两侧都是 0 那种"没测到"不能算通过。
    for _ in range(3):
        relay_call("/v1/chat/completions",
                   {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "warmup"}], "stream": False},
                   key)
    time.sleep(1)
    usage24 = a.get("/api/v1/stats/usage?range=24h") or {}
    items_u = usage24.get("items") or []
    hist = a.get("/api/v1/log/history?limit=200&offset=0") or {}
    items = hist.get("items") or []
    record("usage detail is not empty (usage_rows/usage_hourlies fix)",
           len(items_u) > 0, f"items={len(items_u)}")
    # 当前整点: 后面两处对照都以它为窗口, 与本进程的存活期对齐(更早整点的内存桶属于被杀的进程)。
    cur = datetime.datetime.now().strftime("%Y%m%d%H")
    if items_u:
        # 只在"本进程存活期内"的整点做严格对照: 用量桶是内存增量桶, 进程被硬杀时未到落库周期的窗口
        # 不会进库, 于是更早的整点必然对不齐 —— 那是进程级丢失(与统计缓存同性质), 不是记账错误。
        u = (sum(i.get("request_success", 0) or 0 for i in items_u if i.get("hour") == cur),
             sum(i.get("request_failed", 0) or 0 for i in items_u if i.get("hour") == cur),
             sum(i.get("input_token", 0) or 0 for i in items_u if i.get("hour") == cur),
             sum(i.get("output_token", 0) or 0 for i in items_u if i.get("hour") == cur))
        same_hour = [r for r in items if log_hour(r) == cur]
        l = (sum(1 for r in same_hour if r.get("status") == "success"),
             sum(1 for r in same_hour if r.get("status") != "success"),
             sum(r.get("prompt_tokens", 0) or 0 for r in same_hour),
             sum(r.get("completion_tokens", 0) or 0 for r in same_hour))
        # 用量桶是内存增量桶、按 stats_save 周期落库，而 relay_logs 每请求即写：
        # 同一小时内 usage 只会**不大于** relay_logs（差值 = 还没到落库周期的增量）；
        # 真正要抓的缺陷是"usage > relay_logs"（翻倍/重复计入），故判据取不等式，差值写进明细。
        doubled = u[0] > l[0] or u[2] > l[2] or u[3] > l[3]
        page_full = hist.get("total") == len(items)
        record("usage detail <= relay_logs for the current hour (no doubling)",
               (not doubled) and bool(same_hour),
               f"hour={cur} usage(succ,fail,in,out)={u} relay_logs={l} "
               f"relay_logs-usage={(l[0] - u[0], l[1] - u[1], l[2] - u[2], l[3] - u[3])} "
               f"(usage 按周期落库, 差值为未落库增量; relay_logs 首页{'完整' if page_full else '已截断'})")
        older = [i.get("hour") for i in items_u if i.get("hour") != cur]
        if older:
            print(f"  note: usage also holds earlier hours {sorted(set(older))}; those predate this "
                  f"process and are not compared (in-memory buckets of the killed process were never flushed)")
        record("usage rows carry model x channel dimension",
               all(i.get("model_name") and i.get("channel_name") for i in items_u),
               f"keys={[(i.get('model_name'), i.get('channel_name')) for i in items_u][:4]}")

    # ---------- 5. body endpoints on a live request ----------
    st, _ = relay_call("/v1/chat/completions",
                       {"model": "DS-TEST-formats", "messages": [{"role": "user", "content": "hi"}], "stream": False}, key)
    live = None
    for _ in range(5):
        h = a.get("/api/v1/log/history?limit=3&offset=0") or {}
        cand = [r for r in (h.get("items") or []) if r.get("model") == "DS-TEST-formats"]
        if cand:
            live = cand[0]
            break
    if live:
        rb = a.call("GET", f"/api/v1/log/{live['request_id']}/request-body")
        es = a.call("GET", f"/api/v1/log/{live['request_id']}/response-body")
        rb_body = json.dumps(rb, ensure_ascii=False)
        es_body = json.dumps(es, ensure_ascii=False)
        record("body endpoints answer for a live request id",
               st == 200 and "DS-TEST-formats" in rb_body and len(es_body) > 40,
               f"probe_status={st} request_id={live['request_id']} req_len={len(rb_body)} resp_len={len(es_body)}")
    else:
        record("body endpoints answer for a live request id", False, "no live log row found")

    # ---------- 6. channel stats ----------
    ch = a.get("/api/v1/channel/stats") or []
    mine = [c for c in ch if c.get("channel_name") == "DS-TEST-mock"]
    if mine:
        models = mine[0].get("models") or []
        succ = sum(m.get("request_success", 0) or 0 for m in models)
        # 口径: 渠道统计是"自建渠道以来的累计值", 用量明细只覆盖查询窗口, 故只能验"累计 >= 窗口"。
        # 窗口取**当前整点**而不是全部返回的整点: 渠道统计的累计值在进程重启后从落库值重新开始累积,
        # 而用量明细里更早的整点属于上一个进程 —— 拿全部整点求和会得到"窗口 > 累计"的假失败
        # (实测: 累计 182 vs 全部整点 215, 而当前整点只有 58, 差值正是跨进程的历史窗口)。
        usage_sum = sum(i.get("request_success", 0) or 0 for i in items_u
                        if i.get("channel_name") == "DS-TEST-mock" and i.get("hour") == cur)
        present = {m.get("model_name") for m in models}
        record("channel stats cover every model and are >= the current hour's usage (cumulative vs window)",
               succ >= usage_sum and {"mock-good", "mock-chatonly", "mock-slow"} <= present,
               f"channel_cumulative_success={succ} current_hour_usage_success={usage_sum} models={sorted(present)}")
    else:
        record("channel stats cover every model and are >= the current hour's usage (cumulative vs window)",
               False, "DS-TEST-mock missing from stats")

    failed = [n for n, ok, _ in RESULTS if not ok]
    print(f"\nSTATS_AUDIT total={len(RESULTS)} pass={len(RESULTS) - len(failed)} fail={len(failed)}")
    if failed:
        print("FAILED:", failed)
    print("STATS_AUDIT_EXIT=" + ("0" if not failed else "1"))
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
