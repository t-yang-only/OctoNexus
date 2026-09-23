"""为日志页「失败归因」筛选块补三语文案（纯文本插入，不整文件重写）。

背景：后端历史接口与实时 SSE 都已下发 fault_kind，但前端类型没声明、界面没有筛选入口，
用户能看到「成员故障 5」却找不到那 5 条。本工具加 8 个键到 log.filter。

关键做法（沿用 add_allocation_filter_i18n.py 的文本插入法）：
- 不 json.load/json.dump 整文件重写，否则 1400+ 行的 CRLF 共享文件 diff 会被格式差异淹没。
- 插入块的收尾 `}` 必须带逗号（插在最前面，后面还有兄弟键）。
"""
import json
import io
import os
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOCALES = os.path.join(ROOT, "web", "src", "locales")

TEXTS = {
    "zh_hans": {
        "faultKind": "失败归因",
        "faultAll": "全部",
        "faultRequest": "请求非法",
        "faultMember": "成员故障",
        "faultTransient": "可恢复",
        "faultNone": "未分类",
        "faultKindHint": "归因由后端在失败发生时就地判定，前端只做展示与筛选；「未分类」含升级前的历史记录。",
    },
    "zh_hant": {
        "faultKind": "失敗歸因",
        "faultAll": "全部",
        "faultRequest": "請求非法",
        "faultMember": "成員故障",
        "faultTransient": "可恢復",
        "faultNone": "未分類",
        "faultKindHint": "歸因由後端在失敗發生時就地判定，前端只做展示與篩選；「未分類」含升級前的歷史記錄。",
    },
    "en": {
        "faultKind": "Failure attribution",
        "faultAll": "All",
        "faultRequest": "Invalid request",
        "faultMember": "Member",
        "faultTransient": "Transient",
        "faultNone": "Unclassified",
        "faultKindHint": "Attribution is decided by the backend at failure time; the UI only filters it. Unclassified includes pre-upgrade records.",
    },
}


def find_filter_block(lines):
    """定位 log.filter 的起始行号（0-based）与其子键缩进。

    锚点是 `"filter": {`，且下一行是首子键 `"status"` —— 因为 filter 这个键名在别处也可能出现，
    只按出现次数定位不稳。
    """
    for i, line in enumerate(lines):
        if '"filter": {' not in line:
            continue
        # 看紧随其后的若干行里第一个非空行是不是首子键 status
        for j in range(i + 1, min(i + 6, len(lines))):
            stripped = lines[j].strip()
            if not stripped:
                continue
            if stripped.startswith('"status"'):
                indent = len(lines[j]) - len(lines[j].lstrip(' '))
                return i, indent
            break
    return None, None


def process(path, texts, apply=False):
    with io.open(path, "r", encoding="utf-8", newline="") as fh:
        raw = fh.read()
    newline = "\r\n" if "\r\n" in raw else "\n"
    lines = raw.split(newline)

    start, indent = find_filter_block(lines)
    if start is None:
        print("!! %s: 找不到 log.filter 锚点" % path)
        return False

    pad = " " * indent
    inner = " " * indent  # 子键行的起始缩进

    block = [pad + '"' + k + '": ' + json.dumps(v, ensure_ascii=False) + "," for k, v in texts.items()]
    # 收尾 } 必须带逗号：插入位置在最前面，后面还有兄弟键
    block = [inner + b.strip() for b in block]

    out = lines[: start + 1] + block + lines[start + 1 :]
    text = newline.join(out)

    # 落盘前先校验，避免写出坏 JSON（报错行常指向后面的兄弟键，误导方向）
    try:
        json.loads(text)
    except Exception as exc:  # noqa: BLE001
        print("!! %s: 插入后 JSON 非法: %s" % (path, exc))
        return False

    if not apply:
        print("-- %s: 将插入 %d 个键（缩进 %d）" % (path, len(texts), indent))
        return True

    with io.open(path, "w", encoding="utf-8", newline="") as fh:
        fh.write(text)
    print("OK %s: +%d 键" % (path, len(texts)))
    return True


def main():
    apply = "--apply" in sys.argv
    ok = True
    for loc, texts in TEXTS.items():
        ok = process(os.path.join(LOCALES, loc + ".json"), texts, apply) and ok
    if not apply:
        print("（预览模式，加 --apply 落盘）")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
