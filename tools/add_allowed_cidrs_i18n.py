"""给三语 locale 补 API Key IP 白名单的文案。

用 json 读写而不是文本替换：locale 文件是 4 空格缩进的 JSON，
键顺序也要保持稳定，手写替换容易破坏格式。
"""
import json
import os
import sys

ROOT = r"D:\奇怪的软件\octopus\web\src\locales"

TEXTS = {
    "zh_hans": {
        "allowedCIDRs": "来源 IP 白名单",
        "allowedCIDRsPlaceholder": "留空则不限制，如 10.0.0.0/8, 192.168.1.5",
        "allowedCIDRsHint": "填单个 IP 或 CIDR 网段，逗号分隔；留空表示不限制",
    },
    "zh_hant": {
        "allowedCIDRs": "來源 IP 白名單",
        "allowedCIDRsPlaceholder": "留空則不限制，如 10.0.0.0/8, 192.168.1.5",
        "allowedCIDRsHint": "填單個 IP 或 CIDR 網段，逗號分隔；留空表示不限制",
    },
    "en": {
        "allowedCIDRs": "Source IP allowlist",
        "allowedCIDRsPlaceholder": "Empty means unrestricted, e.g. 10.0.0.0/8, 192.168.1.5",
        "allowedCIDRsHint": "One IP or CIDR per entry, comma separated; empty means unrestricted",
    },
}

failed = False
for lang, items in TEXTS.items():
    path = os.path.join(ROOT, "%s.json" % lang)
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    form = data["setting"]["apiKey"]["form"]
    added = 0
    for key, value in items.items():
        if key not in form:
            form[key] = value
            added += 1
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=4)
        fh.write("\n")
    print("%-8s added=%d total form keys=%d" % (lang, added, len(form)))
    for key in items:
        if form.get(key) != items[key]:
            print("   !! %s 值不正确" % key)
            failed = True

sys.exit(1 if failed else 0)
