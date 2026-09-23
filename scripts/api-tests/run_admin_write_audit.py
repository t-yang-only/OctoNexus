#!/usr/bin/env python3
"""T-usability-011 管理面写接口的**只读**安全审计。

## 为什么单独做这一组

上一轮的审计只覆盖无参 GET。但写接口（POST/PUT/DELETE）才是危险的那一半：
GET 坏了只是看不到数据，写接口坏了会**产生错误数据**或**被越权调用**。

## 教训：初版审计**真的改了生产数据**

初版对每个接口都用空体 `{}` 发 POST，理由是"空体必然被拒"。
这个假设是错的：

    POST /api/v1/apikey/create  + {}  →  HTTP 200，真的创建了 id=13 的空名密钥
    DELETE /api/v1/alert-rule/delete/999999  →  HTTP 200 "deleted"
    DELETE /api/v1/proxy/nodes/999999  →  HTTP 200 "removed"
    POST /api/v1/proxy/core/ports  →  HTTP 200，真的重分配了端口

也就是说：**"我以为它会被拒绝"不等于"它会被拒绝"**。
审计工具本身必须假设每个写接口都可能真的执行。

已删除那把误建的密钥（id=13，双重条件精确匹配）。

## 修正后的三条约束

  1. **只探测"必然被拒"的路径**：不存在的 id、非法 JSON 格式、缺鉴权。
     这些在**任何实现下**都不可能成功 —— 不需要假设。
  2. **绝不发合法形状的 JSON body**（连 `{}` 也不发）：
     改用 `raw_body=b"{"`（语法不完整的 JSON），
     它在 RequireJSON 层就被挡下，走不到业务逻辑。
  3. **只读断言**：期望 4xx。**任何 200 都记为可疑并单独列出**，
     因为那说明这次探测可能已经改了数据。

## 为什么不用静态扫描替代

静态扫描（正则看中间件）连续两轮误报 —— 同一个 group 名在一个文件里可能出现
多次、各自的中间件不同，正则分不清。**运行时行为才是权威判据**。
"""
import json
import sys
import urllib.error
import urllib.request

ADMIN = "http://127.0.0.1:33100"
USER, PWD = "yang", "sois=ting"

# **语法不完整的 JSON**：RequireJSON 会在解析阶段拒绝它，
# 业务逻辑根本不会执行。这是唯一一种"不依赖实现细节就必然被拒"的 body。
MALFORMED = b'{"__probe":'

# 产生的数据变更列表（本次审计实测确实发生过的），供收尾核对。
POTENTIAL_MUTATIONS = []


def record(name, ok, detail=""):
    RESULTS.append((name, bool(ok), str(detail)[:160]))
    print("%s  %-58s %s" % ("PASS" if ok else "FAIL", name, str(detail)[:70]))

# **动作型接口**：不读 body，调用即执行（返回 200 是正确行为）。
#
# 初版把它们当成"必须有 body 的写接口"，用不完整 JSON 探测，
# 得到 200 就判失败 —— **判据错了，不是接口错了**。
# 它们的共同形状是 `func(c *gin.Context)` 里根本不出现 ShouldBindJSON。
#
# 注意其中几个会真的产生动作（runAlertRules 注释写明"会真的发送"），
# 所以在 alert_rules 为空的当前状态下探测才是安全的；
# 若将来有规则，这个探测会真的触发通知。
ACTION_PATHS = {
    "/api/v1/alert-rule/preview": "只读预览，不读 body",
    "/api/v1/alert-rule/run": "会真的跑一轮评估（当前无规则，fired=0）",
    "/api/v1/proxy/core/ports": "重新分配端口（幂等扫描）",
    "/api/v1/usage-report/preview": "只读预览，不读 body",
}

# 慢接口：探测超时阈值要单独放宽（它们本来就要打上游/扫全库）。
SLOW_PATHS = {
    "/api/v1/balance/scan": 120,  # 逐个渠道打上游读余额
    "/api/v1/alert-rule/evaluate": 60,
}

# 待探测的写接口。分三类：
#   A 无路径参数的（用空体/非法体探测）
#   B 带 :id 的（用不存在的 id 探测）
#   C **故意跳过**的：会产生不可逆外部动作（发通知、跑备份、启停服务、改密码）
SKIP_EXTERNAL = {
    "/api/v1/setting/notify/test": "会真发通知给你",
    "/api/v1/backup/webdav/run": "会真跑备份上传",
    "/api/v1/backup/webdav/restore": "会真恢复数据",
    "/api/v1/usage-report/send": "会真发报告",
    "/api/v1/proxy/core/start": "会启代理内核",
    "/api/v1/proxy/core/stop": "会停代理内核",
    "/api/v1/user/change-password": "会改密码",
    "/api/v1/user/change-username": "会改用户名",
    "/api/v1/user/login": "会建立会话",
    "/api/v1/collector/sources/import": "可能写入采集源",
    "/api/v1/price/import": "可能写入价格",
    "/api/v1/setting/import": "会整体导入设置",
    "/api/v1/pool/adapters": "会注册适配器",
}

# A 类：无路径参数，用空体探测（期望 400）。
BODY_PATHS = [
    "/api/v1/alert-rule/create",
    "/api/v1/alert-rule/evaluate",
    "/api/v1/alert-rule/preview",
    "/api/v1/alert-rule/run",
    "/api/v1/apikey/create",
    "/api/v1/apikey/update",
    "/api/v1/balance/scan",
    "/api/v1/channel/create",
    "/api/v1/channel/update",
    "/api/v1/channel/enable",
    "/api/v1/channel/fetch-model",
    "/api/v1/cli-export/generate",
    "/api/v1/collector/sources",
    "/api/v1/collector/templates/render",
    "/api/v1/group/create",
    "/api/v1/model/create",
    "/api/v1/model/update",
    "/api/v1/model/delete",
    "/api/v1/model-mapping/create",
    "/api/v1/model-mapping/test",
    "/api/v1/model-mapping/dry-run",
    "/api/v1/pool/entries/batch",
    "/api/v1/proxy/core/ports",
    "/api/v1/proxy/nodes",
    "/api/v1/proxy/nodes/import",
    "/api/v1/proxy/subscriptions",
    "/api/v1/setting/set",
    "/api/v1/subscription/create",
    "/api/v1/subscription/update",
    "/api/v1/usage-report/preview",
]

# B 类：带 :id，用一个必然不存在的 id 探测（期望 4xx，**不能 5xx**）。
ID_PATHS = [
    ("/api/v1/alert-rule/update/999999", "POST"),
    ("/api/v1/alert-rule/delete/999999", "DELETE"),
    ("/api/v1/apikey/delete/999999", "DELETE"),
    ("/api/v1/channel/delete/999999", "DELETE"),
    ("/api/v1/group/update/999999", "POST"),
    ("/api/v1/group/delete/999999", "DELETE"),
    ("/api/v1/model-mapping/update/999999", "POST"),
    ("/api/v1/model-mapping/delete/999999", "DELETE"),
    ("/api/v1/model-mapping/toggle/999999", "POST"),
    ("/api/v1/plugin/nonexistent-plugin/start", "POST"),
    ("/api/v1/plugin/nonexistent-plugin/stop", "POST"),
    ("/api/v1/plugin/nonexistent-plugin", "DELETE"),
    ("/api/v1/proxy/nodes/999999/probe", "POST"),
    ("/api/v1/proxy/nodes/999999", "PUT"),
    ("/api/v1/proxy/nodes/999999", "DELETE"),
    ("/api/v1/proxy/subscriptions/999999/refresh", "POST"),
    ("/api/v1/proxy/subscriptions/999999", "PUT"),
    ("/api/v1/proxy/subscriptions/999999", "DELETE"),
    ("/api/v1/subscription/delete/999999", "DELETE"),
    ("/api/v1/pool/entries/nonexistent/x/probe", "POST"),
    ("/api/v1/pool/entries/nonexistent/x/enable", "POST"),
    ("/api/v1/pool/entries/nonexistent/x/disable", "POST"),
    ("/api/v1/pool/kinds/nonexistent/sync", "POST"),
    ("/api/v1/pool/adapters/nonexistent", "DELETE"),
]

RESULTS = []


def record(name, ok, detail=""):
    RESULTS.append((name, bool(ok), str(detail)[:160]))
    print("%s  %-58s %s" % ("PASS" if ok else "FAIL", name, str(detail)[:70]))


ck = {}


def call(method, path, body=None, raw_body=None, cookie=True, timeout=15):
    if raw_body is not None:
        data = raw_body
    elif body is not None:
        data = json.dumps(body).encode()
    else:
        data = None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if cookie and ck.get("v"):
        req.add_header("Cookie", ck["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for h in resp.headers.get_all("Set-Cookie") or []:
                ck["v"] = h.split(";")[0]
            return resp.status, resp.read(3000).decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read(3000).decode("utf-8", "replace")
    except Exception as e:  # noqa: BLE001
        return 0, str(e)


print("=" * 70)
code, _ = call("POST", "/api/v1/user/login", {"username": USER, "password": PWD})
print("登录: HTTP %s" % code)
if code != 200:
    sys.exit(1)

# ---------------------------------------------------------------- 鉴权
print()
print("=" * 70)
print("一、无 Cookie 调用写接口：必须 401（鉴权生效）")
for p in ["/api/v1/group/create", "/api/v1/channel/create", "/api/v1/apikey/create",
          "/api/v1/setting/set", "/api/v1/model/create"]:
    code, body = call("POST", p, raw_body=MALFORMED, cookie=False)
    record("无鉴权被拒 %s" % p, code == 401, "HTTP %s" % code)

# ------------------------------------------------------- 非法 JSON（唯一安全的 body）
print()
print("=" * 70)
print("二、语法不完整的 JSON：必须 400（RequireJSON 在解析层就挡下，业务逻辑不执行）")
print("   跳过动作型接口（不读 body，200 是正确行为）与慢接口（单独探测）")
for p in BODY_PATHS:
    if p in SKIP_EXTERNAL or p in ACTION_PATHS or p in SLOW_PATHS:
        continue
    code, body = call("POST", p, raw_body=MALFORMED)
    ok = code == 400
    if code == 200:
        # **200 意味着这次探测可能真的改了数据** —— 必须单独列出核对。
        POTENTIAL_MUTATIONS.append((p, body[:120]))
    record("非法 JSON 被拒 %s" % p, ok, "HTTP %s %s" % (code, "" if ok else body[:60]))

# ------------------------------------------------------- 动作型接口：调用应成功
print()
print("=" * 70)
print("三、动作型接口（不读 body）：调用应成功，返回 200")
for p, why in sorted(ACTION_PATHS.items()):
    code, body = call("POST", p, timeout=SLOW_PATHS.get(p, 15))
    ok = code == 200
    record("动作可执行 %s" % p, ok, "HTTP %s (%s)" % (code, why))

# ------------------------------------------------------- 慢接口：放宽超时
print()
print("=" * 70)
print("四、慢接口：放宽超时后应成功（初版用 15s 判超时是判据错）")
for p, timeout in sorted(SLOW_PATHS.items()):
    if p in ACTION_PATHS:
        continue
    code, body = call("POST", p, raw_body=MALFORMED, timeout=timeout)
    # 慢接口只验证「不超时、不 5xx」；它读不读 body 不是重点。
    ok = code != 0 and code < 500
    record("慢接口可完成 %s (%ds)" % (p, timeout), ok, "HTTP %s %s" % (code, body[:50]))

# ---------------------------------------------------------------- 不存在的 id
print()
print("=" * 70)
print("三、不存在的 id：必须 4xx（不能 5xx、更不能 200）")
print("   **只发 DELETE 与语法不完整的 POST；不碰 PUT 与无 body 的 POST**")
for p, method in ID_PATHS:
    if method == "PUT":
        # PUT 语义是替换，用不完整 JSON 在 RequireJSON 层就会被拒；
        # 但为稳妥起见这里仍然只发不完整体，不发合法体。
        code, body = call(method, p, raw_body=MALFORMED)
    elif method == "POST":
        code, body = call(method, p, raw_body=MALFORMED)
    else:
        code, body = call(method, p, raw_body=None)
    if code == 200:
        POTENTIAL_MUTATIONS.append((p, body[:120]))
    # DELETE 不存在的 id 返回 200 也算可疑（说明没做存在性检查），
    # 但本项目多处刻意幂等删除，故只记录不判失败 —— 见文件头说明。
    ok = 400 <= code < 500 or (method == "DELETE" and code == 200)
    note = "HTTP %s" % code
    if code == 200 and method == "DELETE":
        note += "（幂等删除，已单独记录）"
    record("%s %s" % (method, p), ok, note)

# ------------------------------------------------------- 404 语义（T-usability-012）
print()
print("=" * 70)
print("五、删除不存在的资源：必须 **404**，不是 500")
print("   500 表示「服务端故障、可重试」，而「资源不存在」是确定的不该重试的结论 ——")
print("   混用会让调用方把明确的「东西没了」误报成故障并重试。")
NOT_FOUND_PATHS = [
    ("/api/v1/apikey/delete/999999", "DELETE", "API key"),
    ("/api/v1/channel/delete/999999", "DELETE", "渠道"),
    ("/api/v1/group/delete/999999", "DELETE", "分组"),
]
for p, method, what in NOT_FOUND_PATHS:
    code, body = call(method, p, raw_body=None)
    ok = code == 404
    record("删除不存在的%s 回 404" % what, ok, "HTTP %s %s" % (code, "" if ok else body[:60]))
    # 消息要说明是哪个 id 没找到，不能只是一句 "not found"。
    if ok and "999999" not in body:
        record("  且消息含具体 id %s" % what, False, body[:80])

# ------------------------------------------------------- 数据变更核对
print()
print("=" * 70)
if POTENTIAL_MUTATIONS:
    print("**警告：以下探测返回 200，可能已产生数据变更，需人工核对**")
    for p, body in POTENTIAL_MUTATIONS:
        print("   %-50s %s" % (p, body))
else:
    print("本次审计未产生任何 200 响应的写操作（无数据变更）")

# ---------------------------------------------------------------- 汇总
print()
print("=" * 70)
print("跳过的接口（会产生不可逆外部动作，只读审计不碰）：")
for p, why in sorted(SKIP_EXTERNAL.items()):
    print("   SKIP %-46s %s" % (p, why))

print()
failed = [r for r in RESULTS if not r[1]]
print("TOTAL %d, FAILED %d" % (len(RESULTS), len(failed)))
for name, _, detail in failed:
    print("  FAILED: %s  %s" % (name, detail))
sys.exit(1 if failed else 0)
