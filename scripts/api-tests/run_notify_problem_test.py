#!/usr/bin/env python3
"""告警链路自检套件：验证"出问题真的能通知到我"这条路是通的。

这条链路是工具链的出口（不是产品的告警渠道，那六个另有套件）：
  scripts/api-tests/notify_problem.py            —— 推一条问题到 Server酱
  scripts/api-tests/run_all.py --notify          —— 有套件失败时自动走它
  scripts/api-tests/run_all.py --notify-selftest —— 只发自检，用来验证链路

设计要点（也是本套件要钉住的行为）：
- 凭据只从环境变量读（OCTOPUS_SERVERCHAN_SENDKEY），脚本自己不落库、不回显；
- 没配凭据时：退出码 1，并且**把要发的内容打印出来**（问题不能被静默吞掉）；
- 投递失败（业务错误码或非 2xx）时退出码 2，且把对方的错误原文带出来；
- 绿的时候不许打扰人：正常跑完的矩阵不发任何推送。

为了不真的打扰用户，本套件把 OCTOPUS_SERVERCHAN_BASE_URL 指向本地 mock 的推送桩。
"""

import json
import os
import subprocess
import sys
import urllib.parse
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
MOCK = os.environ.get("OCTOPUS_MOCK_URL", "http://127.0.0.1:18099")
MOCK_LOG = os.path.join(HERE, "requests.jsonl")
NOTIFY = os.path.join(HERE, "notify_problem.py")
RUN_ALL = os.path.join(HERE, "run_all.py")

STUB_BASE = MOCK
GOOD_KEY = "SCT-NOTIFYPROBLEM-good"
FAIL_KEY = "SCT-NOTIFYPROBLEM-fail-errno"

PASSED = 0
FAILED = 0


def safe(value):
    """GBK 控制台遇到替换字符（U+FFFD）会抛 UnicodeEncodeError —— 打印前先降级。"""
    encoding = sys.stdout.encoding or "utf-8"
    return str(value).encode(encoding, errors="replace").decode(encoding, errors="replace")


def record(name, ok, detail=""):
    global PASSED, FAILED
    if ok:
        PASSED += 1
        print(safe("PASS  %s :: %s" % (name, detail)), flush=True)
    else:
        FAILED += 1
        print(safe("FAIL  %s :: %s" % (name, detail)), flush=True)


def log_size():
    return os.path.getsize(MOCK_LOG) if os.path.exists(MOCK_LOG) else 0


def read_log(offset):
    if not os.path.exists(MOCK_LOG):
        return []
    with open(MOCK_LOG, "r", encoding="utf-8", errors="replace") as fh:
        fh.seek(offset)
        rows = []
        for line in fh:
            line = line.strip()
            if not line:
                continue
            try:
                rows.append(json.loads(line))
            except ValueError:
                continue
        return rows


def form_text(body):
    """桩里的 body 可能是表单字符串（且被百分号编码），也可能已被解析成 dict —— 都还原成人话再断言。"""
    if isinstance(body, dict):
        text = " ".join("%s=%s" % (key, value) for key, value in body.items())
    else:
        text = str(body or "")
    return urllib.parse.unquote_plus(text)


def run(args, env=None):
    merged = dict(os.environ)
    merged.pop("OCTOPUS_SERVERCHAN_SENDKEY", None)
    merged.pop("OCTOPUS_SERVERCHAN_BASE_URL", None)
    # 子进程必须按 UTF-8 输出：Windows 上被管道接住时默认是 GBK，
    # 那样中文断言会看到一堆替换字符（本轮踩过），不是子进程的错而是这里没交代编码。
    merged["PYTHONIOENCODING"] = "utf-8"
    merged["PYTHONUTF8"] = "1"
    if env:
        merged.update(env)
    proc = subprocess.run([sys.executable] + args, capture_output=True, text=True,
                          encoding="utf-8", errors="replace", env=merged)
    return proc.returncode, (proc.stdout or "") + (proc.stderr or "")


def main():
    # N0 环境：告警桩必须在（否则后面的"投递成功"根本无从谈起）
    try:
        with urllib.request.urlopen(MOCK + "/v1/models", timeout=5) as resp:
            mock_ok = resp.status == 200
    except Exception as exc:
        print("mock 上游不可用：%s" % exc)
        return 2
    if not mock_ok:
        print("mock 上游返回异常")
        return 2

    # N1 dry-run：不需要凭据，只打印将要发送的内容（内容要真的带上标题前缀与正文）
    code, out = run([NOTIFY, "--title", "干跑标题", "--desp", "干跑正文", "--dry-run"])
    record("N1 --dry-run 只打印不发送（无需凭据）",
           code == 0 and "DRY_RUN title=[octopus] 干跑标题" in out and "干跑正文" in out,
           "exit=%s 首行=%s" % (code, out.strip().splitlines()[0][:80] if out.strip() else "(空)"))

    # N2 没配凭据：退出码 1，但把要发的内容打印出来（问题不许被静默吞掉）
    code, out = run([NOTIFY, "--title", "没配凭据", "--desp", "正文"])
    record("N2 未配置 SendKey 时退出 1 且打印内容（不静默吞掉问题）",
           code == 1 and "OCTOPUS_SERVERCHAN_SENDKEY" in out and "没配凭据" in out,
           "exit=%s" % code)

    # N3 走桩成功：退出 0，桩真的收到表单（title/desp/tags 三件套）
    offset = log_size()
    code, out = run([NOTIFY, "--title", "套件失败", "--desp", "poolapi 41/42", "--tag", "服务器报警|报告"],
                    {"OCTOPUS_SERVERCHAN_SENDKEY": GOOD_KEY, "OCTOPUS_SERVERCHAN_BASE_URL": STUB_BASE})
    rows = [row for row in read_log(offset) if (row.get("path") or "").endswith(".send")]
    body = form_text(rows[0].get("body") if rows else "")
    record("N3 投递成功时退出 0，且桩收到 title/desp/tags 表单",
           code == 0 and "NOTIFY_PROBLEM ok=True" in out and len(rows) == 1
           and (rows[0].get("path") or "").endswith(GOOD_KEY + ".send")
           and "title" in body and "desp" in body and "tags" in body,
           "exit=%s posts=%d path=%s" % (code, len(rows), rows[0].get("path") if rows else None))

    record("N3b 凭据不出现在输出里（脚本不回显 SendKey）",
           GOOD_KEY not in out, "输出里是否含 SendKey：%s" % (GOOD_KEY in out))

    # N4 业务错误码：HTTP 200 包着 errno 非 0 也必须算失败（退出 2）
    code, out = run([NOTIFY, "--title", "业务错误", "--desp", "正文"],
                    {"OCTOPUS_SERVERCHAN_SENDKEY": FAIL_KEY, "OCTOPUS_SERVERCHAN_BASE_URL": STUB_BASE})
    record("N4 业务错误码（HTTP 200 + errno）判为失败并带出原文",
           code == 2 and "NOTIFY_PROBLEM ok=False" in out and "errno=" in out,
           "exit=%s 详情=%s" % (code, [line for line in out.splitlines() if "NOTIFY_PROBLEM" in line][:1]))

    # N4b 网络不可达：也要给出人话并退出 2（"发不出去"绝不能悄悄变成"没问题"）
    code, out = run([NOTIFY, "--title", "网络不可达", "--desp", "正文"],
                    {"OCTOPUS_SERVERCHAN_SENDKEY": GOOD_KEY,
                     "OCTOPUS_SERVERCHAN_BASE_URL": "http://127.0.0.1:18098"})
    record("N4b 网络不可达判为失败并给出原因",
           code == 2 and "NOTIFY_PROBLEM ok=False" in out and "请求失败" in out,
           "exit=%s 详情=%s" % (code, [line for line in out.splitlines() if "NOTIFY_PROBLEM" in line][:1]))

    # N4c 应答口径单测：直接喂真实站点那两种应答（本轮真的抓到过 400），确认判定正确
    import notify_problem

    real_400 = '{"message":"[AUTH]\\u9519\\u8bef\\u7684Key","code":40001,"info":"\\u9519\\u8bef\\u7684Key","args":[null],"scode":461}'
    ok_200, _ = notify_problem.describe_result(200, '{"code":0,"data":{"errno":0}}')
    bad_400, detail_400 = notify_problem.describe_result(400, real_400)
    bad_errno, _ = notify_problem.describe_result(200, '{"code":0,"data":{"errno":1001,"error":"bad sendkey"}}')
    record("N4c 应答判定：200+code0+errno0 才算成功，真实 400 与 errno≠0 都算失败",
           ok_200 and not bad_400 and not bad_errno and "HTTP 400" in detail_400,
           "200→%s 400→%s errno→%s" % (ok_200, bad_400, bad_errno))

    # N5 运行器自检：--notify-selftest 只发一条自检，走同一条链路，退出码反映链路是否通
    offset = log_size()
    code, out = run([RUN_ALL, "--notify-selftest"],
                    {"OCTOPUS_SERVERCHAN_SENDKEY": GOOD_KEY, "OCTOPUS_SERVERCHAN_BASE_URL": STUB_BASE})
    rows = [row for row in read_log(offset) if (row.get("path") or "").endswith(".send")]
    body = form_text(rows[0].get("body") if rows else "")
    record("N5 run_all --notify-selftest 走同一条链路（自检可达）",
           code == 0 and len(rows) == 1 and "告警通道自检" in body
           and "告警推送 exit=0" in out,
           "exit=%s posts=%d" % (code, len(rows)))

    # N5b 自检失败要能被看出来：桩返回业务错误时，自检退出码必须非 0（不能假装成功）
    code, _ = run([RUN_ALL, "--notify-selftest"],
                  {"OCTOPUS_SERVERCHAN_SENDKEY": FAIL_KEY, "OCTOPUS_SERVERCHAN_BASE_URL": STUB_BASE})
    record("N5b 自检投递失败时退出码非 0（不假装成功）", code == 1, "exit=%s" % code)

    # N6 绿的时候不许打扰人：正常跑完的矩阵（--notify）不该发任何推送
    offset = log_size()
    code, out = run([RUN_ALL, "--only", "panel", "--notify", "--keep-mock"],
                    {"OCTOPUS_SERVERCHAN_SENDKEY": GOOD_KEY, "OCTOPUS_SERVERCHAN_BASE_URL": STUB_BASE})
    rows = [row for row in read_log(offset) if (row.get("path") or "").endswith(".send")]
    record("N6 全绿时不发推送（只在失败时打扰人）",
           code == 0 and not rows and "未通过 0 个" in out,
           "exit=%s posts=%d" % (code, len(rows)))

    print("NOTIFY_PROBLEM_TEST total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED))
    return 1 if FAILED else 0


if __name__ == "__main__":
    sys.exit(main())
