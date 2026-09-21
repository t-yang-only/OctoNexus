"""入站请求体上限活体套件（T-bodylimit-001）—— **只用本地实例与本地 mock 上游**。

问题现场（用户线上原话）: Codex 的 remote compact 报
    {"error":{"message":"failed to read request body: request body too large","type":"invalid_request_error"}}
根因不是上游, 而是**我们自己的网关**: 依赖库 axonhub/llm 把读取入站正文的上限写死成 64 MiB
（包内私有常量, 外部改不了）。实测边界: 63 MiB 通过, 65 MiB 报上面那句。

口径（internal/relay/inbound_body.go 文件头）:
- 上限由设置项 relay_max_request_body_bytes 决定（默认 256 MiB, 0 = 不限制）;
- 除上限外语义与依赖库逐字对齐（字段、内容编码解码、删头行为、客户端 IP 取值顺序）;
- 超限仍是依赖库的哨兵错误, 但 handler 翻译成 **413** 并点名设置项与当前上限（可自救）。

用例（每条都带否定式对照）:
  B0 边界: 63 MiB 通过读取（走到后面的阶段）, 65 MiB 不再报 request body too large（**修复前必红**）
  B1 端到端: 65 MiB 正文经真实分组转发到本地 mock 得到 200（不是被自家网关挡掉）
  B2 设置项生效: 把上限调到 1 MiB, 2 MiB 正文被拒 **413** 且文案点名设置项
  B3 关掉限制: 上限设 0, 同一份 2 MiB 正文放行（证明是"可调", 不是把闸门焊死）
  B4 校验: 负数被拒（不是静默夹紧）
"""

import json
import os
import sqlite3
import sys
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY = os.environ.get("OCTOPUS_RELAY_URL", "http://127.0.0.1:11234")
DB = os.environ.get("OCTOPUS_DB", os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                               "..", "..", "data", "data.db"))
GROUP = "DS-TEST-bodylimit"
CHANNEL = "DS-TEST-mock"
MODEL = "mock-good"
COOKIE = {}
RESULTS = []
SETTING = "relay_max_request_body_bytes"


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:240])


def call(method, path, payload=None, timeout=300):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for name, value in resp.getheaders():
                if name.lower() == "set-cookie":
                    COOKIE["v"] = value.split(";")[0]
            raw = resp.read().decode()
            return resp.status, (json.loads(raw).get("data") if raw else None) or {}
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode()
        try:
            return exc.code, json.loads(raw)
        except ValueError:
            return exc.code, {"_raw": raw[:200]}
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}


def db():
    conn = sqlite3.connect("file:" + os.path.abspath(DB) + "?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    return conn


def relay_key():
    conn = db()
    try:
        row = conn.execute("select api_key from api_keys where enabled=1 order by id limit 1").fetchone()
        return row["api_key"] if row else ""
    finally:
        conn.close()


def set_setting(key, value):
    status, payload = call("POST", "/api/v1/setting/set", {"key": key, "value": str(value)})
    return status, payload


def reset_settings():
    set_setting(SETTING, 268435456)


def channel_id(name):
    conn = db()
    try:
        row = conn.execute("select id from channels where name = ?", (name,)).fetchone()
        return row["id"] if row else None
    finally:
        conn.close()


def grant_of(model_name, channel_name=CHANNEL):
    """取某渠道下该模型的授权行 id（与其它套件同源写法：channel_grants × channel_models × channels）。"""
    conn = db()
    try:
        row = conn.execute(
            "select g.id from channel_grants g join channel_models m on m.id = g.channel_model_id "
            "join channels ch on ch.id = m.channel_id where ch.name = ? and m.name = ?",
            (channel_name, model_name)).fetchone()
        return row["id"] if row else None
    finally:
        conn.close()


def create_group(name, grants):
    """建/重建分组（返回 HTTP 状态与响应体）。"""
    conn = db()
    try:
        row = conn.execute("select id from groups where name = ?", (name,)).fetchone()
        if row:
            call("DELETE", "/api/v1/group/delete/%d" % row["id"])
            for _ in range(30):
                conn2 = db()
                gone = conn2.execute("select id from groups where name = ?", (name,)).fetchone() is None
                conn2.close()
                if gone:
                    break
                time.sleep(0.2)
    finally:
        conn.close()
    return call("POST", "/api/v1/group/create", {
        "name": name, "mode": "failover",
        "items": [{"channel_grant_id": gid} for gid in grants],
        "relay_config": {"member_max_attempts": 1, "member_retry_interval_seconds": 1,
                         "member_non_stream_response_timeout_seconds": 120,
                         "member_stream_first_event_timeout_seconds": 30,
                         "member_cooldown_seconds": 5, "member_affinity_seconds": 0},
    })


def post_body(mb, model=None):
    """发送 mb MB 的合法 JSON 请求体, 返回 (状态, 正文前 260 字, 秒数)。"""
    filler = "A" * (mb * 1024 * 1024)
    payload = {"model": model or GROUP, "stream": False, "max_tokens": 8,
               "messages": [{"role": "user", "content": filler}]}
    body = json.dumps(payload).encode()
    req = urllib.request.Request(RELAY + "/v1/chat/completions", data=body, method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + relay_key())
    started = time.time()
    try:
        with urllib.request.urlopen(req, timeout=300) as resp:
            return resp.status, resp.read().decode()[:260], round(time.time() - started, 1)
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()[:260], round(time.time() - started, 1)
    except Exception as exc:  # noqa: BLE001
        return 0, "%s: %s" % (type(exc).__name__, exc), round(time.time() - started, 1)


def main():
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    reset_settings()

    channel = channel_id(CHANNEL)
    if not channel:
        record("fixtures", False, "共享装置 %s 缺失" % CHANNEL)
        return 1
    grant = grant_of(MODEL)
    if not grant:
        record("fixtures", False, "%s 下没有 %s 的授权" % (CHANNEL, MODEL))
        return 1
    grants = [grant]

    try:
        # B0 边界: 旧上限是 64 MiB —— 63 MiB 通过读取, 65 MiB 不再被自家网关拒。
        #    判据只看"读入阶段有没有过": 63 MiB 用不存在的模型名（读到正文后才会报 model not found）;
        #    65 MiB 用真实分组, 修复前必然报 request body too large。
        status_small, body_small, _ = post_body(63, model="DS-TEST-bodylimit-absent")
        status_big, body_big, secs_big = post_body(65)
        small_read = "request body too large" not in body_small
        big_read = "request body too large" not in body_big
        record("B0 64 MiB 旧上限被抬高（63 MiB 与 65 MiB 都不再被读取阶段拒）",
               small_read and big_read,
               "63MiB→HTTP %s(%s) | 65MiB→HTTP %s %ss(%s)" % (
                   status_small, body_small[:60].replace("\n", " "), status_big, secs_big,
                   body_big[:60].replace("\n", " ")))

        # B1 端到端: 65 MiB 正文经真实分组转发到本地 mock 必须 200（不是被自家网关挡掉, 也不是走到上游才失败）。
        status, _ = create_group(GROUP, grants)
        if status != 200:
            record("B1 65 MiB 正文端到端转发", False, "分组创建失败 HTTP %s" % status)
        else:
            status_big, body_big, secs_big = post_body(65)
            record("B1 65 MiB 正文端到端转发（修复前在这里就被 400 挡掉）",
                   status_big == 200 and "chatcmpl" in body_big,
                   "HTTP %s %ss %s" % (status_big, secs_big, body_big[:80].replace("\n", " ")))

        # B2 上限可调: 调到 1 MiB → 2 MiB 正文必须被拒, 且是 413 + 点名设置项（可自救）。
        set_setting(SETTING, 1048576)
        status_small2, body_small2, _ = post_body(2)
        mentions = SETTING in body_small2
        record("B2 上限调小后按新上限拒绝（413 + 点名设置项）",
               status_small2 == 413 and "request body too large" in body_small2 and mentions,
               "HTTP %s %s" % (status_small2, body_small2[:150].replace("\n", " ")))

        # B3 0 = 不限制: 同一份 2 MiB 正文放行（证明闸门可调, 不是焊死）。
        set_setting(SETTING, 0)
        status_off, body_off, _ = post_body(2)
        record("B3 上限设 0 后同一份正文放行（不是把限制焊死）",
               status_off == 200 and "chatcmpl" in body_off,
               "HTTP %s %s" % (status_off, body_off[:80].replace("\n", " ")))

        # B4 校验: 负数被拒（不是静默夹紧）。
        status_bad, payload_bad = set_setting(SETTING, -1)
        record("B4 负数上限被拒（越界不静默夹紧）",
               status_bad != 200,
               "HTTP %s %s" % (status_bad, str(payload_bad.get("message"))[:80]))
    finally:
        reset_settings()
        gid = None
        conn = db()
        try:
            row = conn.execute("select id from groups where name = ?", (GROUP,)).fetchone()
            gid = row["id"] if row else None
        finally:
            conn.close()
        if gid:
            call("DELETE", "/api/v1/group/delete/%d" % gid)

    failed = [name for name, ok, _ in RESULTS if not ok]
    print("total=%d pass=%d fail=%d" % (len(RESULTS), len(RESULTS) - len(failed), len(failed)))
    if failed:
        print("失败: " + ", ".join(failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
