"""把 fault_kind 为空的记录逐条摊开，看它们到底是什么。

用法：python tools/dump_unclassified.py [page_size]

判据：一条记录在什么情况下会落到「未分类」—— 不查清楚，通过率就没法归因。
"""
import http.cookiejar
import json
import sys
import urllib.request
from collections import Counter

SITE = "https://apideta.yangonly.com"
JAR = http.cookiejar.CookieJar()
OPENER = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(JAR))


def call(path, body=None):
    req = urllib.request.Request(SITE + path)
    req.add_header("Content-Type", "application/json")
    data = json.dumps(body).encode() if body is not None else None
    return json.loads(OPENER.open(req, data=data, timeout=60).read().decode())


def main():
    size = int(sys.argv[1]) if len(sys.argv) > 1 else 200
    call("/api/v1/user/login", body={"username": "yang", "password": "sois=ting"})
    hist = call("/api/v1/log/history?page=1&page_size=%d" % size)
    items = (hist.get("data") or hist).get("items") or []
    print("取到 %d 条（total=%s）" % (len(items), (hist.get("data") or hist).get("total")))

    blank = [it for it in items if not (it.get("fault_kind") or "")]
    print("其中 fault_kind 为空: %d 条" % len(blank))
    print()

    by_channel = Counter(it.get("target_channel") or "<空>" for it in blank)
    by_status = Counter(str(it.get("status")) for it in blank)
    by_model = Counter(it.get("target_model") or it.get("model") or "<空>" for it in blank)
    print("按渠道:", dict(by_channel))
    print("按状态:", dict(by_status))
    print("按模型:", dict(by_model.most_common(10)))
    print()
    print("=== 全部样本 ===")
    for it in blank:
        print("id=%s status=%s created=%s" % (it.get("id"), it.get("status"), it.get("created_at")))
        print("   ch=%s model=%s 上报=%s 协议=%s" % (
            it.get("target_channel"), it.get("target_model"),
            it.get("reported_model"), it.get("target_protocol")))
        print("   耗时=%sms 首字=%sms attempts=%s decision=%s" % (
            it.get("duration_ms"), it.get("first_byte_ms"), it.get("attempts"), it.get("decision")))
        err = it.get("error") or ""
        print("   error=%s" % (err[:400] if err else "<空>"))
        print()


if __name__ == "__main__":
    sys.exit(main())
