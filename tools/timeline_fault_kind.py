"""按时间线看 failed 行的 fault_kind 有没有落库 —— 判定「未分类」是存量还是真 bug。

用法：python tools/timeline_fault_kind.py [pages]

若新写入的 failed 行都有 fault_kind、只有早期某些行没有 → 存量数据，面板口径本身没问题。
若新写入的 failed 行也没有 → 写入路径有 bug，通过率的「未分类」会一直虚高。
"""
import http.cookiejar
import json
import sys
import urllib.request

SITE = "https://apideta.yangonly.com"
JAR = http.cookiejar.CookieJar()
OPENER = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(JAR))


def call(path, body=None):
    req = urllib.request.Request(SITE + path)
    req.add_header("Content-Type", "application/json")
    data = json.dumps(body).encode() if body is not None else None
    return json.loads(OPENER.open(req, data=data, timeout=60).read().decode())


def main():
    pages = int(sys.argv[1]) if len(sys.argv) > 1 else 3
    call("/api/v1/user/login", body={"username": "yang", "password": "sois=ting"})

    rows = []
    for page in range(1, pages + 1):
        hist = call("/api/v1/log/history?page=%d&page_size=50" % page)
        data = hist.get("data") or hist
        items = data.get("items") or []
        if not items:
            break
        rows.extend(items)
        print("第 %d 页取到 %d 条（total=%s）" % (page, len(items), data.get("total")))

    print("合计 %d 条" % len(rows))
    print()

    failed = [r for r in rows if (r.get("status") or "") == "failed"]
    print("status=failed: %d 条" % len(failed))
    print()
    print("%-6s %-32s %-10s %s" % ("id", "created_at", "fault_kind", "error 摘要"))
    print("-" * 118)
    for r in sorted(failed, key=lambda x: x.get("id") or 0):
        err = (r.get("error") or "")[:60].replace("\n", " ")
        print("%-6s %-32s %-10s %s" % (
            r.get("id"), (r.get("created_at") or "")[:26],
            (r.get("fault_kind") or "<空>"), err))

    print()
    filled = [r for r in failed if r.get("fault_kind")]
    blank = [r for r in failed if not r.get("fault_kind")]
    print("有 fault_kind: %d / 无: %d" % (len(filled), len(blank)))
    if blank:
        ids = sorted((r.get("id") or 0) for r in blank)
        print("无 fault_kind 的 id 区间: %d .. %d" % (min(ids), max(ids)))
    if filled:
        ids = sorted((r.get("id") or 0) for r in filled)
        print("有 fault_kind 的 id 区间: %d .. %d" % (min(ids), max(ids)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
