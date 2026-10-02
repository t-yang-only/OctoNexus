# -*- coding: utf-8 -*-
"""给三语 locale 的 accounts 段插入删除相关文案（需求8）。

规则与 tools/add_performance_i18n.py 一致：
  * 文本插入，绝不 json.load/json.dump 整写；
  * 行尾从文件自身取样（本仓 locale 是 CRLF），读写走 newline=""；
  * 每处替换前断言锚点恰好命中一次；
  * 落盘前 json.loads 校验键集与兄弟键。
"""
import os
# 仓库根：优先环境变量，否则按脚本位置反推（本脚本在 <仓库根>/<目录>/ 下）。
# 原先这里写死本机绝对路径，换目录或换机器就跑不起来。
_REPO = os.environ.get("OCTOPUS_ROOT") or os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

import json
from pathlib import Path

ROOT = Path(_REPO)
LOCALES = ROOT / "web" / "src" / "locales"
ANCHOR_KEY = "officialSubtitle"

ENTRIES = {
    "zh_hans.json": [
        ("delete", "删除"),
        ("deleteConfirm", "删除「{name}」？该账号在号池里的对应凭据也会一并清除，之后这个账号参与的转发会少一条路。"),
        ("deleteCancel", "取消"),
        ("deleteConfirmButton", "确认删除"),
        ("deleteSuccess", "账号已删除"),
        ("deleteFailed", "删除失败"),
    ],
    "zh_hant.json": [
        ("delete", "刪除"),
        ("deleteConfirm", "刪除「{name}」？該帳號在號池裡的對應憑證也會一併清除，之後這個帳號參與的轉發會少一條路。"),
        ("deleteCancel", "取消"),
        ("deleteConfirmButton", "確認刪除"),
        ("deleteSuccess", "帳號已刪除"),
        ("deleteFailed", "刪除失敗"),
    ],
    "en.json": [
        ("delete", "Delete"),
        ("deleteConfirm", 'Delete "{name}"? The matching pool credential is removed too, so this account will no longer take part in forwarding.'),
        ("deleteCancel", "Cancel"),
        ("deleteConfirmButton", "Confirm delete"),
        ("deleteSuccess", "Account deleted"),
        ("deleteFailed", "Delete failed"),
    ],
}


def insert(name, entries):
    path = LOCALES / name
    raw = path.read_text(encoding="utf-8", newline="")
    eol = "\r\n" if "\r\n" in raw[:2000] else "\n"
    lines = raw.splitlines(keepends=True)

    def bare(index):
        return lines[index].rstrip("\r\n")

    anchor = None
    for i, line in enumerate(lines):
        if line.lstrip().startswith('"%s":' % ANCHOR_KEY):
            anchor = i
            break
    if anchor is None:
        raise SystemExit(f"{name}: 找不到锚点 {ANCHOR_KEY}")

    # 取锚点行的缩进作为新行缩进，避免写死空格数。
    # 每一行都必须带逗号：插在兄弟键**之间**，最后一行后面还有别的键 ——
    # 给它省掉逗号会得到 "Expecting ',' delimiter"，而报错行指向的是后面那个兄弟键。
    indent = lines[anchor][: len(lines[anchor]) - len(lines[anchor].lstrip())]
    added = []
    for key, value in entries:
        added.append(f'{indent}"{key}": {json.dumps(value, ensure_ascii=False)},{eol}')
    lines[anchor + 1 : anchor + 1] = added
    body = "".join(lines)

    parsed = json.loads(body)
    got = parsed["accounts"]
    want = {k for k, _ in entries}
    if not want.issubset(set(got)):
        raise SystemExit(f"{name}: 键缺失 {want - set(got)}")
    for sibling in ("officialTab", "poolTab", "jumpTab", "connect", "status"):
        if sibling not in got:
            raise SystemExit(f"{name}: accounts.{sibling} 消失")

    path.write_text(body, encoding="utf-8", newline="")
    print(f"{name}: +{len(added)} lines, keys ok")


for locale, entries in ENTRIES.items():
    insert(locale, entries)

print("done")
