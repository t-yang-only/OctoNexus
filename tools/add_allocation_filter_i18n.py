#!/usr/bin/env python3
"""给 home.allocation 加筛选/排序文案（三语）。

用文本插入而不是 json.dump 整文件重写：这些 locale 文件是几千行的共享文件，
整体重写会把格式差异混进 diff，让人看不出到底改了什么。插入点固定在
home.allocation 块的第一个 key 之前，缩进从文件里读出来。
"""
import io
import json
import re
import sys

VALUES = {
    'zh_hans': {
        'all': '全部',
        'problem': '异常',
        'ok': '正常',
        'placeholder': '搜索模型 / 渠道 / 钥匙 / 分组',
        'noMatch': '没有匹配的成员 —— 换个关键词，或点「全部」看完整列表。',
        'showing': '显示 {shown} / 共 {total}',
        'clear': '清除筛选',
    },
    'zh_hant': {
        'all': '全部',
        'problem': '異常',
        'ok': '正常',
        'placeholder': '搜尋模型 / 渠道 / 金鑰 / 分組',
        'noMatch': '沒有符合的成員 —— 換個關鍵字，或點「全部」看完整清單。',
        'showing': '顯示 {shown} / 共 {total}',
        'clear': '清除篩選',
    },
    'en': {
        'all': 'All',
        'problem': 'Problems',
        'ok': 'Healthy',
        'placeholder': 'Search model / channel / key / group',
        'noMatch': 'No matching member - try another keyword, or click "All" to see the full list.',
        'showing': '{shown} of {total}',
        'clear': 'Clear filters',
    },
}

ORDER = ['all', 'problem', 'ok', 'placeholder', 'noMatch', 'showing', 'clear']

base = r'D:\奇怪的软件\octopus\web\src\locales'
problems = []

for loc, vals in VALUES.items():
    path = '%s\\%s.json' % (base, loc)
    s = io.open(path, encoding='utf-8', newline='').read()
    if '"filter"' in s and re.search(r'"filter"\s*:\s*\{[^}]*"placeholder"', s):
        print('%s: 已有 filter 块，跳过' % loc)
        continue

    home = s.index('"home"')
    anchor = s.index('"allocation": {', home)
    end_of_anchor = anchor + len('"allocation": {')

    line_start = s.rindex('\n', 0, anchor) + 1
    parent_indent = s[line_start:anchor]
    if parent_indent.strip():
        problems.append('%s: allocation 的缩进解析失败 -> %r' % (loc, parent_indent))
        continue

    nl = s.index('\n', end_of_anchor)
    eol = '\r\n' if s[nl - 1:nl] == '\r' else '\n'
    body_line_start = nl + 1
    indent = re.match(r'[ \t]*', s[body_line_start:]).group(0)
    unit = indent[len(parent_indent):]
    if not unit:
        problems.append('%s: 缩进步长解析失败' % loc)
        continue

    child = indent + unit
    lines = ['%s"filter": {' % indent]
    for i, key in enumerate(ORDER):
        comma = ',' if i < len(ORDER) - 1 else ''
        value = json.dumps(vals[key], ensure_ascii=False)
        lines.append('%s%s: %s%s' % (child, json.dumps(key), value, comma))
    lines.append('%s},' % indent)  # 插在最前面，后面还有兄弟键，这个逗号不能少
    block = eol.join(lines) + eol

    s2 = s[:body_line_start] + block + s[body_line_start:]

    try:
        parsed = json.loads(s2)
    except Exception as exc:  # noqa: BLE001
        problems.append('%s: 插入后 JSON 解析失败 %s' % (loc, exc))
        continue

    got = parsed['home']['allocation'].get('filter')
    if not got or list(got.keys()) != ORDER:
        problems.append('%s: filter 键不齐 %s' % (loc, got))
        continue

    io.open(path, 'w', encoding='utf-8', newline='').write(s2)
    print('%s: 已加 filter（%d 键），allocation 键数 %d -> %d'
          % (loc, len(ORDER), len(parsed['home']['allocation']) - 1, len(parsed['home']['allocation'])))

if problems:
    print('\n有问题：')
    for p in problems:
        print('  ' + p)
    sys.exit(1)
print('OK')
