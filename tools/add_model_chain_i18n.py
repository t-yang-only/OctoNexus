# -*- coding: utf-8 -*-
r"""把「模型链路一致性」的三语文案插进 web/src/locales/*.json。

必须用**文本插入**而不是 json.load/dump 整写：整写会把格式差异混进 diff
（本项目实测整写会产生大段无关改动，看不出到底改了什么），插入能稳定得到
「N insertions / 0 deletions」。

两个必须注意的坑（本项目踩过）：
 ① 插入块的收尾 `}` 后面**必须带逗号**（插在最前面，后面还有兄弟键），
    少这个逗号会得到 "Expecting ',' delimiter"，而报错行指向的是后面那个兄弟键；
 ② 拿不准锚点是否唯一时先数一遍 —— `"filter": {` 这类短锚点在文件里出现过多次。
"""
import io
import json
import os

os.chdir(r"D:\奇怪的软件\octopus")

ANCHOR = '      "completeHint":'  # home.insight 段里最后一个键（前面有兄弟键；实测缩进 6 空格）

KEYS = {
    "zh_hans": [
        ("modelChain", "模型链路一致性"),
        ("modelChainHint", "上游回报的模型名与渠道内目标不一致即为换模型"),
        ("colChannel", "渠道"),
        ("chainRequests", "请求"),
        ("chainAlias", "别名解析"),
        ("chainAliasHint", "客户端请求的是分组名，被解析成渠道内的真实模型名 —— 属正常路由，不是异常"),
        ("chainReported", "上游回报"),
        ("chainReportedHint", "上游在响应里真的给出了模型名的行数，是不匹配率的分母"),
        ("chainMatched", "一致"),
        ("chainMismatched", "被换"),
        ("chainSilent", "无法判定"),
        ("chainSilentHint", "上游没有回报模型名，换没换无从判断 —— 不计入一致，也不计入被换"),
        ("chainRate", "不匹配率"),
        ("mismatchSampleTitle", "最近被换的请求"),
    ],
    "zh_hant": [
        ("modelChain", "模型鏈路一致性"),
        ("modelChainHint", "上游回報的模型名與渠道內目標不一致即為換模型"),
        ("colChannel", "渠道"),
        ("chainRequests", "請求"),
        ("chainAlias", "別名解析"),
        ("chainAliasHint", "客戶端請求的是分組名，被解析成渠道內的真實模型名 —— 屬正常路由，不是異常"),
        ("chainReported", "上游回報"),
        ("chainReportedHint", "上游在回應裡真的給出模型名的行數，是不匹配率的分母"),
        ("chainMatched", "一致"),
        ("chainMismatched", "被換"),
        ("chainSilent", "無法判定"),
        ("chainSilentHint", "上游沒有回報模型名，換沒換無從判斷 —— 不計入一致，也不計入被換"),
        ("chainRate", "不匹配率"),
        ("mismatchSampleTitle", "最近被換的請求"),
    ],
    "en": [
        ("modelChain", "Model chain consistency"),
        ("modelChainHint", "Flagged when the model the upstream reports differs from the channel's target"),
        ("colChannel", "Channel"),
        ("chainRequests", "Requests"),
        ("chainAlias", "Alias resolved"),
        ("chainAliasHint", "The client asked for a group name which resolved to the channel's real model — normal routing, not a fault"),
        ("chainReported", "Reported"),
        ("chainReportedHint", "Rows where the upstream actually returned a model name; this is the denominator of the mismatch rate"),
        ("chainMatched", "Matched"),
        ("chainMismatched", "Swapped"),
        ("chainSilent", "Unknown"),
        ("chainSilentHint", "The upstream returned no model name, so whether it swapped cannot be judged — counted as neither matched nor swapped"),
        ("chainRate", "Mismatch rate"),
        ("mismatchSampleTitle", "Recently swapped requests"),
    ],
}

report = []
for lang, pairs in KEYS.items():
    path = "web/src/locales/%s.json" % lang
    body = io.open(path, encoding="utf-8", newline="").read()

    if body.count(ANCHOR) != 1:
        raise SystemExit("%s: 锚点命中 %d 次（应为 1），先确认真实缩进与唯一性" % (path, body.count(ANCHOR)))

    # 校验：锚点这一行的缩进要与兄弟键一致
    anchor_line = [ln for ln in body.split("\r\n") if ln.startswith(ANCHOR)][0]
    indent = anchor_line[: len(anchor_line) - len(anchor_line.lstrip())]

    lines = []
    for key, value in pairs:
        lines.append('%s"%s": %s,' % (indent, key, json.dumps(value, ensure_ascii=False)))
    block = "\r\n".join(lines) + "\r\n"

    new_body = body.replace(ANCHOR, block + ANCHOR, 1)

    # 校验 JSON 与键集
    parsed = json.loads(new_body)
    for key, value in pairs:
        got = parsed["home"]["insight"].get(key)
        if got != value:
            raise SystemExit("%s: 键 %s 写入后读回是 %r" % (path, key, got))

    # 行尾守恒（必须仍为纯 CRLF，不能混进裸 LF）
    if new_body.count("\n") != new_body.count("\r\n"):
        raise SystemExit("%s: 行尾被破坏（出现裸 LF）" % path)

    io.open(path, "w", encoding="utf-8", newline="").write(new_body)
    report.append((path, len(pairs)))

for path, n in report:
    print("  %-32s +%d 键" % (path, n))
print("完成，共 %d 个文件" % len(report))
