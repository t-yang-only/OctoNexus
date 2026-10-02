"""补「上游模型不一致」标记的三语文案。"""
import json
import os
# 仓库根：优先环境变量，否则按脚本位置反推（本脚本在 <仓库根>/<目录>/ 下）。
# 原先这里写死本机绝对路径，换目录或换机器就跑不起来。
_REPO = os.environ.get("OCTOPUS_ROOT") or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
import sys

ROOT = os.path.join(_REPO, "web", "src", "locales")

TEXTS = {
    "zh_hans": {
        "modelMismatch": "模型不一致",
        "modelMismatchTip": "上游回报它用的是 {reported}，与你请求的模型不同。"
                            "可能是别名或上游路由改名（正常），也可能是真的换了模型（需要处理）。",
    },
    "zh_hant": {
        "modelMismatch": "模型不一致",
        "modelMismatchTip": "上游回報它用的是 {reported}，與你請求的模型不同。"
                            "可能是別名或上游路由改名（正常），也可能是真的換了模型（需要處理）。",
    },
    "en": {
        "modelMismatch": "model mismatch",
        "modelMismatchTip": "The upstream reported it used {reported}, which differs from the model you requested. "
                            "This may be an alias or upstream routing (normal), or the model was actually swapped (needs attention).",
    },
}

for lang, items in TEXTS.items():
    path = os.path.join(ROOT, "%s.json" % lang)
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    section = data.get("log")
    if section is None:
        print("!! %s 缺少 log 段，跳过" % lang)
        sys.exit(1)
    for k, v in items.items():
        section[k] = v
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=4)
        fh.write("\n")
    print("%-8s log 段共 %d 个键" % (lang, len(section)))

sys.exit(0)
