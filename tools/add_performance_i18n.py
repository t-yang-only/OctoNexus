"""给三语 locale 插入 home.performance 段（需求5 首页性能指标 RPM/TPM）。

约定（本项目反复踩过）：
  * 必须用**文本插入**而不是 json.load/json.dump 整文件重写 —— 整写会把格式差异混进 diff；
  * 插入块收尾的 `},` 必须带逗号，否则报错行会指向后面那个兄弟键，极易看错方向；
  * 行尾从文件自身取样（本仓 locale 是 CRLF），读写都走 newline=""；
  * 落盘前先 json.loads 校验并把键集与兄弟键核对一遍。
"""

import json
from pathlib import Path

ROOT = Path(r"D:\奇怪的软件\octopus")
LOCALES = ROOT / "web" / "src" / "locales"

HOME_HEAD = '  "home": {'
ANCHOR = '    "allocation": {'
PARENT_END = '    },'


def block(entries, eol):
    out = [f'    "performance": {{{eol}']
    for i, (key, value) in enumerate(entries):
        comma = "," if i < len(entries) - 1 else ""
        out.append(f'      "{key}": {json.dumps(value, ensure_ascii=False)}{comma}{eol}')
    out.append(f'    }},{eol}')
    return out


ZH_HANS = [
    ("title", "性能指标"),
    ("rpm", "平均 RPM"),
    ("tpm", "平均 TPM"),
    ("tps", "吞吐"),
    ("rpmUnit", "次/分"),
    ("tpmUnit", "Token/分"),
    ("tpsUnit", "Token/秒"),
    ("basis", "按最近 {count} 条请求的真实跨度 {span} 折算（窗口上限 {window} 条）"),
    ("empty", "还没有可折算的请求"),
    ("hint", "分母是首尾请求的实际时间差，不是窗口长度 —— 空闲时段不会把速率稀释；本块与「请求窗口分析」同源同窗口，两处数字必然一致。"),
]

ZH_HANT = [
    ("title", "效能指標"),
    ("rpm", "平均 RPM"),
    ("tpm", "平均 TPM"),
    ("tps", "吞吐"),
    ("rpmUnit", "次/分"),
    ("tpmUnit", "Token/分"),
    ("tpsUnit", "Token/秒"),
    ("basis", "依最近 {count} 筆請求的真實跨度 {span} 折算（視窗上限 {window} 筆）"),
    ("empty", "尚無可折算的請求"),
    ("hint", "分母是首尾請求的實際時間差，不是視窗長度 —— 空閒時段不會稀釋速率；本區塊與「請求視窗分析」同源同視窗，兩處數字必然一致。"),
]

EN = [
    ("title", "Performance"),
    ("rpm", "Avg RPM"),
    ("tpm", "Avg TPM"),
    ("tps", "Throughput"),
    ("rpmUnit", "req/min"),
    ("tpmUnit", "tok/min"),
    ("tpsUnit", "tok/s"),
    ("basis", "folded over the real span of the last {count} requests ({span}); window cap {window}"),
    ("empty", "no requests to fold yet"),
    ("hint", "The denominator is the real gap between the first and last request, not the window length - idle periods do not dilute the rate. Same source and window as Request window analysis, so the two always agree."),
]

TARGETS = {"zh_hans.json": ZH_HANS, "zh_hant.json": ZH_HANT, "en.json": EN}


def insert(name, entries):
    path = LOCALES / name
    raw = path.read_text(encoding="utf-8", newline="")
    eol = "\r\n" if "\r\n" in raw[:2000] else "\n"
    lines = raw.splitlines(keepends=True)

    def bare(index):
        return lines[index].rstrip("\r\n")

    if any(bare(i) == '    "performance": {' for i in range(len(lines))):
        print(f"{name}: already has home.performance, skip")
        return

    home = next(i for i in range(len(lines)) if bare(i) == HOME_HEAD)
    anchor = next(i for i in range(home, len(lines)) if bare(i) == ANCHOR)
    if bare(anchor - 1) != PARENT_END:
        raise SystemExit(f"{name}: anchor preceded by unexpected line {bare(anchor - 1)!r}")

    added = block(entries, eol)
    lines[anchor:anchor] = added
    body = "".join(lines)

    parsed = json.loads(body)  # 落盘前必须能解析
    got = parsed["home"]["performance"]
    want = {k for k, _ in entries}
    if set(got) != want:
        raise SystemExit(f"{name}: key set mismatch {set(got) ^ want}")
    for sibling in ("insight", "allocation", "metric", "total"):
        if sibling not in parsed["home"]:
            raise SystemExit(f"{name}: home.{sibling} disappeared")

    path.write_text(body, encoding="utf-8", newline="")
    print(f"{name}: +{len(added)} lines, keys={len(got)}, siblings ok")


for locale, entries in TARGETS.items():
    insert(locale, entries)

print("done")
