#!/usr/bin/env python3
"""R-test-001 本地备份隔离导入验证：把用户的真实备份导进一个**彻底隔绝**的实验室实例，
验证登录 / 渠道 / 分组 / 协议转化，全程不碰生产库、不打任何真实上游。

为什么必须隔绝（这是本套件的第一设计目标，不是附加项）：
  备份里的 12 个渠道都是用户付费的真站点。两条独立的泄漏路径必须同时堵死：
    (a) 直连调用 —— 若把真 base_url 导进任意实例并发请求，就是拿用户额度打真实上游；
    (b) 后台任务 —— 产品自带"余额扫描（quota_scan_interval，默认 5 分钟一轮）"与
        "冷却成员主动探活（route_probe_enabled，注释明写『探测是真实计费请求』）"，
        它们会自己按渠道 base_url 出网，不需要我发请求。
  所以本套件的做法是：
    1. 实验室实例用自己的配置文件（`--config`）跑在另一套端口 + 另一个数据库文件上；
    2. 导入的是备份的**脱敏副本**：每个渠道的 base_url 改成本地 mock，其余逐字保留；
       于是后台任务即使跑起来，出网目标也只有本地桩；
    3. 同时把后台出网类设置关掉（余额扫描周期 0、主动探活 false、模型信息更新周期拉长、
       告警目标清空），保证实验室既不产生计费调用，也不会把告警推到用户手机上；
    4. ★ 硬安全闸门：导入**之前**先断言"实验室临时库文件确实存在"（证明实例用的是自己的库），
       不满足就立即中止、绝不导入 —— 否则备份会被写进生产库。

用法：
  python run_backup_import_test.py [--backup <备份 json>]

凭据纪律：本套件从不打印任何 key 值；脱敏副本落在 %TEMP%（不在仓库内），收尾一并删除。
"""

import argparse
import hashlib
import io
import json
import os
import shutil
import socket
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
REPO = os.path.normpath(os.path.join(HERE, "..", ".."))
EXE = os.path.join(REPO, "octopus.exe")
LAB_PORT = int(os.environ.get("OCTOPUS_LAB_ADMIN_PORT", "13304"))
LAB_RELAY_PORT = int(os.environ.get("OCTOPUS_LAB_RELAY_PORT", "11235"))
LAB_DIR = os.path.join(os.environ.get("TEMP", HERE), "octopus-dstest", "lab")
LAB_DB = os.path.join(LAB_DIR, "lab-data.db")
LAB_CONFIG = os.path.join(LAB_DIR, "lab-config.json")
LAB_BACKUP = os.path.join(LAB_DIR, "backup-scrubbed.json")
PROD_DB = os.path.join(REPO, "data", "data.db")
MOCK_BASE = os.environ.get("OCTOPUS_MOCK_BASE", "http://127.0.0.1:18099")
DEFAULT_BACKUP = os.path.normpath(
    os.path.join(REPO, "..", "octopus-本地数据", "数据备份(勿提交).json"))

# 导入脱敏副本时要强制覆盖的设置：每一条都对应一条"实验室自己出网/发通知"的路径。
SAFE_SETTINGS = {
    "proxy_url": "",                 # 别把本地桩请求绕去真代理
    "quota_scan_interval": "0",      # 0 = 停用余额扫描任务（默认 5 分钟一轮，会按 base_url 出网）
    "route_probe_enabled": "false",  # 主动探活 = 真实计费请求，必须关
    "model_info_update_interval": "9999",
    "alert_channels": "webhook",
    "alert_webhook_url": "",
    "alert_feishu_webhook": "",
    "alert_dingtalk_webhook": "",
    "alert_wecom_webhook": "",
    "alert_serverchan_sendkey": "",
    "alert_smtp_host": "",
    "alert_smtp_user": "",
    "alert_smtp_from": "",
    "alert_smtp_to": "",
}

PASSED = 0
FAILED = 0
COOKIE = {}
_lab_proc = None


def record(name, ok, detail=""):
    global PASSED, FAILED
    if ok:
        PASSED += 1
    else:
        FAILED += 1
    line = "%s %s :: %s" % ("PASS" if ok else "FAIL", name, str(detail)[:230])
    # GBK 控制台遇到不可编码字符会抛 UnicodeEncodeError 并打断整个套件，必须兜住。
    print(line.encode(sys.stdout.encoding or "utf-8", "replace")
          .decode(sys.stdout.encoding or "utf-8", "replace"), flush=True)


def api(method, path, payload=None, timeout=90, cookie_value=None):
    """返回 (HTTP 状态, 解包后的 data, 原始报文)。响应统一 {code,message,data}。"""
    url = "http://127.0.0.1:%d%s" % (LAB_PORT, path)
    if isinstance(payload, str):
        data = payload.encode("utf-8")
    elif payload is not None:
        data = json.dumps(payload).encode("utf-8")
    else:
        data = None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    holder = cookie_value if cookie_value is not None else COOKIE.get("v")
    if holder:
        req.add_header("Cookie", holder)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for key, value in resp.getheaders():
                if key.lower() == "set-cookie" and cookie_value is None:
                    COOKIE["v"] = value.split(";")[0]
            raw = resp.read().decode("utf-8", errors="replace")
            return resp.status, unwrap(raw), raw
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", errors="replace")
        return exc.code, unwrap(raw), raw
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}, ""


def unwrap(raw):
    if not raw:
        return {}
    try:
        body = json.loads(raw)
    except ValueError:
        return {"_raw": raw[:200]}
    return body.get("data", body) if isinstance(body, dict) else body


def lab():
    return sqlite3.connect("file:%s?mode=ro" % LAB_DB.replace("\\", "/"), uri=True)


def scalar(sql, args=()):
    conn = lab()
    try:
        value = conn.execute(sql, args).fetchone()
        return value[0] if value else None
    finally:
        conn.close()


def rows(sql, args=()):
    conn = lab()
    try:
        return conn.execute(sql, args).fetchall()
    finally:
        conn.close()


def port_open(port, host="127.0.0.1"):
    with socket.socket() as sock:
        sock.settimeout(0.7)
        return sock.connect_ex((host, port)) == 0


def wait_port(port, seconds=30):
    deadline = time.time() + seconds
    while time.time() < deadline:
        if port_open(port):
            return True
        time.sleep(0.4)
    return False


def run_shell(cmd):
    return subprocess.run(cmd, shell=True, capture_output=True, text=True,
                          encoding="utf-8", errors="replace")


def stop_listening(port):
    """按端口找监听进程并停掉（实验室与生产靠端口区分，绝不按进程名杀）。"""
    out = run_shell('netstat -ano | findstr LISTENING | findstr ":%d "' % port).stdout or ""
    pids = set()
    for line in out.splitlines():
        parts = line.split()
        if parts and parts[-1].isdigit():
            pids.add(parts[-1])
    for pid in pids:
        run_shell("taskkill /PID %s /T /F" % pid)
    return pids


def cleanup_lab():
    """停实验室实例并删除它的一切临时产物（含带真凭据的脱敏副本）。"""
    for port in (LAB_PORT, LAB_RELAY_PORT):
        stop_listening(port)
    time.sleep(1.2)
    shutil.rmtree(LAB_DIR, ignore_errors=True)


def prod_fingerprint():
    """生产库"用户数据"的指纹：六张用户数据表的计数 + 渠道 (id,name,base_url) 的摘要。

    刻意不用 mtime/size：生产实例自己会周期性写统计，mtime 必然变化，
    拿它当"没被改动"的判据会得出错误的结论（既可能假失败，也会掩盖真正的写入）。
    """
    if not os.path.exists(PROD_DB):
        return None
    conn = sqlite3.connect("file:%s?mode=ro" % PROD_DB.replace("\\", "/"), uri=True)
    try:
        counts = {}
        for table in ("channels", "channel_keys", "channel_models", "channel_grants",
                      "groups", "group_items"):
            counts[table] = conn.execute("select count(*) from %s" % table).fetchone()[0]
        channels = conn.execute("select id, name, base_url from channels order by id").fetchall()
        digest = hashlib.sha256(
            json.dumps(channels, ensure_ascii=False).encode("utf-8")).hexdigest()[:16]
        return counts, digest
    finally:
        conn.close()


def build_scrubbed_backup(raw):
    """把备份变成"同样形状、但所有上游都指本地桩"的副本。

    只改两处，其余逐字保留：
      · channels[].base_url -> mock 地址（堵住直连与后台任务两条出网路径）
      · settings 里强制覆盖 SAFE_SETTINGS（关掉出网类任务与告警目标）
    """
    dump = json.loads(raw)
    for channel in dump.get("channels") or []:
        if channel.get("base_url"):
            channel["base_url"] = MOCK_BASE
    settings = dump.get("settings") or []
    by_key = {}
    for index, setting in enumerate(settings):
        by_key[str(setting.get("key"))] = index
    for key, value in SAFE_SETTINGS.items():
        if key in by_key:
            settings[by_key[key]]["value"] = value
        else:
            settings.append({"key": key, "value": value})
    dump["settings"] = settings
    return dump


def start_lab():
    global _lab_proc
    os.makedirs(LAB_DIR, exist_ok=True)
    config = {
        "server": {"host": "127.0.0.1", "admin_port": LAB_PORT, "relay_port": LAB_RELAY_PORT},
        "log": {"level": "warn"},
        "database": {"type": "sqlite", "path": LAB_DB.replace("\\", "/")},
    }
    with io.open(LAB_CONFIG, "w", encoding="utf-8") as fh:
        fh.write(json.dumps(config, indent=2))
    _lab_proc = subprocess.Popen('".\\octopus.exe" start --config "%s"' % LAB_CONFIG,
                                 cwd=REPO, shell=True, creationflags=subprocess.CREATE_NEW_CONSOLE)


def main():
    parser = argparse.ArgumentParser(description="备份隔离导入验证（R-test-001）")
    parser.add_argument("--backup", default="", help="备份 json 路径")
    args = parser.parse_args()

    backup = args.backup or DEFAULT_BACKUP
    if not os.path.exists(backup):
        record("前置：能读到备份文件", False, backup)
        print("BACKUP_IMPORT_TEST total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED))
        return 2
    if not os.path.exists(EXE):
        record("前置：octopus.exe 存在", False, EXE)
        print("BACKUP_IMPORT_TEST total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED))
        return 2

    with io.open(backup, "r", encoding="utf-8", errors="replace") as fh:
        raw = fh.read()
    original = json.loads(raw)
    expect = {
        "channels": len(original.get("channels") or []),
        "channel_keys": len(original.get("channel_keys") or []),
        "channel_models": len(original.get("channel_models") or []),
        "channel_grants": len(original.get("channel_grants") or []),
        "groups": len(original.get("groups") or []),
        "api_keys": len(original.get("api_keys") or []),
    }

    if os.path.isdir(LAB_DIR):
        shutil.rmtree(LAB_DIR, ignore_errors=True)
    cleanup_lab()
    os.makedirs(LAB_DIR, exist_ok=True)
    scrubbed = build_scrubbed_backup(raw)
    # 脱敏副本落在 %TEMP%（不在仓库内），收尾必删；它含真凭据，绝不打印、绝不入库。
    with io.open(LAB_BACKUP, "w", encoding="utf-8") as fh:
        fh.write(json.dumps(scrubbed))

    record("L1 脱敏副本只改上游与安全设置（渠道数与分组数与原备份一致）",
           len(scrubbed.get("channels") or []) == expect["channels"]
           and len(scrubbed.get("groups") or []) == expect["groups"],
           "副本 channels=%d groups=%d"
           % (len(scrubbed.get("channels") or []), len(scrubbed.get("groups") or [])))
    record("L2 脱敏副本里已无任何真站点地址（全部指向本地桩）",
           all((c.get("base_url") or "") == MOCK_BASE for c in scrubbed.get("channels") or []),
           "桩地址=%s，渠道 %d 个" % (MOCK_BASE, expect["channels"]))

    prod_before = prod_fingerprint()

    start_lab()
    if not wait_port(LAB_PORT) or not wait_port(LAB_RELAY_PORT):
        record("L3 实验室实例起来（独立端口 %d/%d）" % (LAB_PORT, LAB_RELAY_PORT), False,
               "端口未监听；实验室配置=%s" % LAB_CONFIG)
        cleanup_lab()
        print("BACKUP_IMPORT_TEST total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED))
        return 2
    record("L3 实验室实例起来（独立端口 %d/%d，独立配置文件）" % (LAB_PORT, LAB_RELAY_PORT), True,
           "配置 %s" % LAB_CONFIG)

    # ★★ 硬安全闸门：先证明实验室用的是自己的库，再允许导入。
    # 若实例忽略了 --config 而回落去开生产库，这里必须失败并立即中止 —— 否则备份会被写进生产库。
    if not (os.path.exists(LAB_DB) and os.path.getsize(LAB_DB) > 0):
        record("L4 ★安全闸门：实验室用自己的临时库（不满足则绝不导入）", False,
               "临时库未生成：%s" % LAB_DB)
        cleanup_lab()
        print("BACKUP_IMPORT_TEST total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED))
        return 2
    record("L4 ★安全闸门：实验室用自己的临时库（已确认后才导入）", True,
           "临时库 %s（%d 字节）" % (os.path.basename(LAB_DB), os.path.getsize(LAB_DB)))

    status, _, _ = api("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    record("L5 实验室实例可登录（新库自动初始化 admin）", status == 200, "HTTP %s" % status)
    if status != 200:
        cleanup_lab()
        print("BACKUP_IMPORT_TEST total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED))
        return 1

    status, body, raw_resp = api("POST", "/api/v1/setting/import",
                                 json.dumps(scrubbed), timeout=180)
    detail = ""
    if isinstance(body, dict):
        detail = json.dumps(body, ensure_ascii=False)[:200]
    record("L6 备份经产品导入接口被接受（走真实导入路径）", status == 200,
           "HTTP %s %s" % (status, detail or raw_resp[:150]))
    if status != 200:
        cleanup_lab()
        print("BACKUP_IMPORT_TEST total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED))
        return 1

    for table, key in (("channels", "channels"), ("channel_keys", "channel_keys"),
                       ("channel_models", "channel_models"), ("channel_grants", "channel_grants"),
                       ("groups", "groups"), ("api_keys", "api_keys")):
        actual = scalar("select count(*) from %s" % table)
        record("L7 %s 数量与备份一致" % table, actual == expect[key],
               "库内 %s / 备份 %d" % (actual, expect[key]))

    leaked = scalar("select count(*) from channels where base_url like '%x5m5x%'"
                    " or base_url like '%longcat%' or base_url like '%okai%'"
                    " or base_url like '%cochacode%' or base_url like '%aiaaa%'"
                    " or base_url like '%thqllm%' or base_url like '%ak03%'"
                    " or base_url like '%tokenrhythm%' or base_url like '%pipixia%'"
                    " or base_url like '%apikey.fun%'")
    record("L8 实验室库里没有任何真站点地址（两条出网路径均已堵死）", leaked == 0,
           "命中真站点地址的渠道 %s 条" % leaked)
    record("L9 后台出网类任务已关（余额扫描停用 / 主动探活关闭）",
           (scalar("select value from settings where key='quota_scan_interval'") == "0")
           and (scalar("select value from settings where key='route_probe_enabled'") == "false"),
           "quota_scan_interval=%s route_probe_enabled=%s"
           % (scalar("select value from settings where key='quota_scan_interval'"),
              scalar("select value from settings where key='route_probe_enabled'")))

    # 渠道编辑路径：读详情 -> 改 base_url -> 写回 -> 再读回确认（证明导入的数据是可编辑的）
    target = rows("select id, name from channels order by id limit 1")
    if target:
        channel_id, channel_name = target[0]
        status, detail_body, _ = api("GET", "/api/v1/channel/detail/%s" % channel_id)
        payload = detail_body if isinstance(detail_body, dict) else {}
        payload["base_url"] = MOCK_BASE + "/edited"
        status_upd, _, upd_raw = api("POST", "/api/v1/channel/update", payload)
        status_re, reread, _ = api("GET", "/api/v1/channel/detail/%s" % channel_id)
        after = (reread if isinstance(reread, dict) else {}).get("base_url")
        record("L10 导入的渠道可编辑（详情 -> 改上游 -> 写回 -> 读回一致）",
               status_upd == 200 and after == MOCK_BASE + "/edited",
               "渠道=%s 写回 HTTP %s 读回 base_url=%s" % (channel_name, status_upd, after))
        # 改回原样，避免影响后面的调用测试
        payload["base_url"] = MOCK_BASE
        api("POST", "/api/v1/channel/update", payload)
    else:
        record("L10 导入的渠道可编辑", False, "库里没有渠道")

    # 调用测试：挑一个"渠道启用且至少一条凭据启用"的分组（避开 mock 对 slow/bad 的特殊行为）。
    # 注意 grants 表没有 channel_id 列：它经 channel_model_id / channel_key_id 两侧各自回指渠道，
    # 所以这里从 channel_models 取渠道、从 channel_keys 取凭据（两侧的 channel_id 必须同属一条渠道）。
    candidates = rows(
        "select g.name from groups g where g.name not like '%slow%' and g.name not like '%bad%'"
        " and exists (select 1 from group_items gi"
        "   join channel_grants gr on gr.id = gi.channel_grant_id"
        "   join channel_models cm on cm.id = gr.channel_model_id and cm.channel_id = "
        "        (select channel_id from channel_keys where id = gr.channel_key_id)"
        "   join channels c on c.id = cm.channel_id and c.enabled = 1"
        "   join channel_keys ck on ck.id = gr.channel_key_id and ck.enabled = 1"
        "  where gi.group_id = g.id) order by g.id")
    group = str(candidates[0][0]) if candidates else None
    api_key = scalar("select api_key from api_keys where enabled=1 order by id limit 1")
    record("L11 找到可用于调用测试的分组与 API Key", bool(group) and bool(api_key),
           "分组=%s" % group)

    def relay(path, payload, timeout=90):
        url = "http://127.0.0.1:%d%s" % (LAB_RELAY_PORT, path)
        req = urllib.request.Request(url, data=json.dumps(payload).encode("utf-8"), method="POST")
        req.add_header("Content-Type", "application/json")
        req.add_header("Authorization", "Bearer " + str(api_key))
        try:
            with urllib.request.urlopen(req, timeout=timeout) as resp:
                return resp.status, resp.read().decode("utf-8", errors="replace")
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read().decode("utf-8", errors="replace")
        except Exception as exc:  # noqa: BLE001
            return 0, "%s: %s" % (type(exc).__name__, exc)

    if group:
        status, text = relay("/v1/chat/completions",
                             {"model": group, "messages": [{"role": "user", "content": "hi"}],
                              "max_tokens": 16})
        record("L12 备份里的分组跑通非流式调用（走本地桩）",
               status == 200 and "choices" in text, "HTTP %s %s" % (status, text[:120]))

        status, text = relay("/v1/chat/completions",
                             {"model": group, "messages": [{"role": "user", "content": "hi"}],
                              "max_tokens": 16, "stream": True})
        record("L13 同一分组流式调用通（SSE）",
               status == 200 and "data:" in text, "HTTP %s %s" % (status, text[:120]))

        protocol_results = []
        for path, payload in (
            ("/v1/chat/completions", {"model": group, "max_tokens": 16,
                                      "messages": [{"role": "user", "content": "hi"}]}),
            ("/v1/responses", {"model": group, "input": "hi", "max_tokens": 16}),
            ("/v1/messages", {"model": group, "max_tokens": 16,
                              "messages": [{"role": "user", "content": "hi"}]}),
        ):
            code, body_text = relay(path, payload)
            protocol_results.append((path.rsplit("/", 1)[-1], code))
        record("L14 三种协议入口都被接受（chat / responses / messages）",
               all(200 <= code < 300 for _, code in protocol_results),
               "、".join("%s=%s" % item for item in protocol_results))

        log_rows = scalar("select count(*) from relay_logs") or 0
        targets = rows("select distinct target_channel from relay_logs"
                       " where target_channel is not null limit 5")
        record("L15 调用落进 relay 日志且指向备份里的渠道",
               log_rows > 0 and len(targets) >= 1,
               "日志 %s 行，命中渠道 %s" % (log_rows, [str(t[0]) for t in targets][:3]))
    else:
        record("L12-L15 调用测试", False, "没有找到可用分组，跳过")

    prod_after = prod_fingerprint()
    record("L16 生产库未被本次验证改动（六张用户数据表计数 + 渠道指纹一致）",
           prod_before == prod_after, "前=%s 后=%s" % (prod_before, prod_after))

    cleanup_lab()
    record("L17 实验室已清场（端口关闭 + 临时库/配置/含凭据的副本全部删除）",
           not port_open(LAB_PORT) and not port_open(LAB_RELAY_PORT)
           and not os.path.exists(LAB_DB) and not os.path.exists(LAB_BACKUP)
           and not os.path.isdir(LAB_DIR),
           "端口 %d/%d；临时库残留=%s；副本残留=%s；目录残留=%s"
           % (LAB_PORT, LAB_RELAY_PORT, os.path.exists(LAB_DB), os.path.exists(LAB_BACKUP),
              os.path.isdir(LAB_DIR)))

    print("BACKUP_IMPORT_TEST total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED))
    return 1 if FAILED else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except Exception as exc:  # noqa: BLE001
        try:
            cleanup_lab()
        except Exception:  # noqa: BLE001
            pass
        print("BACKUP_IMPORT_TEST 异常中止: %s: %s" % (type(exc).__name__, exc))
        raise
