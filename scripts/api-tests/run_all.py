#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""octopus 本地 API 测试总运行器（NM-DS-009）。

一条命令跑完本地 API 测试矩阵：多格式调用 / 跨协议无损 / 真实上游 / 超时切换与故障转移 /
后台统计审计 / Key 级限流与鉴权 / 路由策略 / 日志卡片解析。

前置：
  · 本机 octopus 实例在跑（默认 admin 127.0.0.1:13303、relay 127.0.0.1:11234）；
    默认端口 3303 落在 Windows 排除端口段，故本地起服务用
    OCTOPUS_SERVER_ADMIN_PORT=13303 OCTOPUS_SERVER_RELAY_PORT=11234 ./octopus.exe start
  · node/npx 可用（编译 display.ts 做纯函数自检）；web/node_modules 已安装（pnpm install）
  · 数据在本地 data/data.db（真实上游套件需要库里存在可用的真实分组）

用法：
  python scripts/api-tests/run_all.py                 # 跑全部
  python scripts/api-tests/run_all.py --only format,failover
  python scripts/api-tests/run_all.py --list
  python scripts/api-tests/run_all.py --keep-mock     # mock 上游跑完不杀（便于手工复验）

环境变量：OCTOPUS_ADMIN_URL / OCTOPUS_RELAY_HOST / OCTOPUS_RELAY_PORT / OCTOPUS_MOCK_BASE /
OCTOPUS_MOCK_PORT / MOCK_SLOW_SECONDS / OCTOPUS_DB
"""

import argparse
import json
import os
import re
import socket
import sqlite3
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(os.path.dirname(HERE))  # 仓库根
ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
RELAY_HOST = os.environ.get("OCTOPUS_RELAY_HOST", "127.0.0.1")
RELAY_PORT = int(os.environ.get("OCTOPUS_RELAY_PORT", "11234"))
MOCK_PORT = int(os.environ.get("OCTOPUS_MOCK_PORT", "18099"))
DB = os.environ.get("OCTOPUS_DB", os.path.join(ROOT, "data", "data.db"))

# (键, 脚本, 说明, 需要的依赖)
SUITES = [
    ("entities", "setup_test_entities.py", "测试实体与分组（幂等）", "instance,mock"),
    ("format", "run_format_tests.py", "多格式调用矩阵（三协议 × 流式/非流式 + 跨协议）", "instance,mock"),
    ("lossless", "run_lossless_tests.py", "跨协议无损转化（3 客户端协议 × 3 上游协议）", "instance,mock"),
    ("real", "run_real_tests.py", "真实上游调用（成本与价表逐项对照）", "instance,db"),
    ("failover", "run_failover_tests.py", "超时切换与故障转移（重试/冷却/切成员/整响应与首事件超时）", "instance,mock"),
    ("stats", "check_stats.py", "后台统计审计（日志/缓存/daily/usage 与 relay_logs 对照）", "instance"),
    ("pool", "run_pool_audit.py", "号池统一视图（谷歌/GPT/Claude 合并视图 + 同步契约 + 鉴权）", "instance,db"),
    ("apikey", "run_apikey_audit.py", "Key 级审计（限流/过期/禁用/额度/越权/登录/流/中止）", "instance,mock"),
    ("costmode", "run_costmode_test.py", "最低成本选路", "instance"),
    ("quality", "run_quality_test.py", "按质量自动切换", "instance,mock"),
    ("latency", "run_latency_test.py", "最低延迟选路", "instance,mock"),
    ("busy", "run_busy_test.py", "最空闲选路（并发摊分）", "instance,mock"),
    ("hotapply", "hot_apply_check.py", "设置热生效（改完无需重启）", "instance"),
    ("display", "check_display.js", "日志卡片字段容错解析（纯函数自检）", "node,esbuild"),
    ("reallog", "check_real_logs.js", "日志卡片真实数据等价性", "node,db"),
]


def log(message):
    print("[run_all] " + message, flush=True)


def port_open(host, port, timeout=1.0):
    with socket.socket() as sock:
        sock.settimeout(timeout)
        return sock.connect_ex((host, port)) == 0


def wait_port(host, port, seconds=15):
    deadline = time.time() + seconds
    while time.time() < deadline:
        if port_open(host, port):
            return True
        time.sleep(0.4)
    return False


def ensure_instance():
    host = ADMIN.split("//")[-1].split(":")[0]
    admin_port = int(ADMIN.rsplit(":", 1)[-1].rstrip("/"))
    if not port_open(host, admin_port):
        log("实例未在 %s 监听——请先按 README 启动本地实例（可用 OCTOPUS_SERVER_ADMIN_PORT/RELAY_PORT 覆盖端口）" % ADMIN)
        return False
    if not port_open(RELAY_HOST, RELAY_PORT):
        log("relay 端口 %s:%d 未监听" % (RELAY_HOST, RELAY_PORT))
        return False
    log("实例在线：admin %s / relay %s:%d" % (ADMIN, RELAY_HOST, RELAY_PORT))
    return True


_mock_proc = None


def ensure_mock():
    global _mock_proc
    if port_open("127.0.0.1", MOCK_PORT):
        log("mock 上游已在线：127.0.0.1:%d" % MOCK_PORT)
        return True
    env = dict(os.environ, MOCK_PORT=str(MOCK_PORT))
    out = open(os.path.join(HERE, "mock.log"), "wb")
    _mock_proc = subprocess.Popen([sys.executable, os.path.join(HERE, "mock_upstream.py")],
                                  stdout=out, stderr=subprocess.STDOUT, env=env, cwd=HERE)
    if wait_port("127.0.0.1", MOCK_PORT):
        log("mock 上游已启动：127.0.0.1:%d（pid %d）" % (MOCK_PORT, _mock_proc.pid))
        return True
    log("mock 上游启动失败，见 scripts/api-tests/mock.log")
    return False


def mock_log_check():
    """确认在跑的 mock 把请求日志写在本目录。

    端口被**别的** mock 进程占用时（例如早先手工在 %TEMP% 起的那一个），依赖请求日志的套件
    （lossless / failover / latency / busy / apikey）会静默失败——这里提前快速失败并给出处置办法。
    """
    import urllib.request
    log_path = os.path.join(HERE, "requests.jsonl")
    before = os.path.getsize(log_path) if os.path.exists(log_path) else 0
    try:
        urllib.request.urlopen("http://127.0.0.1:%d/v1/models" % MOCK_PORT, timeout=5).read()
    except Exception as exc:  # noqa: BLE001
        return False, "mock 探针请求失败: %s" % exc
    time.sleep(0.3)
    after = os.path.getsize(log_path) if os.path.exists(log_path) else 0
    if after <= before:
        return False, ("端口 %d 上已有别的 mock 进程，它没把请求日志写进 %s。"
                       "请先停掉那个进程（或换 OCTOPUS_MOCK_PORT 再跑）后重试。" % (MOCK_PORT, log_path))
    return True, "mock 请求日志正常写入 %s" % log_path


def stop_mock(keep):
    if _mock_proc and not keep:
        _mock_proc.terminate()
        try:
            _mock_proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            _mock_proc.kill()
        log("mock 上游已停止")


def build_display_cjs():
    """把日志卡片的纯函数模块单独编译成 cjs，供 node 自检脚本 require。"""
    src = os.path.join(ROOT, "web", "src", "components", "modules", "log", "display.ts")
    out = os.path.join(HERE, "display.cjs")
    if not os.path.exists(src):
        return False, "缺少 web/src/components/modules/log/display.ts"
    cmd = ["npx", "esbuild", src, "--format=cjs", "--platform=node", "--outfile=" + out]
    proc = subprocess.run(cmd, cwd=os.path.join(ROOT, "web"), capture_output=True, text=True, shell=True)
    if proc.returncode != 0 or not os.path.exists(out):
        return False, (proc.stderr or proc.stdout or "").strip()[:200]
    return True, out


def dump_real_logs():
    """导出最近两条真实 relay_logs，供日志卡片真实数据等价性校验使用。"""
    if not os.path.exists(DB):
        return False, "找不到本地库 " + DB
    try:
        conn = sqlite3.connect("file:%s?mode=ro" % DB.replace("\\", "/"), uri=True)
        cols = [row[1] for row in conn.execute("pragma table_info(relay_logs)").fetchall()]
        if not cols:
            return False, "relay_logs 表不存在"
        rows = [dict(zip(cols, row)) for row in conn.execute("select * from relay_logs order by id desc limit 2")]
        conn.close()
    except Exception as exc:  # noqa: BLE001
        return False, "读取真实日志失败: %s" % exc
    if not rows:
        return False, "relay_logs 还没有数据（先跑几个请求）"
    with open(os.path.join(HERE, "real_logs.json"), "w", encoding="utf-8") as fh:
        json.dump(rows, fh, ensure_ascii=False, indent=1, default=str)
    return True, "已导出 %d 行真实日志" % len(rows)


SUMMARY_RE = re.compile(r"total=(\d+)\s+pass=(\d+)\s+fail=(\d+)|pass=(\d+)\s+fail=(\d+)")


def parse_summary(text):
    matches = SUMMARY_RE.findall(text)
    if not matches:
        return None
    # 两种写法：total=N pass=N fail=N，或只有 pass=N fail=N（findall 会同时给出两组，取最后一处命中）
    total, passed, failed, passed2, failed2 = matches[-1]
    if total != "":
        return int(total), int(passed), int(failed)
    return int(passed2) + int(failed2), int(passed2), int(failed2)


def run_step(command, cwd=HERE):
    # 子进程输出统一按 UTF-8 解码（中文断言/汇总行在 GBK 控制台下会解码失败，导致汇总行被吞）。
    proc = subprocess.run(command, cwd=cwd, capture_output=True, text=True, encoding="utf-8",
                          errors="replace", shell=isinstance(command, str))
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def main():
    parser = argparse.ArgumentParser(description="octopus 本地 API 测试总运行器")
    parser.add_argument("--only", help="只跑指定套件，逗号分隔（见 --list）")
    parser.add_argument("--list", action="store_true", help="列出套件后退出")
    parser.add_argument("--keep-mock", action="store_true", help="跑完不停止本运行器启动的 mock")
    args = parser.parse_args()

    if args.list:
        for key, script, desc, needs in SUITES:
            print("%-10s %-28s %s（依赖：%s）" % (key, script, desc, needs))
        return 0

    selected = set(args.only.split(",")) if args.only else None
    suites = [s for s in SUITES if not selected or s[0] in selected]
    if selected:
        unknown = selected - {s[0] for s in SUITES}
        if unknown:
            log("未知套件：%s" % ", ".join(sorted(unknown)))
            return 2

    results = []
    if not ensure_instance():
        return 2

    need_mock = any("mock" in s[3] for s in suites)
    if need_mock:
        if not ensure_mock():
            return 2
        ok, detail = mock_log_check()
        log(detail)
        if not ok:
            return 2

    for key, script, desc, needs in suites:
        # 依赖准备
        if "node" in needs or "esbuild" in needs:
            if key == "display":
                ok, detail = build_display_cjs()
                if not ok:
                    results.append((key, desc, None, None, None, "依赖失败: " + detail))
                    continue
            if key == "reallog":
                ok, detail = dump_real_logs()
                if not ok:
                    results.append((key, desc, None, None, None, "依赖失败: " + detail))
                    continue

        started = time.time()
        if script.endswith(".js"):
            code, out = run_step(["node", script])
        else:
            code, out = run_step([sys.executable, script])
        elapsed = round(time.time() - started, 1)
        parsed = parse_summary(out)
        tail = "\n".join([line for line in out.strip().split("\n") if line.strip()][-3:])
        if parsed:
            total, passed, failed = parsed
            results.append((key, desc, total, passed, failed, "%ss" % elapsed))
            log("%-10s %s  %d/%d 通过  (%ss)" % (key, "PASS" if failed == 0 else "FAIL", passed, total, elapsed))
        else:
            state = "PASS(exit0)" if code == 0 else "FAIL(exit%d)" % code
            results.append((key, desc, None, None, None, "%s, %ss" % (state, elapsed)))
            log("%-10s %s  未识别汇总行，末行：%s" % (key, state, tail.split("\n")[-1][:120] if tail else ""))
            if code != 0:
                print(out[-1500:])

    stop_mock(args.keep_mock)

    print("\n================ 本地 API 测试汇总 ================")
    bad = 0
    for key, desc, total, passed, failed, note in results:
        if total is None:
            verdict = note if note.startswith(("PASS", "FAIL")) or "依赖失败" in note else "跳过"
            if "FAIL" in note or "依赖失败" in note:
                bad += 1
        else:
            verdict = "%d/%d" % (passed, total)
            if failed:
                bad += 1
        print("%-10s %-34s %s" % (key, desc[:34], verdict))
    print("--------------------------------------------------")
    print("套件 %d 个，未通过 %d 个" % (len(results), bad))
    return 0 if bad == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
