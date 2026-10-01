#!/usr/bin/env python3
"""给三语 locale 的 home.balance 补 reasonTotal 文案。

与 add_balance_no_record_i18n.py 同一套文本插入法：不整文件 json.load/dump
重写（避免把 1400+ 行 CRLF 文件的格式差异混进 diff）。

锚点用**已有的兄弟键** `"reasonSummary"` 那一行，插在它后面 —— 这样不需要
再判断父缩进，直接沿用该行的缩进即可，比按 `{` 定位更稳。
"""
import io
import json
import sys

APPLY = "--apply" in sys.argv
ANCHOR_KEY = '"reasonSummary":'
NEW_KEY = "reasonTotal"
TEXTS = {
    "zh_hans": "归类合计 {counted} / 未读到 {total}",
    "zh_hant": "歸類合計 {counted} / 未讀到 {total}",
    "en": "classified {counted} / unknown {total}",
}
LOCALES = ("zh_hans", "zh_hant", "en")
PATHS = {loc: "web/src/locales/%s.json" % loc for loc in LOCALES}


def main():
    for loc in LOCALES:
        with io.open(PATHS[loc], encoding="utf-8", newline="") as handle:
            raw = handle.read()

        # 只认 home.balance 里那一处：它前面紧邻 reasonSummary。
        hits = []
        scan = -1
        while True:
            scan = raw.find(ANCHOR_KEY, scan + 1)
            if scan < 0:
                break
            hits.append(scan)
        if len(hits) != 1:
            print("FAIL %s: reasonSummary 命中 %d 处（要求恰好 1 处）" % (loc, len(hits)))
            return 1

        pos = hits[0]
        line_start = raw.rfind("\n", 0, pos) + 1
        indent = " " * (pos - line_start)
        if indent == "":
            print("FAIL %s: 推不出缩进" % loc)
            return 1
        line_end = raw.find("\n", pos) + 1

        before = json.loads(raw)
        block = '%s"%s": %s,\n' % (indent, NEW_KEY, json.dumps(TEXTS[loc], ensure_ascii=False))
        new_raw = raw[:line_end] + block + raw[line_end:]

        try:
            after_obj = json.loads(new_raw)
        except Exception as exc:  # noqa: BLE001
            print("FAIL %s: 插入后 JSON 解析失败 %s" % (loc, exc))
            return 1

        old_keys = set(before["home"]["balance"])
        new_keys = set(after_obj["home"]["balance"])
        if new_keys - old_keys != {NEW_KEY} or old_keys - new_keys:
            print("FAIL %s: 新增 %s 丢失 %s" % (loc, sorted(new_keys - old_keys), sorted(old_keys - new_keys)))
            return 1

        print("OK   %s: balance 键 %d -> %d（+1），缩进 %d"
              % (loc, len(old_keys), len(new_keys), len(indent)))
        if APPLY:
            with io.open(PATHS[loc], "w", encoding="utf-8", newline="") as handle:
                handle.write(new_raw)

    print()
    print("已写入" if APPLY else "预演（未写入）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
