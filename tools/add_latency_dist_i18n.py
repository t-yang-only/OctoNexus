#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""为 T-insight-004（首页延迟分布）插入三语文案。

口径（与既有 tools/add_*_i18n.py 一致）：
  * 文本插入而非 json.load/json.dump 整文件重写——整写会把格式差异混进 diff。
  * 锚点用 home.insight 段里的 'completeHint'（该段最后一个键，**带**行尾逗号），
    新段插在它后面、home.insight 的收尾 `}` 之前。这样新块自带逗号收尾、
    锚点与段尾都不动，少一次改动就少一处出错机会。
  * 文件是 UTF-8 无 BOM、CRLF，行尾从文件自身取样；构造完先 json.loads 校验再落盘。
"""

import io
import json
import os
import sys

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOCALES = os.path.join(REPO, "web", "src", "locales")

# 锚点：home 下的 "allocation" 段起始行（带逗号）。新段插在它**之前**，
# 与 home.insight 平级 —— 这样既有行一个都不动，不需要给谁补逗号也不需要删。
ANCHOR = '"allocation": {'
NEW_BLOCK = "latency"

# 每个语言的 20 个键，顺序即插入顺序。
COPY = {
    "zh_hans": [
        '"title": "延迟分布"',
        '"subtitle": "窗口内整体有多慢。只看分位数与直方图：耗时的平均值由少数极端值决定，描述不了任何一次真实体验。"',
        '"firstByteP50": "首字节中位"',
        '"durationP50": "总耗时中位"',
        '"durationP95": "总耗时 p95"',
        '"slowRatio": "慢请求占比"',
        '"firstByteHint": "从请求发出到上游吐出第一个字节。它不是纯网络延迟：同一条链路（TLS 握手 1ms）下首字节中位可达 4.5 秒，差的是上游排队与推理。"',
        '"durationHint": "从发出到全部收完。p50 描述典型体验——它比平均值更可信，因为一次偶发卡顿就能把平均值拉高。"',
        '"p95Hint": "有 5% 的请求比这个数更慢。挑渠道看中位数，看整体健康看它：p50 漂亮但 p95 巨大，体验是「大多数时候快、偶尔卡死」。"',
        '"slowRatioHint": "总耗时达到 {threshold} 的请求占比（本窗口 {count} 条）。阈值随结果返回，保证界面上的「慢」与后端判定同源。"',
        '"firstByteTitle": "首字节（何时开始看到东西）"',
        '"durationTitle": "总耗时（全部看完花了多久）"',
        '"samples": "{count} 条样本"',
        '"p50": "p50"',
        '"p90": "p90"',
        '"p95": "p95"',
        '"p99": "p99"',
        '"firstByteNote": "只统计真的记到首字节的请求。失败与取消的那些（从未提交首字节）不计入——把它们按 0 算会让 p50 被拉低，显示成「上游快得不可思议」。"',
        '"durationNote": "含失败请求。一次 60 秒超时正是最该被看见的慢，排除它会让故障期的延迟数字反而变漂亮。"',
        '"histogramTitle": "总耗时直方图"',
        '"histogramNote": "固定区间：两张不同时间的直方图可以直接对比，自适应分箱会把变化吃进坐标轴。"',
        '"bucketRange": "{lower} – {upper}"',
        '"bucketOver": "≥ {bound}"',
        '"scanned": "扫描 {count} 条日志"',
        '"overOneMinute": "超过 1 分钟"',
        '"truncated": "已达窗口上限，统计只覆盖最近一部分"',
    ],
    "zh_hant": [
        '"title": "延遲分布"',
        '"subtitle": "視窗內整體有多慢。只看分位數與直方圖：耗時的平均值由少數極端值決定，描述不了任何一次真實體驗。"',
        '"firstByteP50": "首字節中位"',
        '"durationP50": "總耗時中位"',
        '"durationP95": "總耗時 p95"',
        '"slowRatio": "慢請求佔比"',
        '"firstByteHint": "從請求發出到上游吐出第一個字節。它不是純網路延遲：同一條鏈路（TLS 握手 1ms）下首字節中位可達 4.5 秒，差的是上游排隊與推理。"',
        '"durationHint": "從發出到全部收完。p50 描述典型體驗——它比平均值更可信，因為一次偶發卡頓就能把平均值拉高。"',
        '"p95Hint": "有 5% 的請求比這個數更慢。挑渠道看中位數，看整體健康看它：p50 漂亮但 p95 巨大，體驗是「大多數時候快、偶爾卡死」。"',
        '"slowRatioHint": "總耗時達到 {threshold} 的請求佔比（本視窗 {count} 條）。閾值隨結果返回，保證介面上的「慢」與後端判定同源。"',
        '"firstByteTitle": "首字節（何時開始看到東西）"',
        '"durationTitle": "總耗時（全部看完花了多久）"',
        '"samples": "{count} 條樣本"',
        '"p50": "p50"',
        '"p90": "p90"',
        '"p95": "p95"',
        '"p99": "p99"',
        '"firstByteNote": "只統計真的記到首字節的請求。失敗與取消的那些（從未提交首字節）不計入——把它們按 0 算會讓 p50 被拉低，顯示成「上游快得不可思議」。"',
        '"durationNote": "含失敗請求。一次 60 秒超時正是最該被看見的慢，排除它會讓故障期的延遲數字反而變漂亮。"',
        '"histogramTitle": "總耗時直方圖"',
        '"histogramNote": "固定區間：兩張不同時間的直方圖可以直接對比，自適應分箱會把變化吃進坐標軸。"',
        '"bucketRange": "{lower} – {upper}"',
        '"bucketOver": "≥ {bound}"',
        '"scanned": "掃描 {count} 條日誌"',
        '"overOneMinute": "超過 1 分鐘"',
        '"truncated": "已達視窗上限，統計只覆蓋最近一部分"',
    ],
    "en": [
        '"title": "Latency Distribution"',
        '"subtitle": "How slow the window is overall. Quantiles and a histogram only: an average latency is decided by a few extremes and describes no single real experience."',
        '"firstByteP50": "TTFB median"',
        '"durationP50": "Duration median"',
        '"durationP95": "Duration p95"',
        '"slowRatio": "Slow requests"',
        '"firstByteHint": "From sending the request to the upstream emitting its first byte. This is not pure network latency: on the same link (1ms TLS handshake) the TTFB median reaches 4.5s — the difference is upstream queueing and inference."',
        '"durationHint": "From sending to fully received. The p50 describes the typical experience, and it is more trustworthy than the average: a single stall can pull an average up."',
        '"p95Hint": "5% of requests are slower than this. Check the median when picking a channel; check this for overall health: a good p50 with a huge p95 means \\"fast most of the time, occasionally stuck\\"."',
        '"slowRatioHint": "Share of requests whose total duration reached {threshold} ({count} in this window). The threshold is returned with the result so the UI and the backend share one definition."',
        '"firstByteTitle": "First byte (when things start showing)"',
        '"durationTitle": "Total duration (how long to read it all)"',
        '"samples": "{count} samples"',
        '"p50": "p50"',
        '"p90": "p90"',
        '"p95": "p95"',
        '"p99": "p99"',
        '"firstByteNote": "Only requests that actually recorded a first byte. Failures and cancellations (never committed) are excluded — counting them as 0 would drag the p50 down and read as \\"impossibly fast upstream\\"."',
        '"durationNote": "Failed requests are included. A 60-second timeout is exactly the slowness worth seeing; excluding it would make latency look better during incidents."',
        '"histogramTitle": "Duration histogram"',
        '"histogramNote": "Fixed buckets: two histograms from different times stay directly comparable, while adaptive binning eats the change into the axis."',
        '"bucketRange": "{lower} – {upper}"',
        '"bucketOver": "≥ {bound}"',
        '"scanned": "{count} logs scanned"',
        '"overOneMinute": "Over 1 minute"',
        '"truncated": "Window limit reached, only the most recent part is covered"',
    ],
}


def insert_one(path, lines_for_lang):
    with io.open(path, "r", encoding="utf-8", newline="") as handle:
        body = handle.read()

    nl = "\r\n" if "\r\n" in body else "\n"

    if '"%s": {' % NEW_BLOCK in body.split('"log"')[0]:
        pass  # 不阻断：用下面的键名判重更可靠

    if '"bucketOver"' in body:
        return 0, "already present"

    raw_lines = body.split(nl)
    hit = -1
    for index, line in enumerate(raw_lines):
        if line.strip().startswith(ANCHOR):
            hit = index
            break
    if hit < 0:
        return 0, "anchor %s not found" % ANCHOR

    # 锚点行的缩进量出来（home.allocation 的键缩进 6 空格）。
    indent = raw_lines[hit][: len(raw_lines[hit]) - len(raw_lines[hit].lstrip())]
    # 新段与 allocation 平级：同一缩进。
    block_indent = indent
    inner_indent = indent + "  "

    block_lines = [block_indent + '"%s": {' % NEW_BLOCK]
    for i, key_line in enumerate(lines_for_lang):
        suffix = "," if i < len(lines_for_lang) - 1 else ""
        block_lines.append(inner_indent + key_line + suffix)
    block_lines.append(block_indent + "},")

    # 插到锚点行**之前**：新段在 home 里占一个位置，锚点及其后全部原样保留。
    new_raw = raw_lines[:hit] + block_lines + raw_lines[hit:]
    updated = nl.join(new_raw)

    if updated.count("\n") - updated.count("\r\n") != 0:
        return 0, "line ending mismatch after build"

    try:
        parsed = json.loads(updated)
    except ValueError as exc:
        return 0, "json invalid: %s" % exc
    if NEW_BLOCK not in parsed.get("home", {}):
        return 0, "block landed outside home"
    if "bucketOver" not in parsed["home"][NEW_BLOCK]:
        return 0, "keys not under home.%s" % NEW_BLOCK

    with io.open(path, "w", encoding="utf-8", newline="") as handle:
        handle.write(updated)
    return 1, "ok"


def main():
    total = 0
    for lang, lines_for_lang in COPY.items():
        path = os.path.join(LOCALES, "%s.json" % lang)
        count, note = insert_one(path, lines_for_lang)
        total += count
        print("%-8s %s (%d)" % (lang, note, count))
    print("total inserted: %d" % total)
    return 0


if __name__ == "__main__":
    sys.exit(main())
