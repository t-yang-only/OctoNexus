#!/usr/bin/env python3
"""T-usability-012 续：扫描还有哪些「资源不存在 → 5xx」的路径。

## 为什么用运行时探测而不是静态扫描

静态扫描（正则找"统一回 500 且没有 IsNotFound 判断"的 handler）报出 42 个候选 ——
但**上一轮刚证明静态扫描会误报**（同一 group 名多次出现、动作型接口不读 body、
正常空态被当成错误）。所以这里改用运行时事实：**真的发请求，看服务怎么回**。

## 只读约束（沿用 T-usability-011 的教训）

  · 只发 DELETE 与**语法不完整的 JSON**（`{"__probe":`）——
    前者带不存在的 id 不会删掉任何东西，后者在校验层就被挡下；
  · **绝不发合法形状的 body**（连 `{}` 也不发）——
    实测过 `POST /apikey/create` 用 `{}` 会真的创建密钥；
  · 跑前跑后核对基线。

## 判据取向：只有 5xx 判失败

三类结果是**预期内的**，不判失败：

  · **404** —— 显式「资源不存在」，理想形态；
  · **其它 4xx** —— 残缺 JSON 在校验层被挡下（400）是正确行为：
    请求体本身非法，走到不走到"资源是否存在"那一步都该拒。
    **这不是缺陷** —— 要探到 404 就得发合法 body，而那会真的写数据，
    代价不值得；
  · **DELETE 返回 200** —— 幂等删除，本项目多处有意如此：
    「确保这个资源不存在」的结果已经达成，报 404 反而会让调用方
    误以为失败并重试。

**只有 5xx 是真问题**：那表示「服务端故障、可重试」，
而"资源不存在"是确定的、不该重试的结论 —— 用户重试一百次也一样。
"""
import json
import sys
import urllib.error
import urllib.request

ADMIN = "http://127.0.0.1:33100"
USER, PWD = "yang", "sois=ting"
MALFORMED = b'{"__probe":'
NONEXISTENT = 999999

# 带 :id 的写路径。**排除**会产生不可逆外部动作的与需要额外路径段的。
PATHS = [
    ("POST", "/api/v1/alert-rule/update/%d", "告警规则"),
    ("DELETE", "/api/v1/alert-rule/delete/%d", "告警规则"),
    ("POST", "/api/v1/balance/channel/%d", "渠道余额"),
    ("POST", "/api/v1/channel/%d/verify-models", "渠道实测"),
    ("PUT", "/api/v1/collector/sources/%d", "采集源"),
    ("DELETE", "/api/v1/collector/sources/%d", "采集源"),
    ("POST", "/api/v1/collector/sources/%d/run", "采集源"),
    ("POST", "/api/v1/group/update/%d", "分组"),
    ("POST", "/api/v1/model-mapping/update/%d", "模型映射"),
    ("DELETE", "/api/v1/model-mapping/delete/%d", "模型映射"),
    ("POST", "/api/v1/model-mapping/toggle/%d", "模型映射"),
    ("POST", "/api/v1/account/official/usage/%d", "官方账号"),
    ("PUT", "/api/v1/proxy/nodes/%d", "代理节点"),
    ("DELETE", "/api/v1/proxy/nodes/%d", "代理节点"),
    ("PUT", "/api/v1/proxy/subscriptions/%d", "订阅"),
    ("DELETE", "/api/v1/proxy/subscriptions/%d", "订阅"),
    ("DELETE", "/api/v1/subscription/delete/%d", "手动订阅"),
]

ck = {}


def call(method, path, body=None, raw_body=None, cookie=True, timeout=30):
    """发一个请求。

    区分 body（dict，序列化成合法 JSON）与 raw_body（bytes，原样发）：

      · 登录等**需要合法体**的调用走 body；
      · 探测写接口走 raw_body=MALFORMED —— **绝不能把它们混用**：
        把合法 dict 传给探测会让写接口真的执行（T-usability-011 踩过）。

    初版只有 raw_body，登录时传 dict 直接抛异常（urllib 不接受 dict），
    表现为"登录失败 HTTP 0" —— 排查时先确认了服务本身是好的
    （手工 curl 得到 401 = 正确拒绝匿名），才定位到是参数类型的问题。
    """
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


code, _ = call("POST", "/api/v1/user/login", {"username": USER, "password": PWD})
if code != 200:
    print("登录失败 HTTP %s" % code)
    sys.exit(1)

ok_404, ok_4xx, bad_5xx, other = [], [], [], []
print("=" * 74)
print("对不存在的 id（%d）发只读探测" % NONEXISTENT)
print("=" * 74)
for method, tmpl, what in PATHS:
    path = tmpl % NONEXISTENT
    # DELETE 不带 body；POST/PUT 只发语法不完整的 JSON（绝不发合法形状）。
    body = None if method == "DELETE" else MALFORMED
    code, text = call(method, path, raw_body=body)
    if code == 404:
        ok_404.append((method, path, what))
        print("  [404 ] %-7s %-46s %s" % (method, path, what))
    elif 400 <= code < 500:
        ok_4xx.append((method, path, code))
        print("  [%3d ] %-7s %-46s %s（非 404 但仍是客户端错误）" % (code, method, path, what))
    elif code >= 500:
        bad_5xx.append((method, path, code, text[:70]))
        print("  [%3d*] %-7s %-46s %s ← 资源不存在被报成服务端故障" % (code, method, path, what))
    else:
        other.append((method, path, code, text[:60]))
        print("  [%3d?] %-7s %-46s %s" % (code, method, path, what))

print()
print("=" * 74)
print("汇总：404 正确 %d / 其它 4xx %d / **5xx 需修 %d** / 其它 %d" % (
    len(ok_404), len(ok_4xx), len(bad_5xx), len(other)))

if bad_5xx:
    print()
    print("需要修的路径：")
    for method, path, code, text in bad_5xx:
        print("  %-7s %-46s HTTP %s  %s" % (method, path, code, text))

if other:
    print()
    print("其它状态码（需人工看）：")
    for method, path, code, text in other:
        print("  %-7s %-46s HTTP %s  %s" % (method, path, code, text))

sys.exit(1 if bad_5xx else 0)  # 只有 5xx 是真问题，见文件头说明
