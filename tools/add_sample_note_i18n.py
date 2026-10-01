# -*- coding: utf-8 -*-
"""common.sampleNote：画像样本来源账的三语文案（T-trace-006）。"""
import io
import json
import os

BASE = r"D:\奇怪的软件\octopus\web\src\locales"
LANGS = ["zh_hans", "zh_hant", "en"]

# common 段里 `"copy": {` 是唯一锚点；插在它**之前**（它后面还有兄弟键，必然带尾逗号）。
ANCHOR = '"copy": {'

TEXTS = {
    "zh_hans": "已剔除 {count} 条测试请求（窗口共 {total} 条）",
    "zh_hant": "已剔除 {count} 條測試請求（視窗共 {total} 條）",
    "en": "Excluded {count} test request(s) of {total} in window",
}

for lang in LANGS:
    path = os.path.join(BASE, lang + ".json")
    body = io.open(path, "r", encoding="utf-8", newline="").read()
    if '"sampleNote"' in body:
        raise SystemExit("%s: sampleNote 已存在" % lang)
    idx = body.find(ANCHOR)
    if idx < 0 or body.count(ANCHOR) != 1:
        raise SystemExit("%s: 锚点命中 %d 次" % (lang, body.count(ANCHOR)))
    line_start = body.rfind("\n", 0, idx) + 1
    block = '    "sampleNote": %s,\r\n' % json.dumps(TEXTS[lang], ensure_ascii=False)
    body = body[:line_start] + block + body[line_start:]

    if body.count("\r\n") != io.open(path, "r", encoding="utf-8", newline="").read().count("\r\n") + 1:
        raise SystemExit("%s: CRLF 增量异常" % lang)
    parsed = json.loads(body)
    if parsed["common"]["sampleNote"] != TEXTS[lang]:
        raise SystemExit("%s: 落位异常" % lang)
    io.open(path, "w", encoding="utf-8", newline="").write(body)
    print("%s: common.sampleNote 已插入" % lang)

print("done")
