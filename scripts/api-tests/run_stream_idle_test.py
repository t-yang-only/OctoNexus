"""流式「无进展上限」(T-timeout-001) 活体套件 —— **只用本地 mock 上游, 不打真实上游、不产生费用**。

被验证的缺陷（来自用户线上 Caddy 日志：断连在 300–760 秒、中位 343 秒）：
  上游吐出第一个 SSE 帧之后就再也不出字, relay 在首帧提交后只等「客户端自己放弃」——
  因为后端只有「首事件超时」(member_stream_first_event_timeout_seconds),
  首帧之后转发循环是无 deadline 的阻塞读。

装置: 新渠道 DS-TEST-idle 指向本地 mock, 模型名 mock-stall —— mock 只在流式下发出
第一个事件然后保持连接静默（不写 [DONE]、也不关连接）。分组把
member_stream_idle_timeout_seconds 设为 2 秒。

用例:
  I1 装置就位: 渠道/授权/分组存在, 分组配置读回 idle=2。
  I2 看门狗生效: 流式调用首块已到（首字节已提交）, 但响应在 ~2–6 秒内被结束, 而不是挂着。
  I3 记账是真实失败: relay_logs 该请求 status=failed 且 first_byte_ms 已设(首字节确实提交过)。
  I4 成员统计: 该成员 request_failed +1（走 markFailed 而不是 markCanceled）。
  I5 不换目标重试: member_max_attempts=2, 但上游只收到 1 次尝试（首字节已提交 → 不可重试）。
  I6 判据 0=关闭: idle=0 的分组同样静默, 3 秒时请求仍在途中（看门狗没有擅自改变旧行为）。

Run: python run_stream_idle_test.py    （实例 + mock 必须在跑）
"""

import json
import os
import sqlite3
import sys
import threading
import time
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY = "http://%s:%s" % (os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1"),
                          os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
DB = os.environ.get("OCTOPUS_DB", r"D:\奇怪的软件\octopus\data\data.db")
MOCK_LOG = os.path.join(os.path.dirname(os.path.abspath(__file__)), "requests.jsonl")

CHANNEL = "DS-TEST-idle"
GROUP = "DS-TEST-idle"
GROUP_OFF = "DS-TEST-idle-off"
STALL = "mock-stall"
IDLE_SECONDS = 2
COOKIE = {}
RESULTS = []


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    text = ("PASS  " if ok else "FAIL  ") + name + " :: " + str(detail)[:240]
    try:
        print(text)
    except UnicodeEncodeError:
        sys.stdout.buffer.write(text.encode("utf-8", "replace") + b"\n")


def call(method, path, payload=None, timeout=60):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
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


def scalar(sql, args=()):
    c = db()
    row = c.execute(sql, args).fetchone()
    c.close()
    return row[0] if row else None


def api_key():
    return scalar("select api_key from api_keys where enabled=1 order by id limit 1")


def mark():
    return os.path.getsize(MOCK_LOG) if os.path.exists(MOCK_LOG) else 0


def attempts_since(offset):
    """某时刻之后 mock 收到的 (模型名) 列表 —— 用来数「真的有几次尝试」。"""
    if not os.path.exists(MOCK_LOG):
        return []
    out = []
    with open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        for line in fh.read().splitlines():
            try:
                out.append(json.loads(line).get("model"))
            except ValueError:
                continue
    return out


def relay_stream(model, timeout=60, sink=None):
    """流式调用: 返回 (状态码, 首块秒数, 总秒数, 收到的字节数)。

    读到底（或被服务端结束）才返回, 因此总秒数就是「服务端什么时候结束这次响应」。
    """
    key = api_key()
    payload = {"model": model, "max_tokens": 8, "stream": True,
               "messages": [{"role": "user", "content": "ping"}]}
    req = urllib.request.Request(RELAY + "/v1/chat/completions", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Content-Type", "application/json")
    req.add_header("Authorization", "Bearer " + key)
    started = time.time()
    first = None
    total_bytes = 0
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            # read1: 拿到第一个可用分片就返回 —— urllib 的 read(n) 会把分块读到凑够 n 字节,
            # 那样量不到「首块到达时刻」（本项目踩过：首块与总耗时都变成 2.04s）。
            pull = getattr(resp, "read1", None) or resp.read
            while True:
                chunk = pull(4096)
                if not chunk:
                    break
                total_bytes += len(chunk)
                if first is None and b"data:" in chunk:
                    first = round(time.time() - started, 2)
                if sink is not None:
                    sink.append(chunk)
            return resp.status, first, round(time.time() - started, 2), total_bytes
    except urllib.error.HTTPError as e:
        return e.code, first, round(time.time() - started, 2), total_bytes
    except Exception:  # noqa: BLE001
        return 0, first, round(time.time() - started, 2), total_bytes


def wait_relay_log(group_model, timeout=10):
    """等 relay_logs 出现该分组模型的终结行（看门狗结束后异步落库）。"""
    deadline = time.time() + timeout
    while time.time() < deadline:
        c = db()
        row = c.execute("select status, target_channel, first_byte_ms from relay_logs "
                        "where target_model=? order by id desc limit 1", (STALL,)).fetchone()
        c.close()
        if row and row[0] in ("failed", "canceled", "success"):
            return row
        time.sleep(0.4)
    return None


def ensure_channel():
    existing = scalar("select id from channels where name=?", (CHANNEL,))
    if existing:
        call("DELETE", "/api/v1/channel/delete/%d" % existing)
    payload = {"name": CHANNEL, "dialect": "generic", "enabled": True, "base_url": MOCK_BASE,
               "keys": [{"name": "mockkey", "key": "mock-secret-not-a-real-key", "enabled": True}],
               "models": [STALL]}
    call("POST", "/api/v1/channel/create", payload)
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if not cid:
        return None
    call("POST", "/api/v1/channel/update", dict(payload, id=cid,
                                                grants=[{"model_name": STALL, "key_name": "mockkey", "protocols": 14}]))
    return cid


def grant_of():
    return scalar("select g.id from channel_grants g join channel_models m on m.id=g.channel_model_id "
                  "join channels ch on ch.id=m.channel_id where ch.name=? and m.name=?", (CHANNEL, STALL))


def ensure_group(name, grant, idle, attempts=2):
    existing = scalar("select id from groups where name=?", (name,))
    if existing:
        call("DELETE", "/api/v1/group/delete/%d" % existing)
    return call("POST", "/api/v1/group/create", {
        "name": name, "mode": "failover",
        "items": [{"channel_grant_id": grant}],
        "relay_config": {"member_max_attempts": attempts, "member_retry_interval_seconds": 1,
                         "member_non_stream_response_timeout_seconds": 20,
                         "member_stream_first_event_timeout_seconds": 10,
                         "member_stream_idle_timeout_seconds": idle,
                         "member_cooldown_seconds": 1, "member_affinity_seconds": 0},
    })


def group_config(name):
    c = db()
    row = c.execute("select relay_config from groups where name=?", (name,)).fetchone()
    c.close()
    if not row:
        return {}
    try:
        return json.loads(row[0] or "{}")
    except ValueError:
        return {}


def channel_model_failed(model=STALL):
    """读渠道模型的内存统计（/channel/stats 返回的就是运行期缓存, 落库是异步的）。"""
    status, resp = call("GET", "/api/v1/channel/stats")
    if status != 200:
        return None
    for channel in (resp.get("data") or []):
        if channel.get("channel_name") != CHANNEL:
            continue
        for entry in (channel.get("models") or []):
            if entry.get("model_name") == model:
                return entry.get("request_failed")
    return None


def overview_has(model, seconds=2.0):
    """从 /api/v1/log/overview/stream 的初始快照里找仍在途的该请求。"""
    key = api_key()
    req = urllib.request.Request(ADMIN + "/api/v1/log/overview/stream")
    req.add_header("Cookie", COOKIE.get("v", ""))
    req.add_header("Authorization", "Bearer " + key)
    deadline = time.time() + seconds
    try:
        with urllib.request.urlopen(req, timeout=seconds + 2) as resp:
            while time.time() < deadline:
                chunk = resp.read(512)
                if not chunk:
                    break
                text = chunk.decode("utf-8", "replace")
                if model in text:
                    return True
    except Exception:  # noqa: BLE001
        return False
    return False


def remove_fixtures():
    for name in (GROUP, GROUP_OFF):
        gid = scalar("select id from groups where name=?", (name,))
        if gid:
            call("DELETE", "/api/v1/group/delete/%d" % gid)
    cid = scalar("select id from channels where name=?", (CHANNEL,))
    if cid:
        call("DELETE", "/api/v1/channel/delete/%d" % cid)


def main():
    global MOCK_BASE
    MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
    call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})

    cid = ensure_channel()
    grant = grant_of()
    if not cid or not grant:
        record("I1 装置就位", False, "渠道/授权创建失败 channel=%s grant=%s" % (cid, grant))
        return 1
    ensure_group(GROUP, grant, IDLE_SECONDS, attempts=2)
    ensure_group(GROUP_OFF, grant, 0, attempts=1)
    config = group_config(GROUP)
    record("I1 装置就位: 新字段可写入且读回",
           config.get("member_stream_idle_timeout_seconds") == IDLE_SECONDS,
           "channel=%s grant=%s idle=%s" % (cid, grant, config.get("member_stream_idle_timeout_seconds")))

    # I2 看门狗生效: 首块先到（首字节已提交）, 随后看门狗把这次响应结束掉（不是挂着等客户端）。
    before_stats = channel_model_failed()
    offset = mark()
    sink = []
    status, first, total, size = relay_stream(GROUP, timeout=60, sink=sink)
    body = b"".join(sink).decode("utf-8", "replace")
    record("I2 看门狗生效: 首块先到、响应在 %ds 后被结束" % IDLE_SECONDS,
           status == 200 and first is not None and first < 1.5 and 1.5 <= total <= 8 and size > 0,
           "HTTP %s 首块 %ss 总 %ss 收到 %dB 首块含 data=%s" % (status, first, total, size, "data:" in body))

    # I3 记账是真实失败（而不是 canceled）且首字节确实提交过。
    row = wait_relay_log(GROUP, timeout=10)
    record("I3 记账: relay_logs 记 failed 且已有首字节",
           bool(row) and row[0] == "failed" and row[2] is not None,
           "relay_logs=%s" % (row,))

    # I4 成员统计走失败口径（markFailed）而不是取消口径。
    #    注意: 成员统计是内存缓存累加 + 周期性落库, 直接读 channel_models 表看不到（踩过）,
    #    必须读 /api/v1/channel/stats（它就是缓存本身）。
    after_stats = channel_model_failed()
    record("I4 成员统计: request_failed +1",
           before_stats is not None and after_stats is not None and after_stats > before_stats,
           "channel stats before=%s after=%s（内存缓存口径）" % (before_stats, after_stats))

    # I5 首字节已提交 → 不换目标重试（虽然 member_max_attempts=2）。
    tries = [m for m in attempts_since(offset) if m == STALL]
    record("I5 不换目标重试: 上游只收到 1 次尝试",
           len(tries) == 1,
           "member_max_attempts=2 但上游收到 %d 次: %s" % (len(tries), tries))

    # I6 判据 0=关闭: 同样静默的上游, 3 秒时请求仍在途中（旧行为没有被悄悄改变）。
    # I7 同时验证: 客户端在首字节之后放弃**不计**成员失败 —— 线上假死探针的 123 次超时取消
    #    就是这样把 11 个健康成员误判成假死并降级的（见日志包 README）。
    inflight = {}
    failed_before_abort = channel_model_failed()

    def fire():
        inflight["result"] = relay_stream(GROUP_OFF, timeout=8)

    worker = threading.Thread(target=fire, daemon=True)
    worker.start()
    time.sleep(3.0)
    still = overview_has(GROUP_OFF, seconds=2.0)
    config_off = group_config(GROUP_OFF).get("member_stream_idle_timeout_seconds")
    record("I6 判据 0=关闭: 3 秒时请求仍在途中",
           config_off == 0 and still,
           "idle=%s 在途=%s（客户端 8 秒超时，请求自行收尾）" % (config_off, still))
    worker.join(timeout=30)
    failed_after_abort = channel_model_failed()
    record("I7 客户端首字节后放弃不计成员失败",
           failed_before_abort is not None and failed_after_abort == failed_before_abort,
           "成员失败计数 %s→%s（客户端主动断开不是成员故障）" % (failed_before_abort, failed_after_abort))

    return 0


if __name__ == "__main__":
    MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099/v1")
    try:
        main()
    finally:
        try:
            remove_fixtures()
        except Exception as e:  # noqa: BLE001
            print("清理夹具失败: %s" % e)
    failed = [name for name, ok, _ in RESULTS if not ok]
    print("STREAM_IDLE_TEST total=%d pass=%d fail=%d" % (len(RESULTS), len(RESULTS) - len(failed), len(failed)))
    for name in failed:
        print("  FAILED: %s" % name)
    sys.exit(1 if failed else 0)
