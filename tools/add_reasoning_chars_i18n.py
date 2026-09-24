#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""为 T-insight-005（思考字数）插入三语文案。

两个键、两个锚点，都插在既有键**之后**：
  log.card.reasoningTokensHint  ->  log.card.reasoningCharsHint
  log.filter.reasoningTokens    ->  log.filter.reasoningChars

锚点选 reasoningTokens 系（是本功能同一族的上一个键），不用 "filter": { / "card": {
那类段名——段名在文件里多处出现，层级错了界面会显示未翻译的键名且构建不报错。

口径（与既有 tools/add_*_i18n.py 一致）：
  * 文本插入而非 json.load/json.dump 整文件重写
  * 锚点行本身带逗号（后面还有兄弟键），新行也带逗号，既有行一个不动
  * 文件是 UTF-8 无 BOM、CRLF；构造完先 json.loads 校验再落盘
"""

import io
import json
import os
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOCALES = os.path.join(REPO, "web", "src", "locales")

# (锚点键名, 新键名, 各语言的值)
ENTRIES = [
    (
        "reasoningTokensHint",
        "reasoningCharsHint",
        {
            "zh_hans": "响应正文里思考文本的字符数（一个中文算 1 个字）。上游普遍不回报思考 token，这一项是从正文量出来的，因此通常只有它有意义。它是「文本有多长」，与上面的 token 数（「上游说花了多少」）是两个量，同时有值时都要看。",
            "zh_hant": "回應正文裡思考文本的字元數（一個中文算 1 個字）。上游普遍不回報思考 token，這一項是從正文量出來的，因此通常只有它有意義。它是「文本有多長」，與上面的 token 數（「上游說花了多少」）是兩個量，同時有值時都要看。",
            "en": "Characters of reasoning text in the response body (one CJK character counts as 1). Upstreams rarely report reasoning tokens, so this one is measured from the body itself and is usually the only value available. It is how long the text is, a different quantity from the token count above (what the upstream says it spent); read both when both exist.",
        },
    ),
    (
        "reasoningTokens",
        "reasoningChars",
        {
            "zh_hans": "思考字数",
            "zh_hant": "思考字數",
            "en": "Reasoning chars",
        },
    ),
]


def insert_after(path, anchor_key, new_key, value):
    with io.open(path, "r", encoding="utf-8", newline="") as handle:
        body = handle.read()

    if '"%s"' % new_key in body:
        return 0, "already present"

    nl = "\r\n" if "\r\n" in body else "\n"
    raw_lines = body.split(nl)

    hits = [i for i, line in enumerate(raw_lines) if line.strip().startswith('"%s":' % anchor_key)]
    if len(hits) != 1:
        return 0, "anchor %s matched %d lines (need exactly 1)" % (anchor_key, len(hits))
    index = hits[0]

    anchor_line = raw_lines[index]
    indent = anchor_line[: len(anchor_line) - len(anchor_line.lstrip())]

    # 锚点可能是段的最后一个键（没有尾逗号）。统一插在锚点**之前**：
    # 新行自己带逗号，锚点保持原样，既有行一个字节都不动 ——
    # 插在锚点之后则需要给锚点补逗号，那会改动既有行，而报错行还会指向后面的兄弟键。
    encoded = json.dumps(value, ensure_ascii=False)
    new_line = "%s\"%s\": %s," % (indent, new_key, encoded)

    raw_lines.insert(index, new_line)
    updated = nl.join(raw_lines)

    if updated.count("\n") - updated.count("\r\n") != 0:
        return 0, "line ending mismatch"
    try:
        parsed = json.loads(updated)
    except ValueError as exc:
        return 0, "json invalid: %s" % exc

    # 校验落点：两个键必须分别在 log.card 与 log.filter 下。
    card = parsed.get("log", {}).get("card", {})
    filt = parsed.get("log", {}).get("filter", {})
    expect_in = card if new_key.endswith("Hint") else filt
    if new_key not in expect_in:
        return 0, "key landed in the wrong section"

    with io.open(path, "w", encoding="utf-8", newline="") as handle:
        handle.write(updated)
    return 1, "ok"


def main():
    total = 0
    for anchor_key, new_key, values in ENTRIES:
        for lang, value in values.items():
            path = os.path.join(LOCALES, "%s.json" % lang)
            count, note = insert_after(path, anchor_key, new_key, value)
            total += count
            print("%-8s %-22s %-16s %s" % (lang, new_key, note, count))
    print("total inserted: %d" % total)
    return 0


if __name__ == "__main__":
    sys.exit(main())
