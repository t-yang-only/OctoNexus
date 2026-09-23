"""查清「未分类」那一档到底是什么请求。

用法：python tools/probe_unclassified_logs.py [window]

背景：通过率窗口 108 里 成功 62 / 取消 8 / 请求非法 2 / 可恢复 2 / **未分类 34**。
未分类占 31%，是仅次于成功的一档 —— 不查清它，通过率就没法归因。
"""
import http.cookiejar
import json
import sys
import urllib.request
from collections import Counter

SITE = "https://apideta.yangonly.com"
JAR = http.cookiejar.CookieJar()
OPENER = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(JAR))


def call(path, body=None, method=None):
    # method 不能默认成 "GET"：强制 GET 时带 body 会被路由判成不匹配（登录是 POST，实测直接 404）。
    # 留 None，让 urllib 按「有无 data」自己决定 GET / POST。
    req = urllib.request.Request(SITE + path, method=method)
    req.add_header("Content-Type", "application/json")
    data = json.dumps(body).encode() if body is not None else None
    with OPENER.open(req, data=data, timeout=60) as resp:
        return json.loads(resp.read().decode("utf-8"))


def main():
    window = int(sys.argv[1]) if len(sys.argv) > 1 else 200
    call("/api/v1/user/login", body={"username": "yang", "password": "sois=ting"})

    stats = call("/api/v1/log/fault-stats?window=%d" % window)
    sd = stats.get("data") or stats
    print("=== fault-stats（window=%d）===" % window)
    print(json.dumps(sd, ensure_ascii=False, indent=2)[:2000])
    print()

    logs = call("/api/v1/log/list?page=1&page_size=%d" % min(window, 200))
    ld = logs.get("data") or logs
    items = ld.get("items") or ld.get("list") or ld.get("data") or []
    if not isinstance(items, list):
        print("日志列表结构不认识:", json.dumps(ld, ensure_ascii=False)[:600])
        return 1
    print("=== 日志 %d 条，逐条看 fault_kind ===" % len(items))
    if items:
        print("字段:", sorted(items[0].keys()))
    print()

    by_fault = Counter()
    unclassified = []
    for it in items:
        fk = it.get("fault_kind") or ""
        by_fault[fk or "<空>"] += 1
        if not fk:
            unclassified.append(it)

    print("fault_kind 分布:")
    for k, v in by_fault.most_common():
        print("   %-14s %d" % (k, v))
    print()

    print("=== 未分类样本（最多 15 条）===")
    for it in unclassified[:15]:
        print("id=%s status=%s stream=%s cancel=%s" % (
            it.get("id"), it.get("http_status") or it.get("status"),
            it.get("is_stream"), it.get("is_cancel")))
        print("   模型=%s 渠道=%s 分组=%s" % (
            it.get("model_name"), it.get("channel_name"), it.get("group_name")))
        print("   耗时=%sms 首字=%sms" % (it.get("use_time"), it.get("first_byte_time")))
        print("   decision=%s" % (it.get("decision") or "<空>"))
        err = it.get("error") or it.get("error_text") or ""
        print("   error=%s" % (err[:300] if err else "<空>"))
        print()

    return 0


if __name__ == "__main__":
    sys.exit(main())
