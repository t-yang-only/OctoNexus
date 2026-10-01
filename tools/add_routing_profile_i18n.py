# -*- coding: utf-8 -*-
r"""给三语 locale 插入 home.routing 段（T-insight-007）。

必须用**文本插入**而不是 json.load/json.dump 整写：整写会把格式差异混进 diff，
让人看不出到底改了什么。插入能稳定得到「N insertions / 0 deletions」。

两个必须注意的坑（本项目已踩过多次）：
  ① 插入块的收尾 `}` 后面必须带逗号（插在最前面，后面还有兄弟键），
     少这个逗号会得到 "Expecting ',' delimiter"，而报错行指向的却是后面那个兄弟键；
  ② 构造完必须先 json.loads 校验并核对键集，再落盘。
"""
import io
import json
import os

BASE = r"D:\奇怪的软件\octopus\web\src\locales"
LOCALES = ["zh_hans", "zh_hant", "en"]

# 锚点：home 段下的一个既有子段，插在它之前。
ANCHOR_KEY = '"allocation": {'

STRINGS = {
    "zh_hans": {
        "title": "选路与终止画像",
        "subtitle": "谁决定这次选谁、请求是怎么停下来的",
        "windowTotal": "窗口请求",
        "recorded": "已记录",
        "unrecorded": "未记录",
        "malformed": "无法解析",
        "coverageHint": "只有「已记录」的请求参与下面的机制占比 —— 未记录与无法解析的行不计入任何分母",
        "reasonsTitle": "选路机制",
        "denominatorHint": "占比以 {count} 条已记录的行为分母",
        "modesTitle": "分组模式",
        "tiersTitle": "智能路由档位",
        "tiersHint": "只统计 {count} 条智能路由请求",
        "slotTitle": "组内序号",
        "slotHint": "命中的是分组内第几个成员 —— 首位占比高说明排序几乎没起作用",
        "slotAvg": "平均序号",
        "slotSamples": "样本",
        "stopsTitle": "终止原因",
        "stopsHint": "同一个原因来自不同来源时，处置动作不同",
        "stopSource": "来源",
        "stopDenominatorHint": "分母为 {recorded} 条已记录；另有 {unrecorded} 条没有标注终止原因（历史行或漏标）",
        "empty": "窗口内没有可统计的数据",
        "outcomeSuccess": "成功",
        "outcomeCanceled": "取消",
        "outcomeFailed": "失败",
        "reasonManual": "人工指定",
        "reasonAffinity": "亲和沿用",
        "reasonProbe": "冷却探测",
        "reasonPriority": "顺序取首个",
        "reasonRanked": "排序取首位",
        "sourceSystem": "系统规则",
        "sourceConfig": "用户设置",
        "sourceClient": "客户端",
        "sourceUpstream": "上游",
    },
    "zh_hant": {
        "title": "選路與終止畫像",
        "subtitle": "誰決定這次選誰、請求是怎麼停下來的",
        "windowTotal": "窗口請求",
        "recorded": "已記錄",
        "unrecorded": "未記錄",
        "malformed": "無法解析",
        "coverageHint": "只有「已記錄」的請求參與下面的機制佔比 —— 未記錄與無法解析的行不計入任何分母",
        "reasonsTitle": "選路機制",
        "denominatorHint": "佔比以 {count} 條已記錄的行為分母",
        "modesTitle": "分組模式",
        "tiersTitle": "智慧路由檔位",
        "tiersHint": "只統計 {count} 條智慧路由請求",
        "slotTitle": "組內序號",
        "slotHint": "命中的是分組內第幾個成員 —— 首位佔比高說明排序幾乎沒起作用",
        "slotAvg": "平均序號",
        "slotSamples": "樣本",
        "stopsTitle": "終止原因",
        "stopsHint": "同一個原因來自不同來源時，處置動作不同",
        "stopSource": "來源",
        "stopDenominatorHint": "分母為 {recorded} 條已記錄；另有 {unrecorded} 條沒有標註終止原因（歷史行或漏標）",
        "empty": "窗口內沒有可統計的資料",
        "outcomeSuccess": "成功",
        "outcomeCanceled": "取消",
        "outcomeFailed": "失敗",
        "reasonManual": "人工指定",
        "reasonAffinity": "親和沿用",
        "reasonProbe": "冷卻探測",
        "reasonPriority": "順序取首個",
        "reasonRanked": "排序取首位",
        "sourceSystem": "系統規則",
        "sourceConfig": "使用者設定",
        "sourceClient": "用戶端",
        "sourceUpstream": "上游",
    },
    "en": {
        "title": "Routing & stop profile",
        "subtitle": "Who picked the member, and how the request stopped",
        "windowTotal": "Requests",
        "recorded": "Recorded",
        "unrecorded": "Unrecorded",
        "malformed": "Unparsable",
        "coverageHint": "Only recorded requests take part in the mechanism ratios below — unrecorded and unparsable rows are in no denominator",
        "reasonsTitle": "Route reason",
        "denominatorHint": "Ratios are over {count} recorded rows",
        "modesTitle": "Group mode",
        "tiersTitle": "Smart tier",
        "tiersHint": "Counts only the {count} smart-routed requests",
        "slotTitle": "Member slot",
        "slotHint": "Which member of the group was picked — a high first-slot share means ordering barely matters",
        "slotAvg": "Average slot",
        "slotSamples": "Samples",
        "stopsTitle": "Stop reason",
        "stopsHint": "The same reason from a different source needs a different fix",
        "stopSource": "Source",
        "stopDenominatorHint": "Over {recorded} recorded rows; {unrecorded} more carry no stop reason (legacy rows or missing call sites)",
        "empty": "Nothing to summarise in this window",
        "outcomeSuccess": "OK",
        "outcomeCanceled": "Canceled",
        "outcomeFailed": "Failed",
        "reasonManual": "Manual",
        "reasonAffinity": "Affinity",
        "reasonProbe": "Probe",
        "reasonPriority": "Priority",
        "reasonRanked": "Ranked",
        "sourceSystem": "System",
        "sourceConfig": "Config",
        "sourceClient": "Client",
        "sourceUpstream": "Upstream",
    },
}

SEGMENT = "routeProfile"
FIELDS = sorted(STRINGS["zh_hans"].keys())
for locale, table in STRINGS.items():
    if sorted(table.keys()) != FIELDS:
        raise SystemExit("%s 的键集与 zh_hans 不一致" % locale)

# 占位符三语必须一致：少一个占位符，那一行会显示成字面量 {count}。
import re

PLACEHOLDER = re.compile(r"\{[a-z]+\}")
for field in FIELDS:
    expected = sorted(PLACEHOLDER.findall(STRINGS["zh_hans"][field]))
    for locale in LOCALES:
        got = sorted(PLACEHOLDER.findall(STRINGS[locale][field]))
        if got != expected:
            raise SystemExit("%s.%s 占位符不一致：%s vs %s" % (locale, field, got, expected))


def build_block(table, key_indent, value_indent, close_indent):
    lines = ['%s"%s": {' % (key_indent, SEGMENT)]
    for index, field in enumerate(FIELDS):
        tail = "," if index < len(FIELDS) - 1 else ""
        lines.append('%s"%s": %s%s' % (value_indent, field, json.dumps(table[field], ensure_ascii=False), tail))
    lines.append("%s}," % close_indent)
    return lines


def main():
    for locale in LOCALES:
        path = os.path.join(BASE, locale + ".json")
        with io.open(path, encoding="utf-8", newline="") as handle:
            original = handle.read()

        crlf = original.count("\r\n")
        lf_only = original.count("\n") - crlf
        eol = "\r\n" if crlf > lf_only else "\n"

        if '"%s": {' % SEGMENT in original:
            print("%s：已存在 %s 段，跳过" % (locale, SEGMENT))
            continue

        anchor = eol + " " * 0 + ANCHOR_KEY
        # 锚点必须带行首缩进才唯一：先按行找，取缩进最浅（= home 子段）的那个。
        candidates = []
        offset = 0
        while True:
            index = original.find(ANCHOR_KEY, offset)
            if index < 0:
                break
            line_start = original.rfind(eol, 0, index) + len(eol)
            indent = original[line_start:index]
            if indent.strip() == "":
                candidates.append((index, line_start, indent))
            offset = index + 1
        if len(candidates) != 1:
            raise SystemExit("%s：锚点 %s 命中 %d 次（应为 1）" % (locale, ANCHOR_KEY, len(candidates)))

        index, line_start, indent = candidates[0]
        key_indent = indent
        step = "  "
        value_indent = key_indent + step
        close_indent = key_indent
        block = eol.join(build_block(STRINGS[locale], key_indent, value_indent, close_indent)) + eol

        updated = original[:line_start] + block + original[line_start:]

        # 落盘前先校验：JSON 合法 + 键集完整 + 行尾守恒。
        parsed = json.loads(updated)
        segment = parsed["home"][SEGMENT]
        if sorted(segment.keys()) != FIELDS:
            raise SystemExit("%s：插入后键集不符" % locale)
        if updated.count("\n") - updated.count("\r\n") != lf_only:
            raise SystemExit("%s：行尾被改变" % locale)
        # 插入新行必然增加 CRLF 数，所以要校验的是**增量等于插入块自己的行数**
        # （而不是"总数不变"—— 那样任何正常插入都会失败，等于校验器从没被正常路径验证过）。
        if updated.count("\r\n") != crlf + block.count("\r\n"):
            raise SystemExit("%s：插入块的 CRLF 增量不符（可能混入裸 LF）" % locale)

        with io.open(path, "w", encoding="utf-8", newline="") as handle:
            handle.write(updated)

        print("%s：插入 %d 键，文件 %d -> %d 字节（行尾 %s）"
              % (locale, len(FIELDS), len(original), len(updated), "CRLF" if eol == "\r\n" else "LF"))


if __name__ == "__main__":
    main()
