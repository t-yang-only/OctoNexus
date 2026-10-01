"""T-trace-002 尝试链聚合面板的三语文案插入。

## 为什么用文本插入而不是 json.load/json.dump 整文件重写

整写会把格式差异（缩进方式、键序、末尾逗号）混进 diff，实测会产生大段
无关改动，让人看不出到底改了什么。插入能稳定得到「N insertions、0 deletions」。

## 两个必须注意的坑（本项目已踩过）

1. 插入块的收尾 `}` 后面**必须带逗号** —— 插在最前面时后面还有兄弟键，
   少这个逗号会得到 "Expecting ',' delimiter"，而报错行指向的却是后面
   那个兄弟键，极易看错方向。
2. 构造完必须先 json.loads 校验，再落盘。

## 行尾

必须从文件自身取样（工作区 CRLF、仓库 LF），不能硬编码。
"""
import json
import sys
from pathlib import Path

REPO = Path(r"D:\奇怪的软件\octopus")
LOCALES = REPO / "web" / "src" / "locales"

# 三语文案：log.attemptStats 命名空间
TEXTS = {
    "zh_hans": {
        "title": "尝试链聚合",
        "scanned": "扫过 {count} 条带明细的请求",
        "multiRound": "换过成员",
        "affected": "有失败轮",
        "explain": "「有失败轮」含最终成功的请求——某轮失败后换人成功了，那轮在这之前的所有统计里都看不见。",
        "truncated": "其中 {count} 条的轮次被截断，聚合值会偏低",
        "attempts": "共尝试 {count} 次",
        "failures": "失败",
        "memberFault": "成员故障 {count}",
        "transientFault": "可恢复 {count}",
        "requestFault": "请求非法 {count}",
    },
    "zh_hant": {
        "title": "嘗試鏈聚合",
        "scanned": "掃過 {count} 條帶明細的請求",
        "multiRound": "換過成員",
        "affected": "有失敗輪",
        "explain": "「有失敗輪」含最終成功的請求——某輪失敗後換人成功了，那輪在這之前的所有統計裡都看不見。",
        "truncated": "其中 {count} 條的輪次被截斷，聚合值會偏低",
        "attempts": "共嘗試 {count} 次",
        "failures": "失敗",
        "memberFault": "成員故障 {count}",
        "transientFault": "可恢復 {count}",
        "requestFault": "請求非法 {count}",
    },
    "en": {
        "title": "Attempt chain summary",
        "scanned": "Scanned {count} requests with detail",
        "multiRound": "Switched member",
        "affected": "Had a failed round",
        "explain": "\"Had a failed round\" includes requests that still succeeded — a failed round followed by a successful retry is invisible to every other statistic here.",
        "truncated": "{count} of them were truncated, so totals are understated",
        "attempts": "{count} attempts",
        "failures": "failed",
        "memberFault": "member {count}",
        "transientFault": "transient {count}",
        "requestFault": "request {count}",
    },
}


def indent_of(line: str) -> str:
    return line[: len(line) - len(line.lstrip())]


def insert_block(text: str, anchor: str, block: str, newline: str) -> str | None:
    """把 block 插到 anchor 行之前（anchor 必须是某个子键的开头行）。"""
    idx = text.find(anchor)
    if idx < 0:
        return None
    line_start = text.rfind(newline, 0, idx) + len(newline)
    return text[:line_start] + block + text[line_start:]


def build_block(namespace_line: str, keys: dict, newline: str, quotes: int) -> str:
    """按父缩进拼出命名空间块。"""
    parent = indent_of(namespace_line)
    child = parent + " " * 4
    lines = [f'{parent}"attemptStats": {{']
    items = list(keys.items())
    for i, (k, v) in enumerate(items):
        comma = "," if i < len(items) - 1 else ""
        lines.append(f'{child}"{k}": {json.dumps(v, ensure_ascii=False)}{comma}')
    lines.append(f"{parent}}},")
    return newline.join(lines) + newline


def main() -> int:
    changed = []
    for name, keys in TEXTS.items():
        path = LOCALES / f"{name}.json"
        text = path.read_text(encoding="utf-8")
        if '"attemptStats"' in text:
            print(f"  {name}: 已存在 attemptStats，跳过")
            continue

        newline = "\r\n" if "\r\n" in text else "\n"

        # 锚点选 log 段的**直接子节** "filter": { —— 它缩进 4 空格，
        # 与我们要建的 attemptStats 同级（面板用 useTranslations('log.attemptStats')）。
        #
        # 两个坑都必须避开：
        #   1. 不能用 attemptChain 当锚点 —— 它在 log.card 下（缩进 6 空格），
        #      新键会被插进 card 里，与前端命名空间对不上；
        #      这个错误不会让构建失败，只会让界面显示成未翻译的键名。
        #   2. 不能直接 find('"filter": {') —— 文件里多处有同名键，
        #      必须**先定位 log 段起点**，再在其后找第一个 filter。
        log_idx = text.find(f'{newline}    "log": {{')
        if log_idx < 0:
            print(f"  {name}: 找不到 log 段", file=sys.stderr)
            return 1
        idx = text.find('"filter": {', log_idx)
        if idx < 0:
            print(f"  {name}: log 段内找不到 filter 子节", file=sys.stderr)
            return 1
        # 复核：命中的行缩进必须是 4 空格（即 log 的直接子键）。
        anchor_line_start = text.rfind(newline, 0, idx) + len(newline)
        anchor_line_end = text.find(newline, anchor_line_start)
        anchor_line = text[anchor_line_start:anchor_line_end]
        if indent_of(anchor_line) != "    ":
            print(
                f"  {name}: 锚点缩进 {len(indent_of(anchor_line))} 空格，期望 4 —— 层级不对",
                file=sys.stderr,
            )
            return 1

        child_indent = indent_of(anchor_line)

        # 命名空间块：父缩进 = 子键缩进 - 4
        parent_line = child_indent[:-4] + '"x": {'
        block = build_block(parent_line, keys, newline, 0)

        updated = text[:anchor_line_start] + block + text[anchor_line_start:]

        # 落盘前校验：能解析、键集正确、且**路径与前端调用一致**。
        parsed = json.loads(updated)
        got = parsed["log"]["attemptStats"]
        if set(got.keys()) != set(keys.keys()):
            print(f"  {name}: 键集不匹配 {set(got) ^ set(keys)}", file=sys.stderr)
            return 1

        path.write_text(updated, encoding="utf-8")
        changed.append(name)
        print(f"  {name}: 插入 {len(keys)} 键，json 校验通过")

    print(f"\n共改动 {len(changed)} 个文件: {', '.join(changed)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
