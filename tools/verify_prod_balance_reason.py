"""生产端核对：① 账目自洽 ② 新前端产物上线。

用法：python tools/verify_prod_balance_reason.py
"""
import http.cookiejar
import json
import re
import sys
import urllib.request
import gzip
import io

SITE = "https://apideta.yangonly.com"
JAR = http.cookiejar.CookieJar()
OPENER = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(JAR))


def call(path, body=None, base=SITE):
    req = urllib.request.Request(base + path)
    req.add_header("Content-Type", "application/json")
    data = json.dumps(body).encode() if body is not None else None
    with OPENER.open(req, data=data, timeout=60) as resp:
        return json.loads(resp.read().decode("utf-8"))


def fetch_bytes(url):
    req = urllib.request.Request(url)
    with urllib.request.urlopen(req, timeout=60) as resp:
        raw = resp.read()
        if resp.headers.get("Content-Encoding") == "gzip" or url.endswith(".gz"):
            raw = gzip.decompress(raw)
        return raw


def main():
    ok = True

    # 1. 账目自洽
    call("/api/v1/user/login", body={"username": "yang", "password": "sois=ting"})
    summary = call("/api/v1/balance/summary")
    data = summary.get("data") or summary
    unknown = data.get("unknown_channels", 0)
    counts = data.get("reason_counts") or {}
    total = sum(counts.values())
    rows = data.get("channels") or []
    unknown_rows = [r for r in rows if not r.get("known")]
    blank = [r for r in unknown_rows if not r.get("reason_code")]

    print("未读到渠道        : %d" % unknown)
    print("归类明细          :")
    for key in sorted(counts):
        print("   %-14s %d" % (key or "<空>", counts[key]))
    print("归类合计          : %d" % total)
    print("明细未读到的行    : %d" % len(unknown_rows))
    print("其中无原因码的行  : %d" % len(blank))
    for r in blank[:10]:
        print("   - id=%s name=%s" % (r.get("channel_id"), r.get("channel_name")))

    if total != unknown:
        print("FAIL 归类合计 %d != 未读到 %d" % (total, unknown))
        ok = False
    if blank:
        print("FAIL 有 %d 个未读到的渠道没有原因码" % len(blank))
        ok = False
    if "" in counts:
        print("FAIL 出现空原因码")
        ok = False
    if total == unknown and not blank:
        print("OK   账目自洽")

    # 2. 新前端产物
    print()
    html = fetch_bytes(SITE + "/").decode("utf-8", "replace")
    hashes = re.findall(r"/assets/(index-[A-Za-z0-9_-]+\.js)", html)
    print("线上首页引用产物  : %s" % (sorted(set(hashes)) or "<未找到>"))
    hit = False
    for h in sorted(set(hashes)):
        try:
            js = fetch_bytes(SITE + "/assets/" + h).decode("utf-8", "replace")
        except Exception as exc:  # noqa: BLE001
            print("   读取 %s 失败: %s" % (h, exc))
            continue
        found = [kw for kw in ("no_record", "reasonTotal") if kw in js]
        print("   %s (%d 字节) 命中 %s" % (h, len(js), found))
        if len(found) == 2:
            hit = True
    if not hit:
        print("FAIL 线上产物里找不到本轮新增文案")
        ok = False
    else:
        print("OK   新前端已上线")

    print()
    print("PASS 全部通过" if ok else "未通过")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
