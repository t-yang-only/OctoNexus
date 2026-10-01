"""给三语 locale 补「受信反向代理」设置项的文案。

文案要把「为什么默认不填」讲清楚——这是安全默认值的核心：
不填 = 不信任任何转发头，来源 IP 只按 TCP 对端判定。
"""
import json
import os
import sys

ROOT = r"D:\奇怪的软件\octopus\web\src\locales"

TEXTS = {
    "zh_hans": {
        "label": "受信反向代理",
        "placeholder": "留空表示不信任任何代理",
        "hint": "填你的反向代理地址（如 127.0.0.1 或 172.16.0.0/12），逗号分隔。"
                "留空时网关只按 TCP 对端判定来源 IP，X-Forwarded-For 一律忽略 —— 这是安全默认值，"
                "因为采信转发头会让任何客户端都能伪造来源，API Key 的 IP 白名单随之失效。"
                "只有网关确实挂在可信反代后面时才填。",
    },
    "zh_hant": {
        "label": "受信任反向代理",
        "placeholder": "留空表示不信任任何代理",
        "hint": "填你的反向代理位址（如 127.0.0.1 或 172.16.0.0/12），逗號分隔。"
                "留空時閘道只按 TCP 對端判定來源 IP，X-Forwarded-For 一律忽略 —— 這是安全預設值，"
                "因為採信轉送標頭會讓任何用戶端都能偽造來源，API Key 的 IP 白名單隨之失效。"
                "只有閘道確實掛在可信反向代理後面時才填。",
    },
    "en": {
        "label": "Trusted reverse proxies",
        "placeholder": "Empty means trust no proxy",
        "hint": "Your reverse proxy addresses (e.g. 127.0.0.1 or 172.16.0.0/12), comma separated. "
                "When empty the gateway derives the source IP from the TCP peer only and ignores "
                "X-Forwarded-For entirely - that is the safe default, because honouring forwarded "
                "headers lets any client forge its source and silently defeats an API key's IP allowlist. "
                "Set this only when the gateway really sits behind a trusted proxy.",
    },
}

failed = False
for lang, items in TEXTS.items():
    path = os.path.join(ROOT, "%s.json" % lang)
    with open(path, "r", encoding="utf-8") as fh:
        data = json.load(fh)
    section = data["setting"].setdefault("trustedProxies", {})
    for key, value in items.items():
        section[key] = value
    with open(path, "w", encoding="utf-8", newline="\n") as fh:
        json.dump(data, fh, ensure_ascii=False, indent=4)
        fh.write("\n")
    print("%-8s keys=%s" % (lang, sorted(section.keys())))

sys.exit(1 if failed else 0)
