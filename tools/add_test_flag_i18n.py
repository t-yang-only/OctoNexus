# -*- coding: utf-8 -*-
"""T-trace-006 测试请求标记：三语文案插入。

两个锚点都选「确定是段内中间键」的行，插在它们**之前** —— 这样新块自带尾逗号、
锚点行一个字节不动，不需要给锚点补逗号（补逗号时一旦漏了，报错行会指向后面的兄弟键，
极易看错方向）。
"""
import io
import json
import os

BASE = r"D:\奇怪的软件\octopus\web\src\locales"
LANGS = ["zh_hans", "zh_hant", "en"]

CARD_ANCHOR = '"protocolConvertedHint":'
# filter 段里 `"status":` 在整份文件出现 5 次（各语言/各区块都有 status），不能做锚点。
# 改用段内唯一的 "faultKindHint" 并插在它**之后** —— 它在 filter 段里后面还有 status 等键，
# 所以必然带尾逗号，插在它后面不需要给任何既有行补逗号。
FILTER_ANCHOR = '"faultKindHint":'

CARD_KEYS = {
    "zh_hans": [
        ("isTest", "测试"),
        ("isTestHint", "这条请求由客户端用 X-Octopus-Test 头声明为验证/测试。总览与画像默认把它剔除 —— 它出现在这里却不出现在统计里是设计如此，不是丢数据。"),
    ],
    "zh_hant": [
        ("isTest", "測試"),
        ("isTestHint", "這條請求由客戶端用 X-Octopus-Test 頭宣告為驗證/測試。總覽與畫像預設將它剔除 —— 它出現在這裡卻不出現在統計裡是設計如此，不是丟資料。"),
    ],
    "en": [
        ("isTest", "TEST"),
        ("isTestHint", "This request was declared a verification/test call by the client via the X-Octopus-Test header. Overviews and profiles exclude it by default — appearing here but not in the stats is by design, not lost data."),
    ],
}

FILTER_KEYS = {
    "zh_hans": [
        ("testKind", "请求来源"),
        ("testAll", "全部"),
        ("testReal", "真实"),
        ("testOnly", "测试"),
        ("testHint", "「测试」是客户端用 X-Octopus-Test 头主动声明的验证请求。总览与画像默认剔除它们，所以这里看得到、统计里看不到是设计如此。"),
    ],
    "zh_hant": [
        ("testKind", "請求來源"),
        ("testAll", "全部"),
        ("testReal", "真實"),
        ("testOnly", "測試"),
        ("testHint", "「測試」是客戶端用 X-Octopus-Test 頭主動宣告的驗證請求。總覽與畫像預設剔除它們，所以在這裡看得到、統計裡看不到是設計如此。"),
    ],
    "en": [
        ("testKind", "Source"),
        ("testAll", "All"),
        ("testReal", "Real"),
        ("testOnly", "Test"),
        ("testHint", "'Test' means the client declared it via the X-Octopus-Test header. Overviews and profiles exclude these by default, so being visible here and absent from the stats is by design."),
    ],
}


def insert_before(body, anchor_token, pairs, indent, eol):
    idx = body.find(anchor_token)
    if idx < 0:
        raise SystemExit("锚点未找到: %s" % anchor_token)
    if body.count(anchor_token) != 1:
        raise SystemExit("锚点不唯一（%d 次）: %s" % (body.count(anchor_token), anchor_token))
    line_start = body.rfind("\n", 0, idx) + 1
    block = "".join(
        '%s"%s": %s,%s' % (indent, key, json.dumps(value, ensure_ascii=False), eol)
        for key, value in pairs
    )
    return body[:line_start] + block + body[line_start:]


def insert_after(body, anchor_token, pairs, indent, eol):
    idx = body.find(anchor_token)
    if idx < 0:
        raise SystemExit("锚点未找到: %s" % anchor_token)
    if body.count(anchor_token) != 1:
        raise SystemExit("锚点不唯一（%d 次）: %s" % (body.count(anchor_token), anchor_token))
    line_end = body.find("\n", idx) + 1
    block = "".join(
        '%s"%s": %s,%s' % (indent, key, json.dumps(value, ensure_ascii=False), eol)
        for key, value in pairs
    )
    return body[:line_end] + block + body[line_end:]


for lang in LANGS:
    path = os.path.join(BASE, lang + ".json")
    body = io.open(path, "r", encoding="utf-8", newline="").read()
    before_crlf = body.count("\r\n")
    before_lf = body.count("\n")

    if '"isTestHint"' in body or '"testKind"' in body:
        raise SystemExit("%s: 键已存在，避免重复插入" % lang)

    body = insert_before(body, CARD_ANCHOR, CARD_KEYS[lang], "      ", "\r\n")
    body = insert_before(body, FILTER_ANCHOR, FILTER_KEYS[lang], "      ", "\r\n")

    # 行尾守恒：插入的新行带来了新的 \r\n，差值必须与新增行数一致。
    added_lines = len(CARD_KEYS[lang]) + len(FILTER_KEYS[lang])
    if body.count("\r\n") != before_crlf + added_lines:
        raise SystemExit("%s: CRLF 增量异常 %d != %d" % (lang, body.count("\r\n") - before_crlf, added_lines))
    if body.count("\n") != before_lf + added_lines:
        raise SystemExit("%s: LF 增量异常" % lang)

    parsed = json.loads(body)
    for key, _ in CARD_KEYS[lang]:
        if key not in parsed["log"]["card"]:
            raise SystemExit("%s: log.card.%s 不在预期层级" % (lang, key))
    for key, _ in FILTER_KEYS[lang]:
        if key not in parsed["log"]["filter"]:
            raise SystemExit("%s: log.filter.%s 不在预期层级" % (lang, key))

    io.open(path, "w", encoding="utf-8", newline="").write(body)
    print("%s: +%d 键（card %d + filter %d），JSON 校验通过" % (
        lang, added_lines, len(CARD_KEYS[lang]), len(FILTER_KEYS[lang])))

print("done")
