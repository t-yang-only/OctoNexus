"""T-trace-003 终止原因面板的三语文案插入。

复用 add_attempt_stats_i18n.py 的做法（文本插入，不整文件重写）。

## 本轮新增的坑（实测）

锚点 `"filter": {` 在文件里**多处出现**，直接 find 会命中更早的那个同级键
（别的顶层命名空间下也有 filter），导致新块被插到错误的父级下 ——
而 json.loads 依然能通过（语法合法），只是 `parsed["log"]["stopReason"]`
取不到。因此必须**先定位 log 段起点，再在其后找第一个 filter**，
并且复核命中行的缩进必须正好 4 空格。
"""
import json
import sys
from pathlib import Path

REPO = Path(r"D:\奇怪的软件\octopus")
LOCALES = REPO / "web" / "src" / "locales"

TEXTS = {
    "zh_hans": {
        "title": "终止原因",
        "source": "（来自{source}）",
        "reasonBudget": "尝试次数用尽才放弃",
        "reasonAllRejected": "全部成员都判定该请求非法",
        "reasonFailFast": "配置要求首个成员拒绝即结束",
        "reasonMemberFault": "成员自身问题且已无其他成员",
        "reasonNoMember": "分组内没有可用成员",
        "reasonClientCancel": "客户端主动断开",
        "sourceConfig": "配置",
        "sourceUpstream": "上游",
        "sourceClient": "客户端",
        "sourceSystem": "系统规则",
    },
    "zh_hant": {
        "title": "終止原因",
        "source": "（來自{source}）",
        "reasonBudget": "嘗試次數用盡才放棄",
        "reasonAllRejected": "全部成員都判定該請求非法",
        "reasonFailFast": "配置要求首個成員拒絕即結束",
        "reasonMemberFault": "成員自身問題且已無其他成員",
        "reasonNoMember": "分組內沒有可用成員",
        "reasonClientCancel": "客戶端主動斷開",
        "sourceConfig": "配置",
        "sourceUpstream": "上游",
        "sourceClient": "客戶端",
        "sourceSystem": "系統規則",
    },
    "en": {
        "title": "Stop reason",
        "source": "(from {source})",
        "reasonBudget": "gave up after exhausting the attempt budget",
        "reasonAllRejected": "every member rejected the request",
        "reasonFailFast": "config stops on the first rejection",
        "reasonMemberFault": "member fault with no alternative left",
        "reasonNoMember": "no usable member in the group",
        "reasonClientCancel": "client disconnected",
        "sourceConfig": "config",
        "sourceUpstream": "upstream",
        "sourceClient": "client",
        "sourceSystem": "system rule",
    },
}


def indent_of(line: str) -> str:
    return line[: len(line) - len(line.lstrip())]


def main() -> int:
    changed = []
    for name, keys in TEXTS.items():
        path = LOCALES / f"{name}.json"
        text = path.read_text(encoding="utf-8")
        if '"stopReason"' in text:
            print(f"  {name}: 已存在 stopReason，跳过")
            continue

        newline = "\r\n" if "\r\n" in text else "\n"

        # 先定位 log 段（缩进 4 空格），再在其后找第一个 filter —— 见文件头注释的坑。
        log_idx = text.find(f'{newline}    "log": {{')
        if log_idx < 0:
            print(f"  {name}: 找不到 log 段", file=sys.stderr)
            return 1
        idx = text.find('"filter": {', log_idx)
        if idx < 0:
            print(f"  {name}: log 段内找不到 filter", file=sys.stderr)
            return 1

        anchor_line_start = text.rfind(newline, 0, idx) + len(newline)
        anchor_line_end = text.find(newline, anchor_line_start)
        anchor_line = text[anchor_line_start:anchor_line_end]
        if indent_of(anchor_line) != "    ":
            print(f"  {name}: 锚点缩进异常 {len(indent_of(anchor_line))}", file=sys.stderr)
            return 1

        block_lines = ['    "stopReason": {']
        items = list(keys.items())
        for i, (k, v) in enumerate(items):
            comma = "," if i < len(items) - 1 else ""
            block_lines.append(f'        "{k}": {json.dumps(v, ensure_ascii=False)}{comma}')
        block_lines.append("    },")
        block = newline.join(block_lines) + newline

        updated = text[:anchor_line_start] + block + text[anchor_line_start:]

        parsed = json.loads(updated)
        got = parsed["log"]["stopReason"]
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
