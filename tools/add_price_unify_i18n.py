# -*- coding: utf-8 -*-
"""给三语 locale 的 price 段插入「统一标准」相关文案（需求9）。

规则同前：文本插入、保留 CRLF、每行带逗号、落盘前 json.loads 校验。
锚点取 colAnomaly（price 段内最后一个既有键），插到它后面。
"""
import os
# 仓库根：优先环境变量，否则按脚本位置反推（本脚本在 <仓库根>/<目录>/ 下）。
# 原先这里写死本机绝对路径，换目录或换机器就跑不起来。
_REPO = os.environ.get("OCTOPUS_ROOT") or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

import json
from pathlib import Path

LOCALES = Path(os.path.join(_REPO, "web", "src", "locales"))
ANCHOR_KEY = "colAnomaly"

ENTRIES = {
    "zh_hans.json": [
        ("colBilling", "计费方式"),
        ("billingPerCall", "按次"),
        ("billingMetered", "按量"),
        ("billingUnknown", "未知"),
        ("colCnyInput", "实付输入 ¥/1M"),
        ("colCnyOutput", "实付输出 ¥/1M"),
        ("colPerCall", "按次单价"),
        ("unifyHint", "人民币价按设置里的充值比例折算；未配置时显示「—」，只对比站点原币种价。按次计费的模型会按本实例平均每次请求的 token 数折算成每 1M token 价。"),
    ],
    "zh_hant.json": [
        ("colBilling", "計費方式"),
        ("billingPerCall", "按次"),
        ("billingMetered", "按量"),
        ("billingUnknown", "未知"),
        ("colCnyInput", "實付輸入 ¥/1M"),
        ("colCnyOutput", "實付輸出 ¥/1M"),
        ("colPerCall", "按次單價"),
        ("unifyHint", "人民幣價按設定裡的充值比例折算；未設定時顯示「—」，只對比站點原幣種價。按次計費的模型會按本執行個體平均每次請求的 token 數折算成每 1M token 價。"),
    ],
    "en.json": [
        ("colBilling", "Billing"),
        ("billingPerCall", "per call"),
        ("billingMetered", "metered"),
        ("billingUnknown", "unknown"),
        ("colCnyInput", "Input ¥/1M"),
        ("colCnyOutput", "Output ¥/1M"),
        ("colPerCall", "Per call"),
        ("unifyHint", "CNY prices use the recharge ratio from settings; when unset they show an em dash and only the site's own currency is compared. Per-call models are converted to a per-1M-token price using this instance's average tokens per request."),
    ],
}


def insert(name, entries):
    path = LOCALES / name
    raw = path.read_text(encoding="utf-8", newline="")
    eol = "\r\n" if "\r\n" in raw[:2000] else "\n"
    lines = raw.splitlines(keepends=True)

    def bare(index):
        return lines[index].rstrip("\r\n")

    anchor = None
    for i, line in enumerate(lines):
        if line.lstrip().startswith('"%s":' % ANCHOR_KEY):
            anchor = i
            break
    if anchor is None:
        raise SystemExit(f"{name}: 找不到锚点 {ANCHOR_KEY}")

    indent = lines[anchor][: len(lines[anchor]) - len(lines[anchor].lstrip())]
    # 每一行都带逗号：插在兄弟键之间，最后一行后面还有 price 段的其他键。
    added = [f'{indent}"{key}": {json.dumps(value, ensure_ascii=False)},{eol}' for key, value in entries]
    lines[anchor + 1 : anchor + 1] = added
    body = "".join(lines)

    parsed = json.loads(body)
    block = parsed.get("price", {})
    want = {k for k, _ in entries}
    if not want.issubset(set(block)):
        raise SystemExit(f"{name}: price 缺键 {want - set(block)}")
    for sibling in ("tabModels", "tabUsage", "colSite", "colAnomaly"):
        if sibling not in block:
            raise SystemExit(f"{name}: price.{sibling} 消失")

    path.write_text(body, encoding="utf-8", newline="")
    print(f"{name}: +{len(added)} lines, keys ok")


for locale, entries in ENTRIES.items():
    insert(locale, entries)

print("done")
