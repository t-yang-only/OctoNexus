"""R-pool-ext-001 第三批的活体套件：运行时声明式适配器 + 只读 APIKey 通道。

用户对「四个边界」的裁决是「我全都要」，本套件逐个把它变成可执行的判据：

  D1 未配白名单 → 注册被拒（403）           —— 边界①默认就是"谁都接不进来"（fail closed）
  D2 配了白名单 → 注册成功（200）           —— 同一条 spec 只差一个白名单，差分对照
  D3 自描述里只有只读能力位且不回显凭据      —— 边界①的能力位约束
  D4 声明写能力位（sync）→ 400              —— 要写就得写内置适配器，一份 JSON 不行
  D5 目标域名不在白名单 → 403               —— 白名单是硬边界，不是提示
  D6 统一视图真的读到了外部清单             —— 声明式适配器不是"注册成功但取不到数"
  D7 移除后未知 kind 明确报错，且可以再注册  —— 生命周期可逆
  D8 只读 APIKey 通道：能读、未带 Key 401、写动作不存在（404） —— 边界②
  D9 全程任何响应都不出现凭据原文            —— 报文层断言（解包会丢字段，探针要看原文）

Run: python run_pool_declarative_test.py   （实例 + mock 上游必须在跑）
"""

import datetime
import io
import json
import os
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY = os.environ.get("OCTOPUS_RELAY_URL", "http://127.0.0.1:11234")
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099")
RUN_TAG = "DECL-" + datetime.datetime.now().strftime("%H%M%S")
KIND = "custom-decl-" + RUN_TAG.lower()
# 这个串只写进加密存储，绝不应该出现在任何接口响应里；它同时是"接口不回显凭据"的探针。
SECRET_TOKEN = "decl-secret-" + RUN_TAG
COOKIE = {}
RESULTS = []


def record(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:240])


def _unwrap(raw):
    if not raw:
        return {}
    try:
        body = json.loads(raw)
    except ValueError:
        return {"_raw": raw[:200]}
    return body.get("data", body) if isinstance(body, dict) else body


def call(method, path, payload=None, timeout=60, base=None, headers=None):
    """返回 (HTTP 状态, 解包后的 data, 原始报文) —— 原始报文给凭据探针用。"""
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request((base or ADMIN) + path, data=data, method=method)
    request.add_header("Content-Type", "application/json")
    for name, value in (headers or {}).items():
        request.add_header(name, value)
    if base is None and COOKIE.get("v"):
        request.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            for name, value in response.getheaders():
                if name.lower() == "set-cookie":
                    COOKIE["v"] = value.split(";")[0]
            raw = response.read().decode()
            return response.status, _unwrap(raw), raw
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        return exc.code, _unwrap(raw), raw
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}, ""


def set_setting(key, value):
    status, payload, _ = call("POST", "/api/v1/setting/set", {"key": key, "value": value})
    return status, payload


def read_api_key():
    """从库里取一把启用的 APIKey（转发口鉴权用）。"""
    import sqlite3

    db_path = os.environ.get(
        "OCTOPUS_DB",
        os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"),
    )
    conn = sqlite3.connect("file:" + os.path.abspath(db_path) + "?mode=ro", uri=True)
    try:
        row = conn.execute("select api_key from api_keys where enabled=1 order by id limit 1").fetchone()
        return row[0] if row else ""
    finally:
        conn.close()


def spec(host_base=MOCK_BASE, capabilities=("list", "get"), kind=KIND):
    return {
        "kind": kind,
        "title": "声明式测试工具包 " + RUN_TAG,
        "base_url": host_base,
        "capabilities": list(capabilities),
        "auth": {"type": "bearer"},
        "secret": {"token": SECRET_TOKEN},
        "list": {
            "path": "/v1/models",
            "items_path": "data",
            "fields": {"id": "id", "name": "id", "status": "object"},
        },
    }


def main():
    status, _, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    if status != 200:
        record("登录", False, "HTTP %s" % status)
        return 1

    original_hosts = None
    _, payload, _ = call("GET", "/api/v1/pool/adapters")
    original_hosts = (payload or {}).get("hosts", "")
    registered = False
    try:
        # 清场并确保白名单是空的（D1 的前提就是"没配"）。
        set_setting("pool_declarative_hosts", "")

        # D1 未配白名单 → 一律拒绝
        status, payload, _ = call("POST", "/api/v1/pool/adapters", spec())
        record("D1 未配白名单时注册被拒（fail closed）",
               status == 403 and "白名单" in json.dumps(payload, ensure_ascii=False),
               "HTTP %s %s" % (status, payload))

        # D2 配了白名单 → 同一条 spec 注册成功
        status, _ = set_setting("pool_declarative_hosts", "127.0.0.1")
        if status != 200:
            record("D2 白名单设置", False, "HTTP %s" % status)
        status, payload, raw_register = call("POST", "/api/v1/pool/adapters", spec())
        registered = status == 200
        record("D2 配了白名单后注册成功（差分对照）",
               registered and (payload or {}).get("item", {}).get("kind") == KIND,
               "HTTP %s kind=%s" % (status, (payload or {}).get("item", {}).get("kind")))

        # D3 自描述：只有只读能力位、字段清单来自 spec、且不含凭据
        status, payload, _ = call("GET", "/api/v1/pool/kinds")
        mine = next((item for item in ((payload or {}).get("items") or []) if item.get("kind") == KIND), None)
        caps = set((mine or {}).get("capabilities") or [])
        record("D3 自描述只声明只读能力位",
               status == 200 and mine is not None and caps == {"list", "get"} and mine.get("builtin") is False,
               "HTTP %s caps=%s" % (status, sorted(caps)))

        # D4 写能力位一律拒绝
        status, payload, _ = call("POST", "/api/v1/pool/adapters", spec(capabilities=("list", "sync")))
        record("D4 声明式适配器不允许写能力位（sync→400）",
               status == 400 and "只读能力" in json.dumps(payload, ensure_ascii=False),
               "HTTP %s %s" % (status, payload))

        # D5 白名单外的域名
        status, payload, _ = call("POST", "/api/v1/pool/adapters", spec(host_base="http://example.com"))
        record("D5 白名单外的主机被拒（403）",
               status == 403, "HTTP %s %s" % (status, payload))

        # D6 统一视图读到了 mock 的模型清单（说明声明式适配器真的在工作）
        status, payload, _ = call("GET", "/api/v1/pool/entries?kind=" + KIND)
        items = (payload or {}).get("items") or []
        names = [item.get("name") for item in items]
        record("D6 统一视图读到了外部清单（读的是 mock 的 /v1/models）",
               status == 200 and len(items) >= 3 and any(name == "mock-good" for name in names),
               "HTTP %s 条目=%d 例=%s" % (status, len(items), names[:3]))

        # D8 只读 APIKey 通道（边界②）
        # 与 run_balance_test.py 同源：Key 直接从库里取（接口列表里的值不保证是明文）。
        api_key = read_api_key()
        relay_status, relay_payload, _ = call("GET", "/v1/pool/entries?kind=" + KIND, base=RELAY,
                                               headers={"Authorization": "Bearer " + api_key})
        anonymous_status, _, _ = call("GET", "/v1/pool/entries", base=RELAY)
        write_status, write_payload, _ = call("POST", "/v1/pool/adapters", spec(), base=RELAY,
                                              headers={"Authorization": "Bearer " + api_key})
        record("D8 只读 APIKey 通道：能读 / 未带 Key 401 / 写动作不存在",
               relay_status == 200 and (relay_payload or {}).get("read_only") is True
               and anonymous_status == 401 and write_status in (404, 405),
               "读=%s 匿名=%s 写=%s(%s)" % (relay_status, anonymous_status, write_status,
                                            (write_payload or {}).get("_raw", "")))

        # D7 移除 → 未知 kind 明确报错 → 可再注册
        status, _, _ = call("DELETE", "/api/v1/pool/adapters/" + KIND)
        removed = status == 200
        after_status, _, after_raw = call("GET", "/api/v1/pool/entries?kind=" + KIND)
        unknown_reported = after_status == 400 and "unknown pool kind" in after_raw
        again_status, _, _ = call("POST", "/api/v1/pool/adapters", spec())
        record("D7 移除后可再注册，且移除期间未知 kind 明确报错",
               removed and unknown_reported and again_status == 200,
               "删除=%s 未知kind=%s(%s) 再注册=%s" % (removed, unknown_reported, after_status, again_status))

        # D9 凭据不回显（报文层断言：注册响应 / 列表响应 / 自描述 三处都不能出现原文）
        _, _, list_raw = call("GET", "/api/v1/pool/adapters")
        _, _, kinds_raw = call("GET", "/api/v1/pool/kinds")
        leaked = [where for where, raw in (("注册响应", raw_register), ("适配器列表", list_raw), ("自描述", kinds_raw))
                  if SECRET_TOKEN in raw]
        record("D9 凭据原文不出现在任何响应里",
               not leaked, "泄漏位置=%s" % (leaked or "无"))
    finally:
        # 清场：移除夹具适配器，白名单恢复原值（不留下"允许 127.0.0.1"这种状态）。
        if registered:
            call("DELETE", "/api/v1/pool/adapters/" + KIND)
        set_setting("pool_declarative_hosts", original_hosts or "")

    failed = [name for name, ok, _ in RESULTS if not ok]
    print("POOLDECL total=%d pass=%d fail=%d" % (len(RESULTS), len(RESULTS) - len(failed), len(failed)))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
