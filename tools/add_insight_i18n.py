"""给三语 locale 插入 home.insight 段（T-insight-002 请求窗口分析）。

约定（本项目反复踩过）：
  * 必须用**文本插入**而不是 json.load/json.dump 整文件重写 —— 整写会把格式差异混进 diff；
  * 插入块收尾的 `},` 必须带逗号，否则报错行会指向后面那个兄弟键，极易看错方向；
  * 行尾从文件自身取样（本仓 locale 是 CRLF）；
  * 落盘前先 json.loads 校验并把键集核对一遍。
"""

import json
from pathlib import Path

ROOT = Path(r"D:\奇怪的软件\octopus")
LOCALES = ROOT / "web" / "src" / "locales"

# 锚点：home 段里 allocation 之前插入 insight，保持与前端渲染顺序一致。
HOME_HEAD = '  "home": {'
ANCHOR = '    "allocation": {'
PARENT_END = '    },'


def block(entries, eol):
    out = [f'    "insight": {{{eol}']
    for i, (key, value) in enumerate(entries):
        comma = "," if i < len(entries) - 1 else ""
        out.append(f'      "{key}": {json.dumps(value, ensure_ascii=False)}{comma}{eol}')
    out.append(f'    }},{eol}')
    return out


ZH_HANS = [
    ("title", "请求窗口分析"),
    ("subtitle", "最近若干条请求的真实切片，与上方累计画像互补"),
    ("successRate", "成功率"),
    ("channelRate", "渠道健康度"),
    ("avgRpm", "平均 RPM"),
    ("rpmUnit", "次/分"),
    ("avgTpm", "平均 TPM"),
    ("tpmUnit", "Token/分"),
    ("throughput", "吞吐"),
    ("tpsUnit", "Token/秒"),
    ("timeline", "Token 时间线"),
    ("spanPrefix", "跨度"),
    ("faults", "失败归因"),
    ("faultSuccess", "成功"),
    ("faultCanceled", "取消"),
    ("faultRequest", "请求非法"),
    ("faultMember", "成员故障"),
    ("faultTransient", "可恢复"),
    ("faultUnclassified", "未分类"),
    ("other", "其他"),
    ("truncatedHint", "已达窗口上限，仅统计最近若干条"),
    ("completeHint", "已覆盖全部历史记录"),
]

ZH_HANT = [
    ("title", "請求視窗分析"),
    ("subtitle", "最近若干筆請求的真實切片，與上方累計畫像互補"),
    ("successRate", "成功率"),
    ("channelRate", "通路健康度"),
    ("avgRpm", "平均 RPM"),
    ("rpmUnit", "次/分"),
    ("avgTpm", "平均 TPM"),
    ("tpmUnit", "Token/分"),
    ("throughput", "吞吐"),
    ("tpsUnit", "Token/秒"),
    ("timeline", "Token 時間軸"),
    ("spanPrefix", "跨度"),
    ("faults", "失敗歸因"),
    ("faultSuccess", "成功"),
    ("faultCanceled", "取消"),
    ("faultRequest", "請求非法"),
    ("faultMember", "成員故障"),
    ("faultTransient", "可恢復"),
    ("faultUnclassified", "未分類"),
    ("other", "其他"),
    ("truncatedHint", "已達視窗上限，僅統計最近若干筆"),
    ("completeHint", "已涵蓋全部歷史記錄"),
]

EN = [
    ("title", "Request window analysis"),
    ("subtitle", "A slice of the most recent requests, complementing the cumulative view above"),
    ("successRate", "Success rate"),
    ("channelRate", "Channel health"),
    ("avgRpm", "Avg RPM"),
    ("rpmUnit", "req/min"),
    ("avgTpm", "Avg TPM"),
    ("tpmUnit", "tok/min"),
    ("throughput", "Throughput"),
    ("tpsUnit", "tok/s"),
    ("timeline", "Token timeline"),
    ("spanPrefix", "Span"),
    ("faults", "Failure attribution"),
    ("faultSuccess", "Success"),
    ("faultCanceled", "Canceled"),
    ("faultRequest", "Invalid request"),
    ("faultMember", "Member fault"),
    ("faultTransient", "Transient"),
    ("faultUnclassified", "Unclassified"),
    ("other", "Other"),
    ("truncatedHint", "Window limit reached - counts cover the most recent records only"),
    ("completeHint", "All history covered"),
]

TARGETS = {"zh_hans.json": ZH_HANS, "zh_hant.json": ZH_HANT, "en.json": EN}


def insert(name, entries):
    path = LOCALES / name
    raw = path.read_text(encoding="utf-8", newline="")
    eol = "\r\n" if "\r\n" in raw[:2000] else "\n"
    lines = raw.splitlines(keepends=True)

    def bare(index):
        return lines[index].rstrip("\r\n")

    if any(bare(i) == '    "insight": {' for i in range(len(lines))):
        print(f"{name}: already has home.insight, skip")
        return

    home = next(i for i in range(len(lines)) if bare(i) == HOME_HEAD)
    anchor = next(i for i in range(home, len(lines)) if bare(i) == ANCHOR)
    if bare(anchor - 1) != PARENT_END:
        raise SystemExit(f"{name}: anchor preceded by unexpected line {bare(anchor - 1)!r}")

    added = block(entries, eol)
    lines[anchor:anchor] = added
    body = "".join(lines)

    parsed = json.loads(body)  # 落盘前必须能解析
    got = parsed["home"]["insight"]
    want = {k for k, _ in entries}
    if set(got) != want:
        raise SystemExit(f"{name}: key set mismatch {set(got) ^ want}")
    # 兄弟键仍在：确认没有把 allocation 挤掉或弄坏
    for sibling in ("monitor", "allocation", "rank"):
        if sibling not in parsed["home"]:
            raise SystemExit(f"{name}: home.{sibling} disappeared")

    path.write_text(body, encoding="utf-8", newline="")
    print(f"{name}: +{len(added)} lines, keys={len(got)}, siblings ok")


for locale, entries in TARGETS.items():
    insert(locale, entries)

print("done")
