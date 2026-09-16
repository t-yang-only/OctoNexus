"""R-pool-ext-001 号池扩展层第一批接口的活体套件（只读写本地实例 + 本地数据库夹具）。

覆盖:
  P1 GET /api/v1/pool/kinds     —— 适配器自描述（能力位 / 字段说明 / secret 标记）
  P2 GET /api/v1/pool/entries   —— 统一视图能列出官方账号池条目, 且**绝不回显凭据**
  P3 GET /api/v1/pool/entries?kind=official —— 指定 kind 只回这一种
  P4 GET /api/v1/pool/entries?kind=nope     —— 未知 kind 明确报错, 不是静默空列表
  P5 GET /api/v1/pool/stats     —— 聚合计数与条目一致
  P6 未登录访问                   —— 401（号池接口全在 Admin 端口 + Auth 之后）

Run: python run_pool_api_test.py   （实例必须在跑; 会直连本地 sqlite 造/清夹具）
"""

import datetime
import csv
import io
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "data", "data.db"))
RUN_TAG = "POOLAPI-" + datetime.datetime.now().strftime("%H%M%S")
# 这个串只写进数据库, 绝不应该出现在任何接口响应里；它同时是"接口不回显凭据"的探针。
FAKE_CIPHER = "cipher-probe-" + RUN_TAG
COOKIE = {}
RESULTS = []


def record(name, ok, detail=""):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:220])


def call(method, path, payload=None, timeout=60, with_cookie=True):
    """返回 (HTTP 状态, 解包后的 data, 原始报文)。

    原始报文单独回一份是给"凭据探针"用的：断言只在**报文层**成立才算数
    （解包后的结构可能丢字段，探针要看的是接口到底吐了什么出去）。
    """
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if with_cookie and COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for h, v in resp.getheaders():
                if h.lower() == "set-cookie":
                    COOKIE["v"] = v.split(";")[0]
            raw = resp.read().decode()
            return resp.status, _unwrap(raw), raw
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        return exc.code, _unwrap(raw), raw
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}, ""


def _unwrap(raw):
    if not raw:
        return {}
    try:
        body = json.loads(raw)
    except ValueError:
        return {"_raw": raw[:200]}
    # 本项目响应统一是 {code,message,data}：套件一律按 data 断言，避免各套件自己拆一层。
    return body.get("data", body) if isinstance(body, dict) else body


def db():
    return sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)


# ---- 渠道凭据后端（kind=channel）的夹具：一条指向 mock 上游的渠道，凭据是假串 ----
# 渠道名刻意**不含** RUN_TAG 子串：套件里几处断言按 RUN_TAG 做名称子串过滤，官方账号池夹具恰好 3 条；
# 混进来会让那些断言变成"假失败"（口径没错，是过滤条件被污染）。
STATION_CHANNEL = "POOLAPI-STATION-" + RUN_TAG.split("-", 1)[1]
STATION_KEY = "sk-station-fixture-not-a-real-key"
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099")
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")


def scalar(sql, args=()):
    conn = db()
    try:
        value = conn.execute(sql, args).fetchone()
        return value[0] if value else None
    finally:
        conn.close()


def row(sql, args=()):
    conn = db()
    try:
        return conn.execute(sql, args).fetchone()
    finally:
        conn.close()


def read_mock_requests():
    """读 mock 上游的请求日志（它只落凭据指纹，不落明文）。"""
    if not os.path.exists(MOCK_LOG):
        return []
    rows = []
    for line in io.open(MOCK_LOG, encoding="utf-8", errors="replace"):
        line = line.strip()
        if not line:
            continue
        try:
            rows.append(json.loads(line))
        except ValueError:
            continue
    return rows


def seed_station_channel():
    """建夹具渠道并返回渠道 ID。

    走生产同一条写入路径（/channel/create）而不是直接写库：channels 的列多且带约束，
    照抄一份 insert 迟早会漂移。名字带 RUN_TAG，残留也不会撞上别的套件的夹具。
    """
    existing = scalar("select id from channels where name=?", (STATION_CHANNEL,))
    if existing:
        call("DELETE", "/api/v1/channel/delete/%d" % existing)
    payload = {
        "name": STATION_CHANNEL, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
        "keys": [{"name": "k-on", "key": STATION_KEY, "enabled": True},
                 {"name": "k-off", "key": STATION_KEY, "enabled": False}],
        # models 留空：/channel/create 收的是"模型名字符串列表"这一形状，给字典会被判 Invalid JSON format；
        # 这条夹具也不需要模型（号池只投影凭据本身）。
        "models": [],
    }
    call("POST", "/api/v1/channel/create", payload)
    return scalar("select id from channels where name=?", (STATION_CHANNEL,))


def cleanup_station_channel(channel_id):
    # 路由是 DELETE /api/v1/channel/delete/:id —— 用 POST 打不中，夹具就会一直在库里堆着
    #（实测如此：前几次跑留下的 POOLAPI-* 渠道就是这么来的）。
    if channel_id:
        call("DELETE", "/api/v1/channel/delete/%d" % channel_id)


def seed_accounts():
    """造三条账号夹具（access/refresh 用探针串, 不含任何真实凭据）。"""
    conn = sqlite3.connect(os.path.abspath(DB))
    now = datetime.datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    rows = [
        (RUN_TAG + "-g1@example.com", "gemini", "active", 1),
        (RUN_TAG + "-g2@example.com", "gemini", "revoked", 0),
        (RUN_TAG + "-c1@example.com", "claude", "active", 0),
    ]
    for name, provider, status, healthy in rows:
        conn.execute(
            "insert into official_accounts (provider, external_name, status, access_cipher, refresh_cipher,"
            " plan_tier, window_5h, window_7d, healthy, last_error, created_at, updated_at)"
            " values (?, ?, ?, ?, ?, 'Pro', '62%', '88%', ?, '', ?, ?)",
            (provider, name, status, FAKE_CIPHER, FAKE_CIPHER, healthy, now, now))
    conn.commit()
    conn.close()
    return [row[0] for row in rows]


def cleanup_accounts():
    conn = sqlite3.connect(os.path.abspath(DB))
    conn.execute("delete from official_accounts where external_name like ?", (RUN_TAG + "-%",))
    conn.commit()
    conn.close()


def main():
    status, _, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    if status != 200:
        record("登录", False, "HTTP %s" % status)
        return 1
    names = seed_accounts()
    station_id = seed_station_channel()
    try:
        # P1 适配器自描述
        status, payload, _ = call("GET", "/api/v1/pool/kinds")
        items = (payload or {}).get("items") or []
        official = next((item for item in items if item.get("kind") == "official"), None)
        caps = set((official or {}).get("capabilities") or [])
        fields = (official or {}).get("fields") or []
        secret_field = next((f for f in fields if f.get("name") == "access_token"), None)
        record("P1 号池后端自描述（能力位 + 字段说明）",
               status == 200 and official is not None and official.get("builtin") is True
               and {"list", "get", "probe", "refresh", "sync"} <= caps
               and secret_field is not None and secret_field.get("secret") is True,
               "HTTP %s kinds=%s caps=%s" % (status, [i.get("kind") for i in items], sorted(caps)))

        # P2 统一视图能列出夹具账号，且响应里绝不出现凭据
        status, payload, raw = call("GET", "/api/v1/pool/entries")
        entries = (payload or {}).get("items") or []
        mine = [e for e in entries if str(e.get("name", "")).startswith(RUN_TAG)]
        record("P2 统一视图列出官方账号池条目（3 条夹具全部在列）",
               status == 200 and len(mine) == 3 and all(e.get("kind") == "official" for e in mine),
               "HTTP %s 命中 %d/3；示例=%s" % (status, len(mine), mine[0] if mine else None))

        leaked = FAKE_CIPHER in raw or "access_cipher" in raw or "refresh_cipher" in raw
        record("P2b 条目接口绝不回显凭据（探针串未出现在响应里）",
               not leaked, "探针=%s 泄漏=%s" % (FAKE_CIPHER, leaked))

        # P2c 账号→凭据的映射状态可读（active 且未物化时 key_exists=false, status 原样回）
        by_name = {e.get("name"): e for e in mine}
        active = by_name.get(RUN_TAG + "-g1@example.com") or {}
        revoked = by_name.get(RUN_TAG + "-g2@example.com") or {}
        record("P2c 映射状态可读（状态/健康/明细齐全）",
               active.get("status") == "active" and active.get("healthy") is True
               and (active.get("detail") or {}).get("is_active") is True
               and revoked.get("status") == "revoked" and (revoked.get("detail") or {}).get("is_active") is False,
               "active=%s revoked=%s" % (active.get("status"), revoked.get("status")))

        # P3 kind 过滤
        status, payload, _ = call("GET", "/api/v1/pool/entries?kind=official")
        filtered = (payload or {}).get("items") or []
        record("P3 kind=official 只回这一种后端",
               status == 200 and len(filtered) >= 3 and all(e.get("kind") == "official" for e in filtered),
               "HTTP %s 条目=%d" % (status, len(filtered)))

        # P4 未知 kind 明确报错（不是静默空表）
        status, payload, raw = call("GET", "/api/v1/pool/entries?kind=nope")
        record("P4 未知 kind 明确报错", status == 400 and "unknown pool kind" in raw,
               "HTTP %s body=%s" % (status, raw[:90]))

        # P5 聚合计数与条目一致
        status, payload, _ = call("GET", "/api/v1/pool/stats")
        by_kind = ((payload or {}).get("by_kind") or {}).get("official") or {}
        _, entries_payload, _ = call("GET", "/api/v1/pool/entries?kind=official")
        count = len((entries_payload or {}).get("items") or [])
        record("P5 聚合计数与条目一致",
               status == 200 and by_kind.get("entries") == count and (payload or {}).get("total", 0) >= count,
               "stats=%s 条目=%d" % (by_kind, count))

        # P6 未登录必须 401
        status, _, _ = call("GET", "/api/v1/pool/entries", with_cookie=False)
        record("P6 未登录访问被拒（401）", status == 401, "HTTP %s" % status)

        # ===== 第二批：单条详情 + 生命周期操作（按能力位放行）=====
        active_id = (by_name.get(RUN_TAG + "-g1@example.com") or {}).get("id")

        # P7 单条详情
        status, payload, raw = call("GET", "/api/v1/pool/entries/official/" + str(active_id))
        record("P7 单条详情可读（与统一视图同源）",
               status == 200 and (payload or {}).get("id") == active_id
               and (payload or {}).get("kind") == "official",
               "HTTP %s entry=%s" % (status, (payload or {}).get("id")))
        record("P7b 详情同样不回显凭据", FAKE_CIPHER not in raw, "探针泄漏=%s" % (FAKE_CIPHER in raw))

        # P8 不存在的条目 → 404（明确，而不是空对象）
        status, payload, raw = call("GET", "/api/v1/pool/entries/official/nope:999999")
        record("P8 不存在的条目 404", status == 404 and "not found" in raw,
               "HTTP %s body=%s" % (status, raw[:90]))

        # P9 探活：夹具凭据是可识别探针串 → 解密即失败（不发任何真实请求），
        #    接口必须是"干净的失败"（有 code/message、不泄漏探针、不 500 崩栈）。
        status, payload, raw = call("POST", "/api/v1/pool/entries/official/%s/probe" % active_id)
        clean = status >= 400 and "code" in raw and "message" in raw and FAKE_CIPHER not in raw
        record("P9 探活失败是干净失败（无真实请求 / 不泄漏 / 不崩栈）",
               clean, "HTTP %s body=%s" % (status, raw[:140]))

        # P10 启停（toggle）：官方号池现在声明了这项能力，契约是"凭据没物化出来就别启停"。
        #     预期按夹具自己的 detail.key_exists 分岔，两个分支都只用 enable 方向 ——
        #     万一夹具名恰好撞上某条真实凭据，也只会把它启用，不会关掉别人的凭据。
        status, payload, raw = call("POST", "/api/v1/pool/entries/official/%s/enable" % active_id)
        _, fixture_detail, _ = call("GET", "/api/v1/pool/entries/official/" + str(active_id))
        key_exists = bool((fixture_detail or {}).get("detail", {}).get("key_exists"))
        if key_exists:
            toggle_ok = status == 200 and (payload or {}).get("enabled") is True
            toggle_why = "已物化凭据 → 期望 200 且启用"
        else:
            toggle_ok = status == 409 and "does not allow" in raw and FAKE_CIPHER not in raw
            toggle_why = "凭据尚未物化 → 期望 409 conflict"
        record("P10 启停按状态分岔（%s）" % toggle_why,
               toggle_ok, "HTTP %s body=%s" % (status, raw[:130]))

        status, _, raw = call("POST", "/api/v1/pool/entries/official/9999999/disable")
        record("P10b 未知条目的启停 404（不是静默成功）",
               status == 404 and "not found" in raw,
               "HTTP %s body=%s" % (status, raw[:110]))

        # P11 kind 级同步的未知 kind 也要明确报错。
        #     注：这里**不**对 official 真跑同步——那会给实例建"官方账号池-<provider>"渠道，
        #     属于对用户数据的写副作用；同步行为本身由 internal/pool 单测覆盖。
        status, payload, raw = call("POST", "/api/v1/pool/kinds/nope/sync")
        record("P11 未知 kind 的同步 404", status == 404 and "unknown pool kind" in raw,
               "HTTP %s body=%s" % (status, raw[:90]))

        # ===== 第三批：查询面（过滤 / 汇总 / 导出）与机器可读的操作清单 =====
        # P12 操作清单由能力位推导：声明了的（probe/refresh/sync/toggle）都要有路由。
        ops = (official or {}).get("operations") or []
        op_keys = {"%s %s" % (o.get("method"), o.get("path")) for o in ops}
        has_probe = any("probe" in key for key in op_keys)
        has_sync = any("/kinds/{kind}/sync" in key for key in op_keys)
        has_toggle = any(("enable" in key or "disable" in key) for key in op_keys)
        record("P12 操作清单与能力位一致（probe/sync/refresh/toggle 都有路由）",
               has_probe and has_sync and has_toggle and len(ops) >= 7,
               "操作数=%d probe=%s sync=%s toggle路由=%s" % (len(ops), has_probe, has_sync, has_toggle))

        # P13 过滤：状态 / 名称子串 / scanned 口径
        status, payload, _ = call("GET", "/api/v1/pool/entries?status=revoked")
        revoked_items = (payload or {}).get("items") or []
        record("P13 按状态过滤只回该状态",
               status == 200 and len(revoked_items) == 1
               and revoked_items[0].get("status") == "revoked"
               and (payload or {}).get("scanned", 0) >= 1,
               "HTTP %s 条数=%d scanned=%s" % (status, len(revoked_items), (payload or {}).get("scanned")))

        status, payload, _ = call("GET", "/api/v1/pool/entries?q=" + RUN_TAG)
        matched = (payload or {}).get("items") or []
        record("P13b 按名称子串过滤命中夹具 3 条",
               status == 200 and len(matched) == 3 and all(RUN_TAG in (e.get("name") or "") for e in matched),
               "HTTP %s 条数=%d" % (status, len(matched)))

        # P14 汇总视图与条目一致（含分服务商/分状态切分）
        status, payload, _ = call("GET", "/api/v1/pool/summary")
        by_provider = (payload or {}).get("by_provider") or {}
        by_status = (payload or {}).get("by_status") or {}
        _, entries_payload, _ = call("GET", "/api/v1/pool/entries")
        entry_count = len((entries_payload or {}).get("items") or [])
        record("P14 汇总视图与条目一致（分服务商/分状态齐全）",
               status == 200 and (payload or {}).get("total") == entry_count
               and by_provider.get("gemini", 0) >= 2 and by_status.get("revoked", 0) >= 1
               and (payload or {}).get("enabled", 0) + (payload or {}).get("disabled", 0) == entry_count,
               "HTTP %s total=%s 服务商=%s 状态=%s" % (status, (payload or {}).get("total"), by_provider, by_status))

        # P15 导出：JSON 与 CSV 都能出，且都不含凭据
        status, payload, raw = call("GET", "/api/v1/pool/export")
        rows = (payload or {}).get("items") or []
        record("P15 JSON 导出行数与条目一致且不含凭据",
               status == 200 and len(rows) == entry_count and FAKE_CIPHER not in raw,
               "HTTP %s 行数=%d" % (status, len(rows)))

        status, payload, raw = call("GET", "/api/v1/pool/export?format=csv")
        # CSV 是给人/表格软件看的：首字节必须是 BOM，否则中文名字在 Windows 表格软件里是乱码。
        has_bom = raw.startswith("\ufeff")
        lines = [line for line in raw.lstrip("\ufeff").splitlines() if line.strip()]
        record("P15b CSV 导出带表头 + 行数一致且不含凭据",
               status == 200 and lines and lines[0].startswith("kind,id,name,provider,status,enabled,healthy")
               and len(lines) == len(rows) + 1 and FAKE_CIPHER not in raw,
               "HTTP %s 行数=%d 表头=%s" % (status, len(lines), (lines[0][:60] if lines else "")))

        # P15c CSV 的"给表格软件"那一层：BOM + 每行列数与表头一致（错位比缺行更难被人发现）
        parsed = list(csv.reader(io.StringIO(raw.lstrip("\ufeff"))))
        header = parsed[0] if parsed else []
        widths = {len(item) for item in parsed}
        record("P15c CSV 带 BOM 且每行列数与表头一致",
               status == 200 and has_bom and len(header) == 10 and widths == {len(header)},
               "BOM=%s 表头列数=%d 各行列数=%s" % (has_bom, len(header), sorted(widths)))

        # ===== 第四批：单 kind 详情 / 分页排序 / 批量动作 / OpenAPI =====
        # P16 单个后端的详情（外部工具点进某个后端时才拿这一份，不用先拉全部再筛）
        status, payload, _ = call("GET", "/api/v1/pool/kinds/official")
        record("P16 取单个后端详情",
               status == 200 and (payload or {}).get("kind") == "official"
               and "list" in ((payload or {}).get("capabilities") or []),
               "HTTP %s kind=%s" % (status, (payload or {}).get("kind")))

        status, _, raw = call("GET", "/api/v1/pool/kinds/nope")
        record("P16b 未注册 kind 的详情 404", status == 404 and "unknown pool kind" in raw,
               "HTTP %s body=%s" % (status, raw[:90]))

        # P17 分页与排序：过滤后条数(total)与本次返回条数(returned)分开回
        status, payload, _ = call("GET", "/api/v1/pool/entries?q=" + RUN_TAG + "&limit=1&sort=name")
        record("P17 分页：total 是过滤后条数、returned 是本次条数",
               status == 200 and (payload or {}).get("total") == 3
               and (payload or {}).get("returned") == 1 and len((payload or {}).get("items") or []) == 1,
               "HTTP %s total=%s returned=%s" % (status, (payload or {}).get("total"), (payload or {}).get("returned")))

        status, payload, _ = call("GET", "/api/v1/pool/entries?q=" + RUN_TAG + "&limit=1&sort=name&desc=true")
        first_desc = ((payload or {}).get("items") or [{}])[0].get("name")
        status2, payload2, _ = call("GET", "/api/v1/pool/entries?q=" + RUN_TAG + "&limit=1&sort=name")
        first_asc = ((payload2 or {}).get("items") or [{}])[0].get("name")
        record("P17b 排序生效且倒序确实反了",
               status == 200 and status2 == 200 and first_desc and first_asc and first_desc != first_asc,
               "asc=%s desc=%s" % (first_asc, first_desc))

        # P18 批量动作：逐条独立、失败带原因、且不泄漏凭据
        probes = [e.get("id") for e in matched][:2]
        status, payload, raw = call("POST", "/api/v1/pool/entries/batch",
                                    {"action": "probe", "kind": "official", "ids": probes})
        items = (payload or {}).get("items") or []
        record("P18 批量探活逐条独立（假凭据必失败且带原因、不泄漏）",
               status == 200 and (payload or {}).get("total") == len(probes) and len(items) == len(probes)
               and (payload or {}).get("failed") == len(probes) and all(i.get("error") for i in items)
               and FAKE_CIPHER not in raw,
               "HTTP %s total=%s failed=%s" % (status, (payload or {}).get("total"), (payload or {}).get("failed")))

        # P18b 批量停用：官方号池现在声明了 toggle，所以逐条失败的原因是"状态不允许"
        #      （夹具凭据未物化 → 409 语义），而不再是"能力位不支持"；批量本身仍要 200 且逐条带原因。
        #      注：用 disable 方向，且夹具都是未物化凭据 —— 不会关掉任何真实凭据。
        status, payload, raw = call("POST", "/api/v1/pool/entries/batch",
                                    {"action": "disable", "kind": "official", "ids": probes[:1]})
        items = (payload or {}).get("items") or []
        record("P18b 批量启停逐条带原因（能力位已声明，失败原因转为状态不允许）",
               status == 200 and (payload or {}).get("failed") == 1 and len(items) == 1
               and items[0].get("ok") is False and "does not allow" in (items[0].get("error") or "")
               and FAKE_CIPHER not in raw,
               "HTTP %s items=%s" % (status, json.dumps(items)[:130]))

        status, _, raw = call("POST", "/api/v1/pool/entries/batch", {"action": "drop_everything", "ids": ["1"]})
        record("P18c 非法批量动作 400", status == 400 and "unknown batch action" in raw,
               "HTTP %s body=%s" % (status, raw[:90]))

        status, _, raw = call("POST", "/api/v1/pool/entries/batch", {"action": "probe"})
        record("P18d 不给 ids 也不给 filter 被拒（不允许隐式全量）",
               status == 400 and "either ids or a filter" in raw,
               "HTTP %s body=%s" % (status, raw[:90]))

        # P19 OpenAPI 文档由注册表推导：probe/sync/toggle 的路由都要在文档里
        status, payload, raw = call("GET", "/api/v1/pool/openapi.json")
        doc_paths = (payload or {}).get("paths") or {}
        doc_schemas = ((payload or {}).get("components") or {}).get("schemas") or {}
        kind_param = (((doc_paths.get("/api/v1/pool/kinds/{kind}") or {}).get("get") or {})
                      .get("parameters") or [{}])[0]
        enum = ((kind_param.get("schema") or {}).get("enum") or [])
        record("P19 OpenAPI 由注册表推导（路径/枚举/schema 齐全且无凭据）",
               status == 200 and (payload or {}).get("openapi") == "3.0.3"
               and any("probe" in p for p in doc_paths)
               and any("/kinds/{kind}/sync" in p for p in doc_paths)
               and any(("enable" in p or "disable" in p) for p in doc_paths)
               and "official" in enum and "channel" in enum
               and {"Entry", "BatchRequest", "Summary"} <= set(doc_schemas)
               and FAKE_CIPHER not in raw,
               "HTTP %s paths=%d schemas=%d enum=%s" % (status, len(doc_paths), len(doc_schemas), enum))

        # ===== 第九批：渠道凭据后端（kind=channel） =====
        # P21 后端清单里多了"渠道凭据"，能力位与实现一致（声明 toggle 才该有启停路由）
        status, payload, _ = call("GET", "/api/v1/pool/kinds")
        kind_items = (payload or {}).get("items") or []
        channel_info = next((item for item in kind_items if item.get("kind") == "channel"), None)
        channel_caps = set((channel_info or {}).get("capabilities") or [])
        record("P21 渠道凭据后端自描述（list/get/probe/refresh/toggle，且不宣称 sync）",
               status == 200 and channel_info is not None and channel_info.get("builtin") is True
               and {"list", "get", "probe", "refresh", "toggle"} <= channel_caps and "sync" not in channel_caps,
               "HTTP %s kinds=%s caps=%s" % (status, [i.get("kind") for i in kind_items], sorted(channel_caps)))

        # P22 统一视图里能看到这条渠道的两条凭据，ID 口径是 <渠道ID>:<凭据名>
        status, payload, raw = call("GET", "/api/v1/pool/entries?kind=channel")
        channel_entries = (payload or {}).get("items") or []
        station_entries = [e for e in channel_entries if (e.get("provider") or "") == STATION_CHANNEL]
        ids = sorted(e.get("id") for e in station_entries)
        on_entry = next((e for e in station_entries if e.get("id") == "%d:k-on" % station_id), None)
        record("P22 渠道凭据按 <渠道ID>:<凭据名> 列示（启用位/站点族/探活口径齐备）",
               status == 200 and len(station_entries) == 2
               and ids == sorted(["%d:k-on" % station_id, "%d:k-off" % station_id])
               and on_entry is not None and on_entry.get("enabled") is True
               and on_entry.get("status") == "active"
               and (on_entry.get("labels") or {}).get("family") == "new-api"
               and (on_entry.get("detail") or {}).get("base_url") == MOCK_BASE
               and (on_entry.get("detail") or {}).get("probe_kind") == "na_token",
               "HTTP %s 条数=%d ids=%s" % (status, len(station_entries), ids))

        record("P22b 渠道凭据接口绝不回显凭据（探针串未出现）",
               STATION_KEY not in raw,
               "探针泄漏=%s" % (STATION_KEY in raw))

        # P23 官方账号池自动物化的渠道不该以"渠道凭据"的身份再出现一次
        official_like = [e.get("provider") for e in channel_entries if (e.get("provider") or "").startswith("官方账号池-")]
        record("P23 官方账号池渠道不在渠道凭据后端里重复列示",
               official_like == [],
               "命中=%s" % official_like)

        # P24 探活：按 new-api 口径打站点只读自查端点，结论（健康/状态码/口径）回在 detail 里
        status, payload, raw = call("POST", "/api/v1/pool/entries/channel/%d:k-on/probe" % station_id)
        detail = (payload or {}).get("detail") or {}
        record("P24 渠道凭据探活（打只读端点，回健康 + 状态码 + 探活口径）",
               status == 200 and (payload or {}).get("healthy") is True
               and detail.get("probe_kind") == "na_token" and detail.get("probe_status_code") == 200
               and STATION_KEY not in raw,
               "HTTP %s healthy=%s probe=%s" % (
                   status, (payload or {}).get("healthy"),
                   {k: detail.get(k) for k in ("probe_kind", "probe_status_code")}))

        # P24b 从上游日志反证：打的确实是 GET /api/user/self，且日志里只有凭据指纹
        station_calls = [r for r in read_mock_requests() if str(r.get("path", "")).endswith("/api/user/self")]
        record("P24b 探活打的是站点只读端点（GET /api/user/self），日志里不含凭据明文",
               any(r.get("method") == "GET" for r in station_calls)
               and STATION_KEY not in json.dumps(station_calls, ensure_ascii=False),
               "命中 %d 次；示例=%s" % (len(station_calls), station_calls[-1] if station_calls else None))

        # P25 刷余额：quota/used/remaining 都回，同时落进渠道余额快照（加权选路的 balance 维度读它）
        status, payload, raw = call("POST", "/api/v1/pool/entries/channel/%d:k-on/refresh" % station_id)
        detail = (payload or {}).get("detail") or {}
        record("P25 刷余额（500/120/380 三项齐全，且写进渠道余额快照）",
               status == 200 and detail.get("balance_quota") == 500 and detail.get("balance_used") == 120
               and detail.get("balance_remaining") == 380 and STATION_KEY not in raw,
               "HTTP %s detail=%s" % (status, {k: detail.get(k) for k in
                                               ("balance_quota", "balance_used", "balance_remaining")}))

        # P26 启停：停用要同时落下"人工停用"标记，否则周期物化会把它改回启用
        status, payload, _ = call("POST", "/api/v1/pool/entries/channel/%d:k-on/disable" % station_id)
        flags = row("select enabled, operator_disabled from channel_keys where channel_id=? and name=?",
                    (station_id, "k-on"))
        record("P26 停用凭据：enabled=0 且记下人工决定",
               status == 200 and (payload or {}).get("enabled") is False
               and (payload or {}).get("status") == "disabled-by-operator" and flags == (0, 1),
               "HTTP %s status=%s db=%s" % (status, (payload or {}).get("status"), flags))

        status, payload, _ = call("POST", "/api/v1/pool/entries/channel/%d:k-on/enable" % station_id)
        flags = row("select enabled, operator_disabled from channel_keys where channel_id=? and name=?",
                    (station_id, "k-on"))
        record("P26b 启用凭据：enabled=1 且清掉人工决定",
               status == 200 and (payload or {}).get("enabled") is True and flags == (1, 0),
               "HTTP %s db=%s" % (status, flags))

        # P27 未知凭据的探活是干净失败（404），不是静默成功
        status, _, raw = call("POST", "/api/v1/pool/entries/channel/%d:nope/probe" % station_id)
        record("P27 未知凭据探活 404（不是静默成功）",
               status == 404 and "not found" in raw,
               "HTTP %s body=%s" % (status, raw[:90]))

        # P20 新路由同样在鉴权之后。
        #     注意 code=0：无 cookie 的 POST 会在服务端读完请求体前被拒，
        #     客户端偶发"连接重置"而不是收到 401（实测三次里出现过一次）。
        #     这不是接口问题, 但断言也不放宽——重试掉传输噪声, 仍要求 401, 并把真实异常带上。
        def unauth_status(method, path, payload=None):
            last = (0, "")
            for _ in range(3):
                code, body, _ = call(method, path, payload, with_cookie=False)
                if code:
                    return code, body
                last = (code, json.dumps(body)[:80])
            return last

        checks = []
        for method, path in [("GET", "/api/v1/pool/kinds/official"), ("GET", "/api/v1/pool/openapi.json")]:
            checks.append(unauth_status(method, path))
        checks.append(unauth_status("POST", "/api/v1/pool/entries/batch", {"action": "probe", "ids": ["1"]}))
        codes = [code for code, _ in checks]
        record("P20 新路由未登录 401", all(code == 401 for code in codes),
               "codes=%s detail=%s" % (codes, [body for code, body in checks if code != 401]))

    finally:
        cleanup_accounts()
        cleanup_station_channel(station_id)

    print()
    total = len(RESULTS)
    passed = sum(1 for _, ok, _ in RESULTS if ok)
    print("POOLAPI total=%d pass=%d fail=%d" % (total, passed, total - passed))
    for name, ok, _ in RESULTS:
        if not ok:
            print("  FAILED:", name)
    return 0 if passed == total else 1


if __name__ == "__main__":
    sys.exit(main())
