"""Static checks for the 渠道名/模型名 auto-group feature (no Go toolchain needed).

Checks:
1. AutoGroupName/channel-normalization expectations from source text.
2. ensureAutoGroupsLocked is wired into ChannelCreate + ChannelUpdate transactions.
3. channel handlers refresh group list cache after channel mutations
   (create/update already do; this file asserts the wiring strings exist).
4. No unbalanced braces/parens in edited Go files (rough heuristic).
"""
from __future__ import annotations

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GROUP_GO = ROOT / "internal" / "op" / "group.go"
CHANNEL_GO = ROOT / "internal" / "op" / "channel.go"
HANDLER_GO = ROOT / "internal" / "server" / "handlers" / "channel.go"

failures: list[str] = []


def check(cond: bool, msg: str) -> None:
    print(("PASS " if cond else "FAIL ") + msg)
    if not cond:
        failures.append(msg)


def read(p: Path) -> str:
    return p.read_text(encoding="utf-8")


def balanced(text: str) -> bool:
    stack: list[str] = []
    pairs = {")": "(", "}": "{", "]": "["}
    in_str = False
    esc = False
    str_ch = ""
    in_line_comment = False
    in_block_comment = False
    i = 0
    while i < len(text):
        ch = text[i]
        nxt = text[i + 1] if i + 1 < len(text) else ""
        if in_line_comment:
            if ch == "\n":
                in_line_comment = False
            i += 1
            continue
        if in_block_comment:
            if ch == "*" and nxt == "/":
                in_block_comment = False
                i += 2
                continue
            i += 1
            continue
        if in_str:
            if esc:
                esc = False
            elif ch == "\\":
                esc = True
            elif ch == str_ch:
                in_str = False
            i += 1
            continue
        if ch == "/" and nxt == "/":
            in_line_comment = True
            i += 2
            continue
        if ch == "/" and nxt == "*":
            in_block_comment = True
            i += 2
            continue
        if ch in ("\"", "'", "`"):
            in_str = True
            str_ch = ch
            i += 1
            continue
        if ch in "({[":
            stack.append(ch)
        elif ch in ")}]":
            if not stack or stack.pop() != pairs[ch]:
                return False
        i += 1
    return not stack and not in_str and not in_block_comment


group_src = read(GROUP_GO)
channel_src = read(CHANNEL_GO)
handler_src = read(HANDLER_GO)

check("func AutoGroupName(channelName, modelName string) (string, error)" in group_src,
      "group.go defines AutoGroupName(channelName, modelName)")
check('return channelName + "/" + modelName, nil' in group_src,
      "AutoGroupName joins with '/' and preserves case")
check("func ensureAutoGroupsLocked(tx *gorm.DB, channelID int, channelName string, modelNames []string) error" in group_src,
      "group.go defines ensureAutoGroupsLocked(tx, channelID, channelName, modelNames)")
check("gorm.ErrRecordNotFound" in group_src, "auto-group path handles ErrRecordNotFound for idempotency")
check("GroupModeFailover" in group_src and "DefaultGroupRelayConfig()" in group_src,
      "auto groups default to failover + default relay config")
check("Priority: maxPriority" in group_src or "Priority:maxPriority" in group_src,
      "auto-group members append with monotonically increasing priority")

check("ensureAutoGroupsLocked(tx, channel.ID, channel.Name, detail.Models)" in channel_src,
      "ChannelCreate wires ensureAutoGroupsLocked in the same transaction")
check("ensureAutoGroupsLocked(tx, detail.ID, detail.Name, detail.Models)" in channel_src,
      "ChannelUpdate wires ensureAutoGroupsLocked in the same transaction")
check("groupRefreshCache(ctx)" in channel_src, "channel.go refreshes group cache after mutations")

check("groupListQueryOptions" in read(ROOT / "web" / "src" / "api" / "channel.ts"),
      "frontend channel mutations invalidate group list query")

for path, src in [("internal/op/group.go", group_src), ("internal/op/channel.go", channel_src)]:
    check(balanced(src), f"{path} braces/parens/strings/comments balanced")

names = re.findall(r"渠道名/模型名|AutoGroupName|ensureAutoGroupsLocked", group_src + channel_src)
check(len(names) >= 6, "auto-group symbols referenced consistently across op files")

if failures:
    print(f"\n{len(failures)} check(s) failed")
    sys.exit(1)
print("\nAll static checks passed")
