#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""总余额聚合（T-balance-001）活体套件 —— 只用本地 mock 上游与本地实例。

口径（internal/op/balance.go 顶部注释）:
- 总余额 = 各渠道「最近一次读到的剩余额度」按 balance_points_per_unit 折成货币值后求和;
- 未读到余额的渠道只计入 unknown_channels, 绝不计入总额（否则「还没扫到」会被显示成「没钱了」）;
- 剩余次数 = 包月额度 - 已用, 未配包月的渠道不计入;
- 客户端凭据额度 = MaxCost 上限与累计已花, 只作为明细, 不计入上游总余额。

用例:
  B1 管理面 /api/v1/balance/summary 需要登录（未登录 401）
  B2 号池刷新把站点余额写进快照后, 总额按配置口径折算（含 Known/Unknown 计数）
  B3 换算口径可改（balance_points_per_unit 生效）, 坏值被拒（400）
  B4 标准协议端点 /v1/dashboard/billing/subscription + /usage 可用调用模型的那把 Key 查
  B5 标准协议端点的余额语义自洽: 订阅额度 - 用量 = 总余额
  B6 未带 Key 的标准协议查询被拒（401）
"""

import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY = "http://%s:%s" % (os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1"),
                          os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))
MOCK = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099")

STATION_NAME = "DS-TEST-balance"
STATION_KEY = "sk-balance-station"

COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:220])


def call(method, path, payload=None, timeout=60, with_cookie=True, headers=None, base=ADMIN):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(base + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if with_cookie and COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    for key, value in (headers or {}).items():
        req.add_header(key, value)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for header, value in resp.getheaders():
                if header.lower() == "set-cookie":
                    COOKIE["v"] = value.split(";")[0]
            body = resp.read().decode()
            return resp.status, (json.loads(body) if body else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode()
        try:
            return e.code, json.loads(raw)
        except ValueError:
            return e.code, {"_raw": raw[:200]}
    except Exception as e:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(e).__name__, e)}


def db():
    return sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)


def relay_key():
    c = db()
    row = c.execute("select api_key from api_keys where enabled=1 order by id limit 1").fetchone()
    c.close()
    return row[0]


def set_setting(key, value):
    return call("POST", "/api/v1/setting/set", {"key": key, "value": value})


def read_setting(key):
    status, body = call("GET", "/api/v1/setting/list")
    if status != 200:
        return None
    for item in body.get("data") or []:
        if item.get("key") == key:
            return item.get("value")
    return None


def ensure_station_channel():
    """建一条指向本地 mock 的渠道, 复用 mock 的 /api/user/self 余额桩（quota 500 / used 120 → 剩余 380）。"""
    status, body = call("GET", "/api/v1/channel/stats")
    for item in (body.get("data") or []):
        if item.get("channel_name") == STATION_NAME:
            return item.get("channel_id"), False
    status, body = call("POST", "/api/v1/channel/create", {
        "name": STATION_NAME,
        "dialect": "generic",
        "base_url": MOCK,
        "enabled": True,
        "models": ["mock-good"],
        "keys": [{"name": "k1", "key": STATION_KEY, "enabled": True}],
        "grants": [{"model_name": "mock-good", "key_name": "k1", "protocols": 2}],
    })
    if status == 200:
        detail = body.get("data") or {}
        return detail.get("id"), True
    print("建渠道失败:", status, json.dumps(body)[:200])
    return None, False


def pool_refresh(channel_id):
    """经号池刷新该渠道凭据余额（站点 /api/user/self）→ 写入渠道余额快照。"""
    status, body = call("GET", "/api/v1/pool/entries?kind=channel")
    items = (body.get("data") or {}).get("items") or []
    entry = None
    for item in items:
        if isinstance(item, dict) and str(item.get("id", "")).startswith("%d:" % channel_id):
            entry = item
            break
    if entry is None:
        return 0, {"_raw": "号池里找不到该渠道的条目（items=%d）" % len(items)}
    return call("POST", "/api/v1/pool/entries/channel/%s/refresh" % entry.get("id"))


def main():
    status, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    if status != 200:
        print("登录失败, 无法继续:", status)
        return 1

    # 夹具纪律：本套件会改换算口径设置，跑完必须复原成缺省（500000 / USD），
    # 否则后续套件看到的余额折算口径会带着本套件的痕迹。
    try:
        return run_cases()
    finally:
        set_setting("balance_points_per_unit", "500000")
        set_setting("balance_currency", "USD")


def run_cases():
    # B1 未登录不得读余额
    status, _ = call("GET", "/api/v1/balance/summary", with_cookie=False)
    record("B1 管理面余额需登录", status == 401, "HTTP %d" % status)

    channel_id, created = ensure_station_channel()
    record("B0 余额测试渠道就绪", channel_id is not None,
           "channel_id=%s created=%s" % (channel_id, created))
    if channel_id is None:
        return 1

    # 先把换算口径设成 100000, 便于观察折算（380 点 / 100000 = 0.0038）
    set_setting("balance_points_per_unit", "100000")
    set_setting("balance_currency", "USD")

    refresh_status, refresh_body = pool_refresh(channel_id)
    record("B2a 号池刷新站点余额成功", refresh_status == 200 and (refresh_body.get("data") or {}).get("detail", {}).get("balance_remaining") == 380,
           "HTTP %d %s" % (refresh_status, json.dumps(refresh_body)[:160]))

    status, body = call("GET", "/api/v1/balance/summary")
    data = body.get("data") or {}
    rows = {row.get("channel_id"): row for row in (data.get("channels") or [])}
    mine = rows.get(channel_id) or {}
    record("B2b 渠道余额进入快照并按口径折算",
           status == 200 and mine.get("known") is True and abs((mine.get("balance") or 0) - 0.0038) < 1e-9,
           "HTTP %d known=%s remaining=%s balance=%s" % (status, mine.get("known"), mine.get("remaining"), mine.get("balance")))
    record("B2c 总额只累加已知余额",
           (data.get("known_channels") or 0) >= 1 and (data.get("total") or 0) >= mine.get("balance", 0),
           "total=%s known=%s unknown=%s" % (data.get("total"), data.get("known_channels"), data.get("unknown_channels")))
    record("B2d 口径字段回显", data.get("points_per_unit") == 100000 and data.get("currency") == "USD",
           "points_per_unit=%s currency=%s" % (data.get("points_per_unit"), data.get("currency")))

    # B3 口径可改 + 坏值被拒
    set_setting("balance_points_per_unit", "500000")
    status2, body2 = call("GET", "/api/v1/balance/summary")
    data2 = body2.get("data") or {}
    rows2 = {row.get("channel_id"): row for row in (data2.get("channels") or [])}
    mine2 = rows2.get(channel_id) or {}
    record("B3a 改口径后折算随之变化",
           abs((mine2.get("balance") or 0) - 0.00076) < 1e-9,
           "balance=%s (500000 口径)" % mine2.get("balance"))
    bad_status, bad_body = set_setting("balance_points_per_unit", "0")
    record("B3b 非正口径被拒", bad_status == 400, "HTTP %d %s" % (bad_status, json.dumps(bad_body)[:120]))
    bad_status, bad_body = set_setting("balance_currency", "")
    record("B3c 空货币名被拒", bad_status == 400, "HTTP %d %s" % (bad_status, json.dumps(bad_body)[:120]))
    set_setting("balance_points_per_unit", "100000")
    set_setting("balance_currency", "USD")

    # B4 标准协议端点：用调用模型的那把 Key 直接查
    key = relay_key()
    headers = {"Authorization": "Bearer " + key}
    status, sub = call("GET", "/v1/dashboard/billing/subscription", with_cookie=False, headers=headers, base=RELAY)
    sub_data = sub.get("data") or sub
    record("B4a 标准协议订阅端点可用",
           status == 200 and (sub_data.get("hard_limit_usd") or 0) > 0,
           "HTTP %d hard_limit_usd=%s" % (status, sub_data.get("hard_limit_usd")))
    status, usage = call("GET", "/v1/dashboard/billing/usage", with_cookie=False, headers=headers, base=RELAY)
    usage_data = usage.get("data") or usage
    record("B4b 标准协议用量端点可用",
           status == 200 and usage_data.get("total_usage") is not None,
           "HTTP %d total_usage=%s" % (status, usage_data.get("total_usage")))
    status, detail = call("GET", "/v1/balance", with_cookie=False, headers=headers, base=RELAY)
    detail_data = detail.get("data") or detail
    record("B4c 自有余额端点回明细",
           status == 200 and isinstance(detail_data.get("channels"), list) and len(detail_data.get("keys") or []) == 1,
           "HTTP %d channels=%d keys=%d" % (status, len(detail_data.get("channels") or []), len(detail_data.get("keys") or [])))

    # B5 语义自洽：订阅额度 - 用量 = 总余额（客户端就是这么算的）
    limit = sub_data.get("hard_limit_usd") or 0
    used = (usage_data.get("total_usage") or 0) / 100.0
    total_now = detail_data.get("total") or 0
    record("B5 订阅-用量=总余额",
           abs((limit - used) - total_now) < 1e-6,
           "limit=%s used=%s total=%s" % (limit, used, total_now))

    # B6 未带 Key 被拒
    status, _ = call("GET", "/v1/dashboard/billing/subscription", with_cookie=False, base=RELAY)
    record("B6 标准协议余额需 Key", status == 401, "HTTP %d" % status)

    failed = [item for item in RESULTS if not item[1]]
    print("\nTOTAL %d, FAILED %d" % (len(RESULTS), len(failed)))
    for name, _, detail in failed:
        print("  FAIL", name, detail)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
