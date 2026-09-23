"""核对「未读到 N」与「按原因归类合计」是否相等（本地实例）。

用法：python tools/check_balance_reason_account.py

登录是**写 Cookie**（/api/v1/user/login 返回的是 "login successfully"，
不是 token），所以必须挂 cookie jar，否则后续请求一律 401。
"""
import http.cookiejar
import json
import sys
import urllib.request

BASE = "http://127.0.0.1:13303"
JAR = http.cookiejar.CookieJar()
OPENER = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(JAR))


def call(path, body=None):
    req = urllib.request.Request(BASE + path)
    req.add_header("Content-Type", "application/json")
    data = json.dumps(body).encode() if body is not None else None
    with OPENER.open(req, data=data, timeout=30) as resp:
        return json.loads(resp.read().decode("utf-8"))


def main():
    call("/api/v1/user/login", body={"username": "admin", "password": "admin"})
    if not list(JAR):
        print("登录后没有拿到 Cookie，后续请求会 401")
        return 1

    summary = call("/api/v1/balance/summary")
    data = summary.get("data") or summary

    unknown = data.get("unknown_channels", 0)
    counts = data.get("reason_counts") or {}
    total = sum(counts.values())

    print("未读到渠道 : %d" % unknown)
    print("归类明细   :")
    for key in sorted(counts):
        print("   %-14s %d" % (key or "<空>", counts[key]))
    print("归类合计   : %d" % total)
    print()

    rows = data.get("channels") or []
    unknown_rows = [r for r in rows if not r.get("known")]
    blank = [r for r in unknown_rows if not r.get("reason_code")]
    print("明细里未读到的行          : %d" % len(unknown_rows))
    print("其中没有原因码的行        : %d" % len(blank))
    for r in blank[:10]:
        print("   - id=%s name=%s" % (r.get("channel_id"), r.get("channel_name")))
    print()

    ok = True
    if total != unknown:
        print("FAIL 归类合计 %d != 未读到 %d" % (total, unknown))
        ok = False
    if blank:
        print("FAIL 有 %d 个未读到的渠道没有原因码" % len(blank))
        ok = False
    if "" in counts:
        print("FAIL 出现空原因码")
        ok = False

    print()
    print("PASS 账目自洽" if ok else "未通过")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
