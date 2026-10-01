# -*- coding: utf-8 -*-
"""把已插入的 log.filter 测试组搬到 `"faultKindHint":` 之后（原位会让归因提示与测试组错位）。"""
import io
import json
import os

BASE = r"D:\奇怪的软件\octopus\web\src\locales"
LANGS = ["zh_hans", "zh_hant", "en"]
TEST_KEYS = ['"testKind":', '"testAll":', '"testReal":', '"testOnly":', '"testHint":']

for lang in LANGS:
    path = os.path.join(BASE, lang + ".json")
    body = io.open(path, "r", encoding="utf-8", newline="").read()
    lines = body.split("\r\n")

    idxs = [i for i, line in enumerate(lines) if line.strip().startswith(tuple(TEST_KEYS))]
    if len(idxs) != len(TEST_KEYS):
        raise SystemExit("%s: 期望 %d 行测试键，实得 %d" % (lang, len(TEST_KEYS), len(idxs)))
    if idxs != list(range(idxs[0], idxs[0] + len(TEST_KEYS))):
        raise SystemExit("%s: 测试键不连续: %s" % (lang, idxs))

    block = [lines[i] for i in idxs]
    rest = [line for i, line in enumerate(lines) if i not in set(idxs)]

    anchors = [i for i, line in enumerate(rest) if '"faultKindHint":' in line]
    if len(anchors) != 1:
        raise SystemExit("%s: faultKindHint 锚点 %d 个" % (lang, len(anchors)))
    at = anchors[0]

    if rest[at + 1:at + 1 + len(TEST_KEYS)] == block:
        print("%s: 已在正确位置，跳过" % lang)
        continue

    out = rest[:at + 1] + block + rest[at + 1:]
    text = "\r\n".join(out)
    parsed = json.loads(text)
    for key in ("testKind", "testAll", "testReal", "testOnly", "testHint"):
        if key not in parsed["log"]["filter"]:
            raise SystemExit("%s: log.filter.%s 丢失" % (lang, key))
    io.open(path, "w", encoding="utf-8", newline="").write(text)
    print("%s: 已搬到 faultKindHint 之后（JSON 校验通过）" % lang)

print("done")
