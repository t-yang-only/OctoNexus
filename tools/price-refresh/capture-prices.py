#!/usr/bin/env python3
"""定时抓取各上游站点的余额 / 模型价格 / 用量，并导入 octopus（T-price-003）。

- 站点与账号从 sites.json 读（0600，不进 git）；管理面账号也放该文件的 admin 段，
  或用 OCTOPUS_ADMIN_USER / OCTOPUS_ADMIN_PASSWORD 覆盖，避免密码进版本库。
- 抓到的原始快照 POST 到本机管理面的 /api/v1/price/import。
- 站点级失败只记日志不中断其他站点。

两种"没有价格"必须区分开（历史教训：不区分时日志里一片 ERR，真故障被淹没）：
  ① 站点根本没开模型广场 —— /api/v1/settings/public 的 model_plaza_enabled=false，
     这是"没得抓"，不是故障；
  ② 开了但抓取失败 —— 才是真故障。
"""
import json
import os
import sys
import time
import sqlite3
import urllib.error
import urllib.request

PROXY = None

CONF = "/opt/octonexus-deta/tools/sites.json"
ADMIN = "http://127.0.0.1:33100"
LOG = "/opt/octonexus-deta/data/price-refresh.log"

# 不依赖登录即可读的公开开关；用它的 model_plaza_enabled 判断站点有没有价格可抓。
PUBLIC_SETTINGS_PATH = "/api/v1/settings/public"


def resolve_proxy():
    """取一个可用出口节点的本地端口，避免用服务器真实 IP 直连上游。"""
    try:
        conn = sqlite3.connect('/opt/octonexus-deta/data/data.db')
        row = conn.execute(
            'select local_port from proxy_nodes where enabled=1 and local_port>0 '
            'order by id desc limit 1'
        ).fetchone()
        return 'http://127.0.0.1:%d' % row[0] if row else None
    except Exception:
        return None


def log(msg):
    line = "%s %s" % (time.strftime("%Y-%m-%dT%H:%M:%S"), msg)
    print(line)
    with open(LOG, "a", encoding="utf-8") as handle:
        handle.write(line + "\n")


def fetch(url, token=None, data=None, timeout=25):
    headers = {
        'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 '
                      '(KHTML, like Gecko) Chrome/124.0 Safari/537.36',
        'Accept': 'application/json, text/plain, */*',
    }
    if token:
        headers["Authorization"] = "Bearer " + token
    body = None
    if data is not None:
        headers["Content-Type"] = "application/json"
        body = json.dumps(data).encode()
    request = urllib.request.Request(url, data=body, headers=headers)
    opener = (urllib.request.build_opener(
        urllib.request.ProxyHandler({'http': PROXY, 'https': PROXY}))
        if PROXY else urllib.request.build_opener())
    with opener.open(request, timeout=timeout) as response:
        return json.loads(response.read().decode("utf-8", "replace"))


def admin_session(conf):
    """登录本机管理面，拿会话 Cookie。凭据优先取环境变量，其次 sites.json 的 admin 段。"""
    admin = conf.get("admin") or {}
    user = os.environ.get("OCTOPUS_ADMIN_USER") or admin.get("username") or "yang"
    password = os.environ.get("OCTOPUS_ADMIN_PASSWORD") or admin.get("password") or ""
    if not password:
        raise RuntimeError(
            "管理面密码未配置：请在 sites.json 的 admin 段填写，或设置 OCTOPUS_ADMIN_PASSWORD"
        )
    request = urllib.request.Request(
        ADMIN + "/api/v1/user/login",
        data=json.dumps({"username": user, "password": password}).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(request, timeout=15) as response:
        cookie = response.headers.get("Set-Cookie", "")
    return cookie.split(";")[0]


def plaza_enabled_for(base):
    """站点是否开放模型广场。读不到时返回 None（按"未知"处理，仍然尝试抓价格）。"""
    try:
        public = fetch(base + PUBLIC_SETTINGS_PATH).get("data") or {}
        value = public.get("model_plaza_enabled")
        return None if value is None else bool(value)
    except Exception:
        return None


def capture(site):
    base = site["base"].rstrip("/")
    entry = {"site": base, "captured_at": time.strftime("%Y-%m-%dT%H:%M:%S")}
    plaza_enabled = plaza_enabled_for(base)
    entry["plaza_enabled"] = plaza_enabled

    login = fetch(base + "/api/v1/auth/login",
                  data={"email": site["email"], "password": site["password"]})
    token = (login.get("data") or {}).get("access_token", "")
    if not token:
        entry["error"] = "login failed"
        return entry

    for key, path in (("me", "/api/v1/auth/me"),
                      ("plaza", "/api/v1/model-plaza"),
                      ("usage_stats", "/api/v1/usage/dashboard/stats")):
        if key == "plaza" and plaza_enabled is False:
            continue  # 站点没开模型广场：没有价格可抓，不是失败。
        try:
            entry[key] = (fetch(base + path, token=token).get("data"))
        except urllib.error.HTTPError as error:
            entry[key] = {"_status": error.code}
    return entry


def describe(entry):
    """一行摘要：把"没得抓"和"抓失败"在日志里区分开。

    注意 plaza 的形态随站点版本不同：可能是分组数组，也可能是分组字典，
    所以不能拿"是不是 list"当作"有没有抓到"——上一版就因此把 14 条报成了 0。
    """
    if entry.get("error"):
        return "login failed"
    if entry.get("plaza_enabled") is False:
        return "plaza disabled (site has no price page)"
    prices = entry.get("plaza")
    if isinstance(prices, list):
        return "plaza items=%d" % len(prices)
    if isinstance(prices, dict):
        if prices.get("_status"):
            return "plaza ERR(%s)" % prices["_status"]
        return "plaza groups=%d" % len(prices)
    return "plaza none"


def main():
    global PROXY
    PROXY = resolve_proxy()
    log('出口代理: %s' % (PROXY or '(无可用节点)'))
    conf = json.load(open(CONF, encoding="utf-8"))
    sites = conf["sites"]
    cookie = admin_session(conf)
    ok = 0
    for site in sites:
        if PROXY is None:
            log('SKIP %-27s 没有可用出口节点' % site['base'])
            continue
        try:
            entry = capture(site)
            request = urllib.request.Request(
                ADMIN + "/api/v1/price/import",
                data=json.dumps(entry).encode(),
                headers={"Content-Type": "application/json", "Cookie": cookie},
            )
            with urllib.request.urlopen(request, timeout=30) as response:
                result = json.loads(response.read().decode("utf-8", "replace")).get("data") or {}
            log("OK  %-28s %-34s price_rows=%s usage_rows=%s" % (
                site["base"], describe(entry),
                result.get("price_rows"), result.get("usage_rows")))
            ok += 1
        except Exception as error:  # noqa: BLE001
            log("ERR %-28s %s" % (site["base"], str(error)[:120]))
    log("完成：成功 %d / %d" % (ok, len(sites)))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
