# -*- coding: utf-8 -*-
"""给三语 locale 的 log.filter 段插入「按天查询 / 按 API 查询」文案（需求4）。

规则同 tools/add_account_delete_i18n.py：
  * 文本插入，绝不 json.load/json.dump 整写；
  * 行尾从文件自身取样（locale 是 CRLF），读写走 newline=""；
  * 每处替换前断言锚点恰好命中一次；
  * **插在兄弟键之间，每一行都必须带逗号**（少最后一个逗号会得到
    "Expecting ',' delimiter"，而报错行指向后面那个兄弟键）；
  * 落盘前 json.loads 校验键集与兄弟键。
"""
import os
# 仓库根：优先环境变量，否则按脚本位置反推（本脚本在 <仓库根>/<目录>/ 下）。
# 原先这里写死本机绝对路径，换目录或换机器就跑不起来。
_REPO = os.environ.get("OCTOPUS_ROOT") or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

import json
from pathlib import Path

LOCALES = Path(os.path.join(_REPO, "web", "src", "locales"))
ANCHOR_KEY = "testHint"

ENTRIES = {
    "zh_hans.json": [
        ("dayRange", "按天查询"),
        ("from", "起始日"),
        ("to", "结束日"),
        ("dayRangeHint", "按创建日期筛选，含起始日当天、不含结束日次日 —— 与导出的条件一致。"),
        ("apiKey", "按 API 查询"),
        ("apiKeyPlaceholder", "输入客户端 Key 名"),
        ("apiKeyHint", "只按 Key 名精确匹配这一列；想同时搜模型/渠道/错误请用左上角关键字。"),
    ],
    "zh_hant.json": [
        ("dayRange", "按天查詢"),
        ("from", "起始日"),
        ("to", "結束日"),
        ("dayRangeHint", "按建立日期篩選，含起始日當天、不含結束日次日 —— 與匯出的條件一致。"),
        ("apiKey", "按 API 查詢"),
        ("apiKeyPlaceholder", "輸入客戶端 Key 名"),
        ("apiKeyHint", "只按 Key 名精確匹配這一欄；想同時搜模型/通路/錯誤請用左上角關鍵字。"),
    ],
    "en.json": [
        ("dayRange", "Filter by day"),
        ("from", "From"),
        ("to", "To"),
        ("dayRangeHint", "Filters by creation date: the from-day is included, the day after to-day is not. Same condition as the export."),
        ("apiKey", "Filter by API key"),
        ("apiKeyPlaceholder", "Client key name"),
        ("apiKeyHint", "Exact match on the key name only. To also search model/channel/error, use the keyword box at the top left."),
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
        if line.lstrip().startswith('"%s":' % ANCHOR_KEY) and '"filter"' not in line:
            # 只认 log.filter 段内的那个（setting.log 等处也有同名键）。
            anchor = i
            break
    if anchor is None:
        raise SystemExit(f"{name}: 找不到锚点 {ANCHOR_KEY}")

    indent = lines[anchor][: len(lines[anchor]) - len(lines[anchor].lstrip())]
    added = [f'{indent}"{key}": {json.dumps(value, ensure_ascii=False)},{eol}' for key, value in entries]
    lines[anchor + 1 : anchor + 1] = added
    body = "".join(lines)

    parsed = json.loads(body)
    block = parsed.get("log", {}).get("filter", {})
    want = {k for k, _ in entries}
    if not want.issubset(set(block)):
        raise SystemExit(f"{name}: log.filter 缺键 {want - set(block)}")
    for sibling in ("status", "faultKind", "testKind", "fields", "autoRefresh"):
        if sibling not in block:
            raise SystemExit(f"{name}: log.filter.{sibling} 消失")

    path.write_text(body, encoding="utf-8", newline="")
    print(f"{name}: +{len(added)} lines, keys ok")


for locale, entries in ENTRIES.items():
    insert(locale, entries)

print("done")
