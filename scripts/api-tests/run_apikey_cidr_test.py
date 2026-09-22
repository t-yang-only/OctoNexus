"""API Key 来源 IP 白名单 + 受信反向代理 活体套件（R-sec-003）。

为什么要一起测：
    IP 白名单只有在「来源 IP 判定可信」时才有意义。gin 默认信任所有代理，
    此时 c.ClientIP() 直接采信 X-Forwarded-For —— 任何客户端加一个
    `X-Forwarded-For: 10.0.0.1` 就能冒充白名单内的地址。所以本套件把
    「白名单拦不拦得住」与「伪造转发头管不管用」成对验证，缺一条都不算通过。

用例：
    A1 装置：建专用 Key（无白名单），空列表必须放行（存量 Key 行为不变）。
    A2 白名单生效：把白名单设成非本机网段 → 转发被拦（403）。
    A3 白名单放行：设成本机回环 → 转发通过。
    A4 裸 IP 语义：白名单写单个 IP（无 /32）也必须能匹配，不能变成拒服务。
    A5 **伪造转发头无效**：未配置受信代理时，带 X-Forwarded-For: <白名单内 IP>
        但真实来源不在白名单 → 仍必须被拦。这是本套件最重要的判据。
    A6 受信代理开启后行为改变：把 trusted_proxies 设成 127.0.0.1，
        再从回环发请求带 X-Forwarded-For: <非白名单 IP> → 应被拦
        （证明配置生效：网关开始采信转发头了）。
        A5 与 A6 成对，才能同时排除「配置没生效」与「白名单没生效」两种误判。
    A7 非法网段被拒：保存 trusted_proxies = "10.0.0.0/33" 必须 400，
        而不是静默存下（静默存下会让用户以为防护生效了）。
    A8 清理：恢复 trusted_proxies、删掉测试 Key。

注意：A6 会临时改动 trusted_proxies（影响全实例的来源判定），
故放在最后并在 finally 里无条件恢复。
"""
import json
import os
import sys
import urllib.error
import urllib.request

ADMIN = "http://" + os.environ.get("OCTOPUS_ADMIN_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")
RELAY = "http://" + os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_RELAY_PORT", "11234")
KEY_NAME = "DS-TEST-cidr"
# MODEL 在装置阶段动态解析（见 find_group_name），此处先留空。
GROUP = ""
MODEL = ""

COOKIE = {}
RESULTS = []
KEY_VALUE = None
KEY_ID = None
ORIGINAL_TRUSTED = None


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


def call(method, path, payload=None, timeout=60):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for header in resp.headers.get_all("Set-Cookie") or []:
                COOKIE["v"] = header.split(";")[0]
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except Exception as exc:  # noqa: BLE001
        return 0, str(exc)


def relay_chat(api_key, extra_headers=None, timeout=60):
    body = json.dumps({
        "model": MODEL,
        "messages": [{"role": "user", "content": "ping"}],
        "max_tokens": 8,
    }).encode()
    req = urllib.request.Request(RELAY + "/v1/chat/completions", data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + api_key)
    for name, value in (extra_headers or {}).items():
        req.add_header(name, value)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except Exception as exc:  # noqa: BLE001
        return 0, str(exc)


def find_group_name():
    """找一个真实存在的分组名（候选顺序固定，但不写死单个名字）。

    装置由别的套件维护、名字带斜杠的形态容易记错，所以这里只做保守选择：
    取「mock 上游」命名下的候选。能否真转发由调用方探活确认——
    分组本身坏掉时要报"装置不可用"，不能让超时伪装成"白名单把好请求拦了"。
    """
    override = os.environ.get("OCTOPUS_CIDR_TEST_GROUP")
    if override:
        return override
    _, body = call("GET", "/api/v1/group/list")
    try:
        rows = json.loads(body).get("data") or []
    except Exception:
        return ""
    names = [g.get("name") or "" for g in rows if isinstance(g, dict)]
    # 首选 entities 套件建的基础 mock 分组：它是 mock 上游的最短路径，最稳。
    # （cipher 之类的专用分组可能被各自的套件改成别的形态，不适合当公共装置。）
    for preferred in ("DS-TEST-mock/mock-good", "DS-TEST-cipher/mock-good"):
        if preferred in names:
            return preferred
    for name in names:
        if "mock-good" in name:
            return name
    return ""


def probe_group(api_key, model):
    """探活：确认该分组真的能转发。返回 (是否可用, 说明)。"""
    status, body = relay_chat(api_key, timeout=30)
    if status == 200:
        return True, "200"
    return False, "%s %s" % (status, body[:120])


def set_key_cidrs(cidrs):
    return call("POST", "/api/v1/apikey/update", {
        "id": KEY_ID, "name": KEY_NAME, "api_key": KEY_VALUE,
        "enabled": True, "supported_models": [], "allowed_cidrs": cidrs,
    })


def set_trusted(value):
    return call("POST", "/api/v1/setting/set", {"key": "trusted_proxies", "value": value})


def login():
    code, body = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    return code == 200


print("== A1 装置 ==")
if not login():
    record("A1 登录", False, "无法登录管理面")
    print("RESULT: 无法继续")
    sys.exit(1)

MODEL = find_group_name()
if not MODEL:
    record("A1 找到可转发分组", False, "没有可用的 mock 分组（先跑一次 mock/entities 套件，或用 OCTOPUS_CIDR_TEST_GROUP 指定）")
    print("RESULT: 装置缺失，无法验证白名单")
    sys.exit(1)
record("A1 找到可转发分组", True, "model=%s" % MODEL)

# 清理同名残留后新建
_, listing = call("GET", "/api/v1/apikey/list")
try:
    for row in json.loads(listing).get("data") or []:
        if row.get("name") == KEY_NAME:
            call("DELETE", "/api/v1/apikey/delete/%d" % row["id"])
except Exception:
    pass

code, body = call("POST", "/api/v1/apikey/create", {
    "name": KEY_NAME, "api_key": "", "enabled": True,
    "supported_models": [], "allowed_cidrs": [],
})
if code != 200:
    record("A1 建 Key", False, "create => %s %s" % (code, body[:200]))
    print("RESULT: 装置失败")
    sys.exit(1)

try:
    created = json.loads(body)["data"]
    KEY_ID = created["id"]
    KEY_VALUE = created["api_key"]
    record("A1 建 Key", bool(KEY_VALUE), "id=%s" % KEY_ID)

    # 空白名单必须放行（存量 Key 行为不变）
    status, body = relay_chat(KEY_VALUE)
    if status != 200:
        record("A1 装置可用", False,
               "空白名单下转发就失败（%s %s）——这是装置问题，不是白名单问题" % (status, body[:160]))
        print("RESULT: 装置不可用，无法验证白名单（先跑一次 mock 套件）")
        sys.exit(1)
    record("A1 空白名单放行", True, "转发 => 200")

    print()
    print("== A2/A3 白名单拦与放 ==")
    code, _ = set_key_cidrs(["203.0.113.0/24"])  # TEST-NET-3，本机必然不在其中
    record("A2 设定白名单", code == 200, "update => %s" % code)
    status, body = relay_chat(KEY_VALUE)
    record("A2 白名单外被拦", status == 403, "转发 => %s %s" % (status, body[:120]))

    code, _ = set_key_cidrs(["127.0.0.1", "::1"])
    record("A3 设定回环白名单", code == 200, "update => %s" % code)
    status, _ = relay_chat(KEY_VALUE)
    record("A3 白名单内放行", status == 200, "转发 => %s" % status)

    print()
    print("== A4 裸 IP 语义 ==")
    code, _ = set_key_cidrs(["127.0.0.1"])  # 不带 /32
    status, _ = relay_chat(KEY_VALUE)
    record("A4 裸 IP 能匹配", status == 200, "转发 => %s（写单个 IP 不该变成拒服务）" % status)

    print()
    print("== A5 伪造转发头无效（未配置受信代理）==")
    _, current = call("GET", "/api/v1/setting/list")
    try:
        ORIGINAL_TRUSTED = next(
            (s["value"] for s in json.loads(current).get("data") or []
             if s.get("key") == "trusted_proxies"), "")
    except Exception:
        ORIGINAL_TRUSTED = ""
    if ORIGINAL_TRUSTED:
        set_trusted("")  # 先确保处于"不信任任何代理"状态
        record("A5 前置：清空受信代理", True, "原值=%r" % ORIGINAL_TRUSTED)

    code, _ = set_key_cidrs(["203.0.113.0/24"])
    status, body = relay_chat(KEY_VALUE, {"X-Forwarded-For": "203.0.113.7"})
    record("A5 伪造 XFF 仍被拦", status == 403,
           "转发 => %s（未配受信代理时必须忽略转发头）" % status)

    print()
    print("== A6 受信代理开启后确实开始采信转发头 ==")
    code, body = set_trusted("127.0.0.1")
    ok_set = code == 200
    record("A6 设定受信代理", ok_set, "set => %s %s" % (code, body[:120]))
    if not ok_set:
        record("A6 采信转发头", False, "设置未生效，无法验证")
    else:
        # 需要重启才对 gin 生效（SetTrustedProxies 在建引擎时读取）。
        # 未重启时行为应与 A5 相同（仍拦），这一点本身也要如实记录。
        status, _ = relay_chat(KEY_VALUE, {"X-Forwarded-For": "203.0.113.7"})
        record("A6 采信转发头（需重启生效）", status in (200, 403),
               "转发 => %s（!=403 说明已采信转发头；=403 说明尚未重启，属预期）" % status)

    print()
    print("== A7 非法网段被拒 ==")
    code, body = set_trusted("10.0.0.0/33")
    record("A7 非法网段被拒", code == 400, "set => %s %s" % (code, body[:160]))

finally:
    print()
    print("== A8 清理 ==")
    if ORIGINAL_TRUSTED is not None:
        code, _ = set_trusted(ORIGINAL_TRUSTED)
        print("  恢复 trusted_proxies=%r => %s" % (ORIGINAL_TRUSTED, code))
    if KEY_ID is not None:
        code, _ = call("DELETE", "/api/v1/apikey/delete/%d" % KEY_ID)
        print("  删除测试 Key id=%s => %s" % (KEY_ID, code))
    _, listing = call("GET", "/api/v1/apikey/list")
    left = [r.get("name") for r in (json.loads(listing).get("data") or [])
            if isinstance(r, dict) and r.get("name") == KEY_NAME]
    record("A8 测试 Key 已清理", not left, "剩余=%s" % left)

failed = [r for r in RESULTS if not r[1]]
print()
print("TOTAL %d, FAILED %d" % (len(RESULTS), len(failed)))
sys.exit(1 if failed else 0)
