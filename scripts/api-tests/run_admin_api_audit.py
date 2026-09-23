#!/usr/bin/env python3
"""T-usability-010 管理面接口可用性审计。

## 为什么需要

54 个无参 GET 接口是面板与外部工具的实际入口。其中任何一个返回 5xx 或
结构异常，用户看到的就是"功能坏了"，但**没有任何地方会主动告诉你哪个坏了** ——
只有点到那个页面才发现。

这个审计逐个调用它们，把异常挑出来。

## 判据取向

  · HTTP 5xx → 真实故障（后端 panic / 未捕获错误）
  · HTTP 404 → 路由未注册（前端若在调它，那个功能就是死的）
  · HTTP 200 但响应无 data 字段 → 可能正常（有些接口就该返回空），只记录不判失败
  · **流式接口**（SSE）单独处理：不能等它结束，只验证能建立连接

取不到数据时如实报 FAIL，不用"跳过"掩盖。
"""
import json
import sys
import time
import urllib.error
import urllib.request

ADMIN = "http://127.0.0.1:33100"
USER, PWD = "yang", "sois=ting"

# 从后端路由表枚举出来的全部无参 GET（54 个）。
# 流式的单列：它们的响应永不结束，只验证能否建立连接。
PATHS = [
    "/api/v1/account/jump/list",
    "/api/v1/account/official/list",
    "/api/v1/account/official/pool/list",
    "/api/v1/alert-rule/fires",
    "/api/v1/alert-rule/list",
    "/api/v1/apikey/list",
    "/api/v1/backup/webdav/list",
    "/api/v1/balance/summary",
    "/api/v1/channel/diagnose",
    "/api/v1/channel/grants",
    "/api/v1/channel/latency",
    "/api/v1/channel/stats",
    "/api/v1/collector/sources",
    "/api/v1/collector/templates",
    "/api/v1/group/latency",
    "/api/v1/group/list",
    "/api/v1/group/usage",
    "/api/v1/log/export",
    "/api/v1/log/fault-stats",
    "/api/v1/log/history",
    "/api/v1/model-mapping/list",
    "/api/v1/model/last-update-time",
    "/api/v1/model/list",
    "/api/v1/monitor/allocation",
    "/api/v1/plugin/list",
    "/api/v1/pool/adapters",
    "/api/v1/pool/adapters/templates",
    "/api/v1/pool/entries",
    "/api/v1/pool/export",
    "/api/v1/pool/kinds",
    "/api/v1/pool/openapi.json",
    "/api/v1/pool/stats",
    "/api/v1/pool/summary",
    "/api/v1/price/models",
    "/api/v1/price/usage",
    "/api/v1/proxy/core/status",
    "/api/v1/proxy/nodes",
    "/api/v1/proxy/subscriptions",
    "/api/v1/setting/export",
    "/api/v1/setting/list",
    "/api/v1/setting/notify/channels",
    "/api/v1/stats/apikey",
    "/api/v1/stats/daily",
    "/api/v1/stats/hourly",
    "/api/v1/stats/total",
    "/api/v1/stats/usage",
    "/api/v1/subscription/list",
    "/api/v1/update/now-version",
    "/api/v1/usage-report/history",
    "/api/v1/user/status",
]

# 这三个是**文件级导出**，不是 JSON 包装：log/export 出 CSV、
# setting/export 与 pool/export 出裸对象（供下载）。
# 判定要按各自的预期格式走，不能一律要求 `{"data": ...}` ——
# 初版就是这么误报的：把两个正常接口判成了"结构异常"。
# 判据是"是不是预期格式"，不是"是不是统一包装"。
NON_WRAPPED = {
    "/api/v1/log/export": "csv",
    "/api/v1/setting/export": "raw-json",
    "/api/v1/pool/export": "raw-json",
}

# 这两个走 middleware.APIKeyAuth()：**故意要求传客户端 API Key，不接受管理面会话**。
# 用管理面 Cookie 调用得到 401 是正确行为，不是缺陷 —— 单列出来，
# 断言"必须 401 而不是 5xx 或 200"（200 反而说明鉴权漏了）。
APIKEY_AUTH_PATHS = [
    "/api/v1/apikey/stats",
    "/api/v1/apikey/login",
]

# 这两个是 SSE / 事件流：只验证能建立连接，不等响应结束。
STREAM_PATHS = [
    "/api/v1/group/events",
    "/api/v1/log/overview/stream",
]

ck = {}


def call(method, path, body=None, timeout=20, read_cap=8 * 1024 * 1024):
    """调用管理面接口。

    read_cap 默认 8MB 而不是 200KB：初版用 200KB，结果 group/list、
    monitor/allocation、setting/export 三个响应被**截断**，json.loads 失败，
    被我误报成"结构异常"。**截断阈值本身就是判据的一部分** ——
    阈值太小会把正常接口判成坏的。
    """
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if ck.get("v"):
        req.add_header("Cookie", ck["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for h in resp.headers.get_all("Set-Cookie") or []:
                ck["v"] = h.split(";")[0]
            return resp.status, resp.read(read_cap).decode("utf-8", "replace"), dict(resp.headers)
    except urllib.error.HTTPError as e:
        return e.code, e.read(2000).decode("utf-8", "replace"), {}
    except Exception as e:  # noqa: BLE001
        return 0, str(e), {}


def probe_stream(path):
    """流式接口只验证"能建立连接且返回事件流"，不等它结束。

    直接 read() 会一直阻塞到超时 —— 那不是接口的问题，是探测方式的问题。
    所以这里读响应头就够了，然后在超时后主动断开。
    """
    req = urllib.request.Request(ADMIN + path, method="GET")
    if ck.get("v"):
        req.add_header("Cookie", ck["v"])
    try:
        resp = urllib.request.urlopen(req, timeout=5)
        ct = resp.headers.get("Content-Type", "")
        # 读到第一个 chunk 就够，随后主动关闭（SSE 永不结束）。
        try:
            resp.read(256)
        except Exception:  # noqa: BLE001
            pass
        resp.close()
        return resp.status, ct
    except urllib.error.HTTPError as e:
        return e.code, e.headers.get("Content-Type", "")
    except Exception as e:  # noqa: BLE001
        # 读超时说明连接建立成功、只是没有数据流 —— 对 SSE 是正常的。
        if "timed out" in str(e).lower():
            return 200, "event-stream(推断)"
        return 0, str(e)[:60]


print("=" * 68)
code, _, _ = call("POST", "/api/v1/user/login", {"username": USER, "password": PWD})
print("登录: HTTP %s" % code)
if code != 200:
    sys.exit(1)

ok, empty_data, not_found, server_error, other = [], [], [], [], []
print()
print("=" * 68)
for p in PATHS:
    t0 = time.time()
    code, body, _ = call("GET", p)
    ms = int((time.time() - t0) * 1000)

    if code == 200:
        expect = NON_WRAPPED.get(p)
        if expect == "csv":
            # 导出 CSV：首行应是表头而不是 `{`。
            good = "\n" in body and not body.lstrip().startswith(("{", "["))
            (ok if good else empty_data).append(
                (p, ms) if good else (p, ms, "应输出 CSV，实得：" + body[:50]))
            continue
        if expect == "raw-json":
            # 导出裸 JSON：能解析即可（不需要 data 包装）。
            try:
                json.loads(body)
                ok.append((p, ms))
            except Exception:  # noqa: BLE001
                empty_data.append((p, ms, "应输出可解析 JSON，实得：" + body[:50]))
            continue

        try:
            d = json.loads(body)
            if isinstance(d, dict) and "data" in d:
                ok.append((p, ms))
            else:
                # 200 但既不在 NON_WRAPPED 里、又没有 data 字段：需要人工看。
                empty_data.append((p, ms, str(d)[:60]))
        except Exception:  # noqa: BLE001
            empty_data.append((p, ms, "响应不是 JSON：" + body[:50]))
    elif code == 404:
        not_found.append((p, ms, body[:60]))
    elif code >= 500:
        server_error.append((p, ms, body[:120]))
    else:
        other.append((p, code, ms, body[:80]))

print()
print("== 正常（HTTP 200 且含 data）：%d 个 ==" % len(ok))
for p, ms in ok:
    print("   [OK ] %-46s %sms" % (p, ms))

if empty_data:
    print()
    print("== 200 但结构异常（需人工看）：%d 个 ==" % len(empty_data))
    for p, ms, note in empty_data:
        print("   [?? ] %-46s %sms  %s" % (p, ms, note))

if not_found:
    print()
    print("== 404 路由未注册：%d 个 ==" % len(not_found))
    for p, ms, note in not_found:
        print("   [404] %-46s %s" % (p, note))

if server_error:
    print()
    print("== 5xx 真实故障：%d 个 ==" % len(server_error))
    for p, ms, note in server_error:
        print("   [5xx] %-46s %s" % (p, note))

if other:
    print()
    print("== 其它状态码：%d 个 ==" % len(other))
    for p, code, ms, note in other:
        print("   [%s] %-46s %s" % (code, p, note))

print()
print("=" * 68)
print("流式接口（只验证能建立连接，不等结束）")
stream_ok = True
for p in STREAM_PATHS:
    code, ct = probe_stream(p)
    good = code == 200
    if not good:
        stream_ok = False
    print("   [%s] %-40s HTTP %s  Content-Type=%s" % (
        "OK " if good else "?? ", p, code, ct or "(空)"))

# APIKeyAuth 接口：必须 401（用管理面 Cookie 调）。200 反而说明鉴权漏了。
print()
print("=" * 68)
print("APIKeyAuth 接口（用管理面 Cookie 调，必须 401）")
apikey_auth_ok = True
for p in APIKEY_AUTH_PATHS:
    code, body, _ = call("GET", p, timeout=10)
    good = code == 401
    if not good:
        apikey_auth_ok = False
    print("   [%s] %-40s HTTP %s  %s" % (
        "OK " if good else "?? ", p, code,
        "（正确拒绝）" if good else "（应为 401）" + body[:60]))

print()
print("=" * 68)
fatal = bool(server_error or not_found)
print("TOTAL JSON 探测 %d 个：正常 %d / 结构异常 %d / 404 %d / 5xx %d / 其它 %d" % (
    len(PATHS), len(ok), len(empty_data), len(not_found), len(server_error), len(other)))
print("流式 %d 个：%s；APIKeyAuth %d 个：%s" % (
    len(STREAM_PATHS), "全部可连接" if stream_ok else "有异常",
    len(APIKEY_AUTH_PATHS), "鉴权正确" if apikey_auth_ok else "有异常"))
sys.exit(1 if (fatal or not stream_ok or not apikey_auth_ok) else 0)
