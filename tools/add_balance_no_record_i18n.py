#!/usr/bin/env python3
"""给三语 locale 的 home.balance.reason 区块补 no_record 文案。

沿用 tools/add_rank_filter_i18n.py 的文本插入法：不做整文件 json.load/dump
重写，避免把 1400+ 行 CRLF 文件的格式差异混进 diff（能得到 N insertions / 0 deletions）。

坑（上一轮踩过、这里同样适用）：插入块的收尾 `}` 必须带逗号 —— 插在兄弟键
之前。少逗号会报 `Expecting ',' delimiter`，但报错行指向的是**后面的兄弟键**，
病因和报错位置不在同一处，只能靠"把插入结果打出来看"而不是盯报错行猜。
"""
import io
import json
import sys

APPLY = "--apply" in sys.argv
ANCHOR = '"reason": {'
# `"reason": {` 在 locale 里出现不止一次（另一个区块的首子键是 breached）。
# 只凭出现次数找不到目标，所以用**首子键**判定：要的是 no_endpoint 那一个。
FIRST_CHILD = '"no_endpoint"'
NEW_KEY = "no_record"
TEXTS = {
    "zh_hans": "没有读数记录（从未扫过）",
    "zh_hant": "沒有讀數記錄（從未掃過）",
    "en": "no scan record yet",
}
LOCALES = ("zh_hans", "zh_hant", "en")
PATHS = {loc: "web/src/locales/%s.json" % loc for loc in LOCALES}


def main():
    for loc in LOCALES:
        with io.open(PATHS[loc], encoding="utf-8", newline="") as handle:
            raw = handle.read()

        # 定位 home.balance.reason：必须同时满足「锚点是 "reason": {」且
        # 「下一行的首子键是 "no_endpoint"」。
        candidates = []
        scan = -1
        while True:
            scan = raw.find(ANCHOR, scan + 1)
            if scan < 0:
                break
            nxt = raw.find("\n", scan) + 1
            if nxt > 0:
                head = raw[nxt:].lstrip(" ")
                if head.startswith(FIRST_CHILD):
                    candidates.append((scan, nxt))
        if len(candidates) != 1:
            print("FAIL %s: 命中 %d 处 home.balance.reason（要求恰好 1 处），中止" % (loc, len(candidates)))
            return 1
        pos, after = candidates[0]

        line_start = raw.rfind("\n", 0, pos) + 1
        parent_indent = pos - line_start
        first_child = raw[after:]
        first_child_indent = len(first_child) - len(first_child.lstrip(" "))
        step = first_child_indent - parent_indent
        if step <= 0 or step > 8:
            print("FAIL %s: 推不出缩进步长（父 %d 子 %d）" % (loc, parent_indent, first_child_indent))
            return 1

        before = json.loads(raw)
        block = '%s"%s": %s,\n' % (" " * first_child_indent, NEW_KEY,
                                   json.dumps(TEXTS[loc], ensure_ascii=False))
        new_raw = raw[:after] + block + raw[after:]

        try:
            after_obj = json.loads(new_raw)
        except Exception as exc:  # noqa: BLE001
            print("FAIL %s: 插入后 JSON 解析失败 %s" % (loc, exc))
            return 1

        old_keys = set(before["home"]["balance"]["reason"])
        new_keys = set(after_obj["home"]["balance"]["reason"])
        if new_keys - old_keys != {NEW_KEY} or old_keys - new_keys:
            print("FAIL %s: 新增 %s 丢失 %s" % (loc, sorted(new_keys - old_keys), sorted(old_keys - new_keys)))
            return 1

        print("OK   %s: reason 键 %d -> %d（+1），父缩进 %d 步长 %d"
              % (loc, len(old_keys), len(new_keys), parent_indent, step))
        if APPLY:
            with io.open(PATHS[loc], "w", encoding="utf-8", newline="") as handle:
                handle.write(new_raw)

    print()
    print("已写入" if APPLY else "预演（未写入）")
    return 0


if __name__ == "__main__":
    sys.exit(main())
