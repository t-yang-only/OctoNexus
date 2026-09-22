"""补「上游核查」面板的三语文案（channel.upstream.*）。"""
import json
import os
import sys

ROOT = r"D:\奇怪的软件\octopus\web\src\locales"

TEXTS = {
    "zh_hans": {
        "run": "核查上游",
        "running": "核查中 {done}/{total}",
        "finished": "已核查 {total} 个渠道，其中 {problem} 个需留意",
        "probeFailed": "探测失败：{reason}",
        "diff": "上游列出 {upstream} 个，配置 {configured} 个，其中 {listed} 个在清单内",
        "notListed": "清单未列出",
        "more": "还有 {count} 个…",
        "allConsistent": "配置与上游清单一致",
    },
    "zh_hant": {
        "run": "核查上游",
        "running": "核查中 {done}/{total}",
        "finished": "已核查 {total} 個渠道，其中 {problem} 個需留意",
        "probeFailed": "探測失敗：{reason}",
        "diff": "上游列出 {upstream} 個，配置 {configured} 個，其中 {listed} 個在清單內",
        "notListed": "清單未列出",
        "more": "還有 {count} 個…",
        "allConsistent": "配置與上游清單一致",
    },
    "en": {
        "run": "Check upstream",
        "running": "Checking {done}/{total}",
        "finished": "Checked {total} channels, {problem} need attention",
        "probeFailed": "Probe failed: {reason}",
        "diff": "Upstream lists {upstream}, configured {configured}, {listed} of them are listed",
        "notListed": "not listed upstream",
        "more": "{count} more…",
        "allConsistent": "Config matches upstream list",
    },
}

for lang, items in TEXTS.items():
    path = os.path.join(ROOT, "%s.json" % lang)
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    section = data.setdefault("channel", {})
    section["upstream"] = items
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=4)
        fh.write("\n")
    print("%-8s channel.upstream 已写入 %d 个键" % (lang, len(items)))

sys.exit(0)
