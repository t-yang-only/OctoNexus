#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""为「尝试链」面板补三语文案（T-trace-001）。

按项目既有契约: 必须用**文本插入**而不是 json.load/dump 整文件重写 ——
整写会把格式差异混进 diff, 让人看不出到底改了什么。插入能稳定得到「N insertions、0 deletions」。

锚点是 log.card 段里的 "waitingResponse" 行: 它在段中, 行尾**带逗号**,
在它后面插入新键最稳（不需要动段末那个没有逗号的键）。
"""
import io
import json

LOCALES = {
    'zh_hans.json': {
        'attemptChain': '尝试明细',
        'attemptChainHint': '按轮次列出这次请求打过的每个成员及各自结果。回答的是「换过谁、为什么换」——只看总次数和最终渠道，看不出某个成员在反复拖后腿。',
        'attemptOk': '已接管',
        'attemptTruncated': '前面还有更多轮次未列出',
        'attemptEmpty': '本次请求只打过一个成员',
    },
    'zh_hant.json': {
        'attemptChain': '嘗試明細',
        'attemptChainHint': '按輪次列出這次請求打過的每個成員及各自結果。回答的是「換過誰、為什麼換」——只看總次數和最終渠道，看不出某個成員在反覆拖後腿。',
        'attemptOk': '已接管',
        'attemptTruncated': '前面還有更多輪次未列出',
        'attemptEmpty': '本次請求只打過一個成員',
    },
    'en.json': {
        'attemptChain': 'Attempt detail',
        'attemptChainHint': 'Every member this request hit, round by round, with each outcome. This answers "who was swapped out, and why" — a total count and the final channel cannot show that one member keeps dragging the request down.',
        'attemptOk': 'took over',
        'attemptTruncated': 'earlier rounds omitted',
        'attemptEmpty': 'This request hit a single member',
    },
}

# 锚点行: 必须是**段中**且行尾带逗号的键, 它既是插入位置也是缩进取样来源。
ANCHOR = '"waitingResponse"'


def patch(path: str, additions: dict) -> None:
    # newline='' 表示"不翻译行尾", 于是读到的 \r\n 原样保留、写回时也原样保留。
    # 这一点是本脚本最容易出错的地方: 工作区文件是 CRLF（仓库存 LF, 由 core.autocrlf 转换）,
    # 若插入块单独用 '\n' 拼接, 就会得到"1 行 LF + 其余 CRLF"的混合行尾文件 ——
    # git 会把它看成整文件重写（实测 1444 行全增全删）, 完全看不出真正改了什么。
    # 因此插入块的行尾必须从文件自身取样, 而不是硬编码。
    with io.open(path, 'r', encoding='utf-8', newline='') as fh:
        text = fh.read()

    if '"attemptChain"' in text:
        print('  %-16s 已存在 attemptChain，跳过' % path.split('\\')[-1])
        return

    key_at = text.find(ANCHOR)
    if key_at < 0:
        raise SystemExit('anchor %s not found in %s' % (ANCHOR, path))

    line_start = text.rfind('\n', 0, key_at)
    line_start = 0 if line_start < 0 else line_start + 1
    line_end = text.find('\n', key_at)
    if line_end < 0:
        raise SystemExit('unterminated anchor line in %s' % path)

    anchor_line = text[line_start:line_end]
    if not anchor_line.rstrip().endswith(','):
        raise SystemExit('anchor line does not end with a comma: %r' % anchor_line)

    # 缩进 = 锚点行的前导空白（不含行尾的 \r）。
    indent = anchor_line[:len(anchor_line) - len(anchor_line.lstrip())]

    # 行尾从文件自身取样: 看行尾字符前一个字符是不是 \r（\r\n 的位置是 line_end-1）。
    if line_end > 0 and text[line_end - 1] == '\r':
        crlf = '\r\n'
    else:
        crlf = '\n'
    lines = ['%s"%s": %s,' % (indent, k, json.dumps(v, ensure_ascii=False)) for k, v in additions.items()]
    block = crlf.join(lines)

    # 插到锚点行**之后**: line_end 指向 \n, 跳过它才是下一行的起点。
    # 若在此处补 crlf 而不跳过 \n, 会得到 "\r\r\n" —— 反斜杠 r 变成一个孤立的回车,
    # 表现出来就是"混合行尾"(本脚本的自检会拦下它)。
    insert_at = line_end + 1
    text = text[:insert_at] + block + crlf + text[insert_at:]

    parsed = json.loads(text)
    card = parsed['log']['card']
    missing = [k for k in additions if k not in card]
    if missing:
        raise SystemExit('inserted keys missing after patch: %s' % missing)
    if card.get('waitingResponse') is None:
        raise SystemExit('anchor key value was damaged')

    # 行尾自检: 全文件必须只有一种行尾（混合行尾会让 git 看成整文件重写）。
    body = text[:-1] if text.endswith('\n') else text
    lone_lf = body.count('\n') - body.count('\r\n')
    if lone_lf != 0:
        raise SystemExit('mixed line endings after patch: %d bare LF' % lone_lf)

    with io.open(path, 'w', encoding='utf-8', newline='') as fh:
        fh.write(text)
    print('  %-16s +%d 键  (log.card 现有 %d 键)' % (path.split('\\')[-1], len(additions), len(card)))


def main():
    base = r'D:\奇怪的软件\octopus\web\src\locales'
    for name, additions in LOCALES.items():
        patch(base + '\\' + name, additions)


if __name__ == '__main__':
    main()
