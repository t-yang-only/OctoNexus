# -*- coding: utf-8 -*-
"""导航文案调整（需求7）：日志 → 使用记录，并修掉 model 项写错成"价格"的文案 bug。

规则：
  * 一律**文本替换**而不是 json.load/json.dump 整写 —— 整写会把格式差异混进 diff；
  * 读写走 bytes 并保留原行尾（locale 是 CRLF，用文本模式写回会把 \\n 展开成 \\r\\n）；
  * 每处替换前断言旧串**恰好出现一次**，否则报错退出（锚点漂了就停下来看，别猜）。

为什么要改 model 项：它现在在中文里显示成"价格"，与 price 项同名，
用户点"价格"分不清进哪个页面（这是抄错文案，与本次需求同属导航层）。
"""
import os
# 仓库根：优先环境变量，否则按脚本位置反推（本脚本在 <仓库根>/<目录>/ 下）。
# 原先这里写死本机绝对路径，换目录或换机器就跑不起来。
_REPO = os.environ.get("OCTOPUS_ROOT") or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
import json
import sys
from pathlib import Path

LOCALES = Path(os.path.join(_REPO, "web", "src", "locales"))

EDITS = {
    "zh_hans.json": [
        ('"log": "日志"', '"log": "使用记录"'),
        ('"model": "价格"', '"model": "模型"'),
    ],
    "zh_hant.json": [
        ('"log": "日誌"', '"log": "使用記錄"'),
        ('"model": "價格"', '"model": "模型"'),
    ],
    "en.json": [
        ('"log": "Log"', '"log": "Usage Records"'),
        ('"model": "Price"', '"model": "Model"'),
    ],
}

EXPECT = {
    "zh_hans.json": {"log": "使用记录", "model": "模型"},
    "zh_hant.json": {"log": "使用記錄", "model": "模型"},
    "en.json": {"log": "Usage Records", "model": "Model"},
}


def main() -> int:
    for name, edits in EDITS.items():
        path = LOCALES / name
        text = path.read_bytes().decode("utf-8")
        for old, new in edits:
            count = text.count(old)
            if count != 1:
                print(f"[{name}] ANCHOR-MISS: {old!r} 命中 {count} 次，期望 1 次")
                return 1
            text = text.replace(old, new, 1)

        parsed = json.loads(text)
        navbar = parsed.get("navbar", {})
        for key, want in EXPECT[name].items():
            got = navbar.get(key)
            if got != want:
                print(f"[{name}] 校验失败：navbar.{key} = {got!r}，期望 {want!r}")
                return 1
        # 兄弟键不能被挤掉
        for key in ("home", "channel", "group", "account", "setting", "pool", "extensions", "proxy", "price"):
            if key not in navbar:
                print(f"[{name}] 校验失败：兄弟键 navbar.{key} 丢失")
                return 1

        path.write_bytes(text.encode("utf-8"))
        print(f"[{name}] 已更新：log={navbar['log']!r} model={navbar['model']!r}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
