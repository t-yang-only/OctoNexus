#!/usr/bin/env python3
"""给三语 locale 的 home.rank 区块补榜单筛选文案。

## 为什么用文本插入而不是 json.load/json.dump 整文件重写

locale 文件是 1400+ 行、CRLF 的共享文件。整写会把格式差异（缩进风格、
换行、键顺序）混进 git diff，让人看不出这一版到底改了什么业务内容。
文本插入能稳定得到「N insertions、0 deletions」。

## 两个必须遵守的点

1. 插入块的收尾 `}` **必须带逗号** —— 我们插在第一个子键前面，后面还有兄弟键。
   少这个逗号会得到 `Expecting ',' delimiter`，而报错行指向的却是**后面那个
   兄弟键**（缩进层级误导），病因和报错位置不在同一处。
2. 构造完必须先 `json.loads` 校验并核对键集，再落盘。
"""
import io
import json
import sys

# 首次运行加 --apply；不带参数只做检查（打印将插入的内容与校验结果）。
APPLY = "--apply" in sys.argv

ANCHOR = '"rank": {'

# 新增键（顺序即插入顺序）。{shown}/{total}/{count} 是 use-intl 的插值占位符。
NEW_KEYS = [
    ("filterPlaceholder", {
        "zh_hans": "搜索模型或渠道",
        "zh_hant": "搜尋模型或渠道",
        "en": "Search model or channel",
    }),
    ("filterNoMatch", {
        "zh_hans": "没有匹配的条目 —— 换个关键词试试",
        "zh_hant": "沒有符合的項目 —— 換個關鍵字試試",
        "en": "No matching entry - try another keyword",
    }),
    ("clear", {
        "zh_hans": "清除",
        "zh_hant": "清除",
        "en": "Clear",
    }),
    ("showing", {
        "zh_hans": "显示 {shown} / 共 {total}",
        "zh_hant": "顯示 {shown} / 共 {total}",
        "en": "Showing {shown} of {total}",
    }),
    ("showMore", {
        "zh_hans": "展开全部（还有 {count} 条）",
        "zh_hant": "展開全部（還有 {count} 條）",
        "en": "Show all ({count} more)",
    }),
    ("collapse", {
        "zh_hans": "收起",
        "zh_hant": "收起",
        "en": "Collapse",
    }),
]

LOCALES = ("zh_hans", "zh_hant", "en")
PATHS = {loc: "web/src/locales/%s.json" % loc for loc in LOCALES}


def read(loc):
    with io.open(PATHS[loc], encoding="utf-8", newline="") as handle:
        return handle.read()


def build_block(loc, parent_indent, step):
    """构造带正确缩进的插入块（不含收尾逗号，由调用方补）。"""
    lines = []
    for name, texts in NEW_KEYS:
        value = texts[loc]
        lines.append('%s%s"%s": %s' % (" " * (parent_indent + step), "", name,
                                       json.dumps(value, ensure_ascii=False)))
    # 每个键都要有逗号（后面一定有兄弟键），包括最后一个。
    body = ",\n".join(lines)
    return body + ",\n"


def main():
    total_insertions = 0
    for loc in LOCALES:
        raw = read(loc)
        count = raw.count(ANCHOR)
        if count != 1:
            print("FAIL %s: 锚点出现 %d 次（要求恰好 1 次），中止" % (loc, count))
            return 1
        pos = raw.find(ANCHOR)

        # 父缩进 = 锚点所在行的缩进；步长 = 从文件里读出，不写死。
        line_start = raw.rfind("\n", 0, pos) + 1
        parent_indent = pos - line_start

        # 找第一个子键的缩进作为步长。
        after = raw.find("\n", pos) + 1
        first_child = raw[after:]
        first_child_indent = len(first_child) - len(first_child.lstrip(" "))
        step = first_child_indent - parent_indent
        if step <= 0 or step > 8:
            print("FAIL %s: 推不出缩进步长（父 %d，子 %d）" % (loc, parent_indent, first_child_indent))
            return 1

        before = json.loads(raw)
        block = build_block(loc, parent_indent, step)
        # 插到第一个子键之前（即锚点行之后）。
        new_raw = raw[:after] + block + raw[after:]

        try:
            after_obj = json.loads(new_raw)
        except Exception as exc:  # noqa: BLE001
            print("FAIL %s: 插入后 JSON 解析失败 %s" % (loc, exc))
            return 1

        # 键集核对：旧键一个不缺，新键一个不多。
        old_keys = set(before["home"]["rank"])
        new_keys = set(after_obj["home"]["rank"])
        added = new_keys - old_keys
        lost = old_keys - new_keys
        expect = {name for name, _ in NEW_KEYS}
        if added != expect or lost:
            print("FAIL %s: 新增 %s / 丢失 %s（期望新增 %s）" % (loc, sorted(added), sorted(lost), sorted(expect)))
            return 1

        diff_lines = len(new_raw.splitlines()) - len(raw.splitlines())
        print("OK   %s: 语言键 %d -> %d（+%d），行数 +%d，父缩进 %d 步长 %d"
              % (loc, len(old_keys), len(new_keys), len(added), diff_lines, parent_indent, step))
        if APPLY:
            with io.open(PATHS[loc], "w", encoding="utf-8", newline="") as handle:
                handle.write(new_raw)
        total_insertions += len(NEW_KEYS)

    print()
    print("%s：三语共 %d 处新增" % ("已写入" if APPLY else "预演（未写入）", total_insertions))
    return 0


if __name__ == "__main__":
    sys.exit(main())
