#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""为 T-trace-004（客户端入站协议落库与展示）插入三语文案。

口径（与既有 tools/add_*_i18n.py 一致）：
  * 文本插入而非 json.load/json.dump 整文件重写——整写会把格式差异混进 diff。
  * 锚点用 'reasoningEffortHint'（log.card 段倒数第二个键，**带**行尾逗号），
    新键插在它后面、'reasoningTokensHint' 之前。这样新键带逗号、锚点与段尾键都不动，
    不需要给谁补逗号也不需要给谁删逗号——少一次改动就少一处出错机会。
  * 文件是 UTF-8 无 BOM、CRLF，行尾从文件自身取样；构造完先 json.loads 校验再落盘。
"""

import io
import json
import os
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOCALES = os.path.join(REPO, "web", "src", "locales")

# 锚点必须是 log.card 段里带逗号的键：整份文件里 reasoningEffortHint 只此一处。
ANCHOR = '"reasoningEffortHint"'

# 解释卡片/弹窗上那个双向箭头图标——回答"这次请求中间做过跨协议转换吗"。
COPY = {
    "zh_hans": '"protocolConvertedHint": "客户端用的协议与上游用的协议不同：这次请求中间做过一次跨协议转换，请求体与响应体都被改写了一遍。",',
    "zh_hant": '"protocolConvertedHint": "客戶端用的協定與上游用的協定不同：這次請求中間做過一次跨協定轉換，請求體與回應體都被改寫了一遍。",',
    "en": '"protocolConvertedHint": "The client protocol differs from the upstream one: this request was converted across protocols, so both the request and response bodies were rewritten.",',
}


def insert_one(path, key_line):
    with io.open(path, "r", encoding="utf-8", newline="") as handle:
        body = handle.read()

    # 行尾必须与文件自身一致（本仓库 locales 是 CRLF）。
    nl = "\r\n" if "\r\n" in body else "\n"

    if '"protocolConvertedHint"' in body:
        return 0, "already present"

    lines = body.split(nl)
    hit = -1
    for index, line in enumerate(lines):
        if line.strip().startswith(ANCHOR):
            hit = index
            break
    if hit < 0:
        return 0, "anchor %s not found" % ANCHOR

    if not lines[hit].rstrip().endswith(","):
        # 锚点必须带逗号，否则说明它已是段尾——那时插入就要改动兄弟键的逗号，
        # 而报错行会指向后面那个键，极易看错方向。直接拒绝比猜安全。
        return 0, "anchor has no trailing comma; refusing to guess"

    # 缩进从锚点行自身量出来，不写死空格数。
    indent = lines[hit][: len(lines[hit]) - len(lines[hit].lstrip())]
    lines.insert(hit + 1, indent + key_line)

    updated = nl.join(lines)
    if updated.count("\n") - updated.count("\r\n") != 0:
        return 0, "line ending mismatch after build"

    try:
        parsed = json.loads(updated)  # 先校验再落盘
    except ValueError as exc:
        return 0, "json invalid: %s" % exc
    if "protocolConvertedHint" not in parsed["log"]["card"]:
        return 0, "key landed outside log.card"

    with io.open(path, "w", encoding="utf-8", newline="") as handle:
        handle.write(updated)
    return 1, "ok"


def main():
    total = 0
    for lang, key_line in COPY.items():
        path = os.path.join(LOCALES, "%s.json" % lang)
        count, note = insert_one(path, key_line)
        total += count
        print("%-8s %s (%d)" % (lang, note, count))
    print("total inserted: %d" % total)
    return 0


if __name__ == "__main__":
    sys.exit(main())
