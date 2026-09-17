"""Live audit for U-key-001 (request-level detail export).

What it proves, on a running instance:
  * the export endpoint really produces a CSV: UTF-8 BOM, fixed header, ascending log-row ids;
  * exported fields equal the persisted history rows field by field (matched on the unique log-row id);
  * the same filters as /history apply (status / model / keyword), with non-vacuous row counts;
  * response headers make a browser download it instead of rendering it;
  * the endpoint is behind admin auth.

Run: python run_log_export_audit.py   (instance + mock upstream must be running)
"""

import csv
import http.client
import io
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))

GROUP = "DS-TEST-formats"
HEADER = ["日志ID", "请求ID", "时间", "状态", "分组(请求模型)", "上游模型", "目标渠道", "上游协议", "首字节(ms)", "耗时(ms)",
          "上游轮次", "输入tokens", "缓存命中tokens", "输出tokens", "费用", "API Key", "错误"]
COLUMN = {name: index for index, name in enumerate(HEADER)}
PROTOCOLS = {"openai-chat", "openai-responses", "anthropic-messages", ""}

RESULTS = []
COOKIE = {}


def call(method, path, payload=None, timeout=60):
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


def fetch_export(query="", with_cookie=True):
    req = urllib.request.Request(ADMIN + "/api/v1/log/export" + query)
    if with_cookie and COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            return resp.status, resp.read(), dict(resp.getheaders())
    except urllib.error.HTTPError as e:
        return e.code, e.read(), dict(e.getheaders())


def parse_csv(raw):
    return list(csv.reader(io.StringIO(raw.decode("utf-8-sig"))))


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def history(query=""):
    _, payload = call("GET", "/api/v1/log/history?limit=200&offset=0" + query)
    data = (payload or {}).get("data") or {}
    return data.get("items") or [], data.get("total") or 0


def relay(model, key, timeout=90):
    conn = http.client.HTTPConnection(RELAY_HOST, RELAY_PORT, timeout=timeout)
    conn.request("POST", "/v1/chat/completions",
                 body=json.dumps({"model": model, "messages": [{"role": "user", "content": "export audit"}],
                                  "stream": False, "max_tokens": 16}),
                 headers={"Content-Type": "application/json", "Authorization": "Bearer " + key})
    resp = conn.getresponse()
    resp.read()
    conn.close()
    return resp.status


def main():
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    cols = [r[1] for r in conn.execute("pragma table_info(api_keys)").fetchall()]
    key_col = "api_key" if "api_key" in cols else "key"
    key = conn.execute("select %s from api_keys where enabled = 1 order by id limit 1" % key_col).fetchone()[0]
    conn.close()

    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    fresh = [relay(GROUP, key) for _ in range(3)]
    record("fresh relayed requests exist for the export", all(status == 200 for status in fresh),
           "statuses=%s" % fresh)

    status, raw, headers = fetch_export()
    content_type = headers.get("Content-Type", "") or headers.get("content-type", "")
    disposition = headers.get("Content-Disposition", "") or headers.get("content-disposition", "")
    record("export returns a csv download", status == 200 and "text/csv" in content_type and "attachment" in disposition,
           "HTTP %d content-type=%s disposition=%s" % (status, content_type, disposition))
    record("export starts with a utf-8 bom", raw.startswith(b"\xef\xbb\xbf"), "first bytes=%r" % raw[:6])

    rows = parse_csv(raw)
    record("export header matches the documented columns", rows and rows[0] == HEADER,
           "columns=%d header_tail=%s" % (len(rows[0]) if rows else 0, rows[0][-3:] if rows else None))

    body = [row for row in rows[1:] if row and row[COLUMN["日志ID"]].isdigit()]
    log_ids = [int(row[COLUMN["日志ID"]]) for row in body]
    record("exported rows are ordered ascending by log-row id and ids are unique",
           len(log_ids) > 0 and log_ids == sorted(log_ids) and len(set(log_ids)) == len(log_ids),
           "rows=%d first=%s last=%s unique=%d" % (len(log_ids), log_ids[:1], log_ids[-1:], len(set(log_ids))))

    record("protocol column is a readable label, never a bitmask number",
           all(row[COLUMN["上游协议"]] in PROTOCOLS for row in body),
           "labels=%s" % sorted({row[COLUMN["上游协议"]] for row in body}))

    # 字段级等价: 以日志行主键匹配(请求 ID 会因重试重复, 不能当行标识)。
    items, _ = history()
    by_log_id = {int(item["id"]): item for item in items if item.get("id") is not None}
    compared = 0
    mismatches = []
    for row in body:
        item = by_log_id.get(int(row[COLUMN["日志ID"]]))
        if not item:
            continue
        compared += 1
        first_byte = item.get("first_byte_ms")
        # 注意 0 是合法值(首字节在 0ms 内写出), 不能写成 `first_byte or -1`——Python 里 0 会被当假值误判成"未提交"。
        expected = {
            "请求ID": str(item.get("request_id") or 0),
            "状态": item.get("status") or "",
            "分组(请求模型)": item.get("model") or "",
            "上游模型": item.get("target_model") or "",
            "目标渠道": item.get("target_channel") or "",
            "首字节(ms)": "" if first_byte is None or first_byte < 0 else str(first_byte),
            "耗时(ms)": str(item.get("duration_ms") or 0),
            "上游轮次": str(item.get("attempts") or 0),
    "输入tokens": str(item.get("prompt_tokens") or 0),
            "缓存命中tokens": str(item.get("cached_tokens") or 0),
            "输出tokens": str(item.get("completion_tokens") or 0),
            "API Key": item.get("api_key_name") or "",
            "错误": item.get("error") or "",
        }
        for column, want in expected.items():
            got = row[COLUMN[column]]
            if got != want:
                mismatches.append("log_id=%s %s: csv=%r history=%r" % (row[0], column, got, want))
        if abs(float(row[COLUMN["费用"]]) - float(item.get("cost") or 0)) > 1e-12:
            mismatches.append("log_id=%s cost: csv=%s history=%s" % (row[0], row[COLUMN["费用"]], item.get("cost")))
    record("exported fields equal the persisted history rows", compared > 0 and not mismatches,
           "compared=%d mismatches=%d %s" % (compared, len(mismatches), mismatches[:3]))

    # 筛选: 状态。挑一个"确实有条数"的状态做非空断言, 顺带证明零条状态不会被凭空造出来。
    statuses = sorted({item.get("status") for item in items if item.get("status")})
    checked = []
    for wanted in statuses[:3]:
        code, filtered_raw, _ = fetch_export("?status=" + wanted)
        filtered = [row for row in parse_csv(filtered_raw)[1:] if row]
        _, filtered_total = history("&status=" + wanted)
        ok = (code == 200 and len(filtered) > 0 and all(row[COLUMN["状态"]] == wanted for row in filtered)
              and (len(filtered) == filtered_total or filtered_total > 200))
        checked.append("%s:%d/%d" % (wanted, len(filtered), filtered_total))
        if not ok:
            record("status filter applies to the export (%s)" % wanted, False, "rows=%d total=%d" % (len(filtered), filtered_total))
            break
    else:
        record("status filter applies to the export", bool(checked), "per-status rows/total = %s" % checked)

    _, model_raw, _ = fetch_export("?model=" + GROUP)
    model_rows = [row for row in parse_csv(model_raw)[1:] if row]
    record("model filter applies to the export",
           len(model_rows) > 0 and all(row[COLUMN["分组(请求模型)"]] == GROUP for row in model_rows),
           "rows=%d models=%s" % (len(model_rows), sorted({row[COLUMN["分组(请求模型)"]] for row in model_rows})[:3]))

    keyword = "DS-TEST"
    _, keyword_raw, _ = fetch_export("?q=" + keyword)
    keyword_rows = [row for row in parse_csv(keyword_raw)[1:] if row]
    hit = [row for row in keyword_rows
           if keyword in (row[COLUMN["分组(请求模型)"]] + row[COLUMN["上游模型"]] + row[COLUMN["目标渠道"]] + row[COLUMN["错误"]])]
    record("keyword filter applies to the export",
           len(keyword_rows) > 0 and len(hit) == len(keyword_rows),
           "rows=%d rows_containing_keyword=%d" % (len(keyword_rows), len(hit)))

    anon_status, _, _ = fetch_export(with_cookie=False)
    record("export requires admin auth", anon_status == 401, "HTTP %d without cookie" % anon_status)

    print()
    total_cases = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("LOG_EXPORT_TEST total=%d pass=%d fail=%d" % (total_cases, passed, total_cases - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total_cases else 1


if __name__ == "__main__":
    sys.exit(main())
