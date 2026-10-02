"""补「渠道可用性诊断」横幅的三语文案（channel.diagnose.*）。"""
import json
import os
# 仓库根：优先环境变量，否则按脚本位置反推（本脚本在 <仓库根>/<目录>/ 下）。
# 原先这里写死本机绝对路径，换目录或换机器就跑不起来。
_REPO = os.environ.get("OCTOPUS_ROOT") or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
import sys

ROOT = os.path.join(_REPO, "web", "src", "locales")

TEXTS = {
    "zh_hans": {
        "title": "可用性诊断",
        "summary": "全部渠道可用 {usable}/{total} 个模型（{rate}%）",
        "expand": "展开 {count} 个待处理",
        "collapse": "收起",
        "channelGap": "可用 {usable}/{total}",
        "more": "还有 {count} 个…",
    },
    "zh_hant": {
        "title": "可用性診斷",
        "summary": "全部渠道可用 {usable}/{total} 個模型（{rate}%）",
        "expand": "展開 {count} 個待處理",
        "collapse": "收起",
        "channelGap": "可用 {usable}/{total}",
        "more": "還有 {count} 個…",
    },
    "en": {
        "title": "Availability",
        "summary": "{usable}/{total} models usable across all channels ({rate}%)",
        "expand": "Show {count} to fix",
        "collapse": "Hide",
        "channelGap": "{usable}/{total} usable",
        "more": "{count} more…",
    },
}

for lang, items in TEXTS.items():
    path = os.path.join(ROOT, "%s.json" % lang)
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    section = data.setdefault("channel", {})
    section["diagnose"] = items
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=4)
        fh.write("\n")
    print("%-8s channel.diagnose 已写入 %d 个键" % (lang, len(items)))

sys.exit(0)
