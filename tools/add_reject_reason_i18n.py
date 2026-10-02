# -*- coding: utf-8 -*-
"""给 log.stopReason 加两个新终止原因的文案（T-reject-001）。

为什么用文本插入而不是 json.load/json.dump 整文件重写：
  整写会把格式差异混进 diff（实测会产生大段无关改动，让人看不出到底改了什么），
  插入能稳定得到「N insertions、0 deletions」。

两个必须注意的坑（项目里踩过）：
  ① 插入块的收尾 `}` 后面必须带逗号 —— 本文档是在兄弟键之间插入，少逗号会得到
     "Expecting ',' delimiter"，而报错行指向的却是后面那个兄弟键，极易看错方向；
  ② 构造完必须先 json.loads 校验并核对键集，再落盘。

读写一律走 bytes 并在文件内部推断行尾：locale 文件是 CRLF，用文本模式写回会按平台
把 \\n 展开成 \\r\\n（在别的文件上曾把 LF 变成 CRLF，diff 瞬间变成整文件重写）。
"""
import os
# 仓库根：优先环境变量，否则按脚本位置反推（本脚本在 <仓库根>/<目录>/ 下）。
# 原先这里写死本机绝对路径，换目录或换机器就跑不起来。
_REPO = os.environ.get("OCTOPUS_ROOT") or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
import json
import sys
from pathlib import Path

LOCALES = Path(os.path.join(_REPO, "web", "src", "locales"))
ANCHOR = '"reasonNoMember"'

ENTRIES = {
    "zh_hans.json": [
        ("reasonKeyScope", "请求的模型不在这把 Key 的模型范围内"),
        ("reasonModelNotFound", "模型名没有对应的分组"),
    ],
    "zh_hant.json": [
        ("reasonKeyScope", "請求的模型不在這把 Key 的模型範圍內"),
        ("reasonModelNotFound", "模型名沒有對應的分組"),
    ],
    "en.json": [
        ("reasonKeyScope", "the model is outside this API key's allowed models"),
        ("reasonModelNotFound", "no group matches the requested model name"),
    ],
}


def main() -> int:
    for name, entries in ENTRIES.items():
        path = LOCALES / name
        text = path.read_bytes().decode("utf-8")
        eol = "\r\n" if "\r\n" in text else "\n"

        # 定位锚点行，取它的缩进作为新行的缩进（避免写死空格数）。
        lines = text.split(eol)
        anchor_index = None
        for i, line in enumerate(lines):
            if ANCHOR in line:
                anchor_index = i
                break
        if anchor_index is None:
            print(f"[{name}] ANCHOR-MISS：找不到 {ANCHOR}")
            return 1

        anchor_line = lines[anchor_index]
        indent = anchor_line[: len(anchor_line) - len(anchor_line.lstrip())]

        added = []
        for key, value in entries:
            if f'"{key}"' in text:
                print(f"[{name}] 跳过 {key}（已存在）")
                continue
            added.append(f'{indent}"{key}": {json.dumps(value, ensure_ascii=False)},')
        if not added:
            continue

        # 在锚点行之后插入：锚点行自己带着逗号，插进去的块末尾也带逗号。
        lines[anchor_index + 1 : anchor_index + 1] = added
        new_text = eol.join(lines)

        parsed = json.loads(new_text)
        block = parsed.get("log", {}).get("stopReason", {})
        for key, _ in entries:
            if key not in block:
                print(f"[{name}] 校验失败：键 {key} 没进 stopReason")
                return 1
        # 顺带确认没把兄弟键挤掉。
        for sibling in ("reasonBudget", "reasonNoMember", "sourceConfig"):
            if sibling not in block:
                print(f"[{name}] 校验失败：兄弟键 {sibling} 丢失")
                return 1

        path.write_bytes(new_text.encode("utf-8"))
        print(f"[{name}] 插入 {len(added)} 行，键集校验通过")

    return 0


if __name__ == "__main__":
    sys.exit(main())