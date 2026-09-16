#!/usr/bin/env python3
"""把"出问题了"推给用户（Server酱 / ServerChan 出口）。

为什么单独一个脚本：产品自己有告警渠道（设置页那六个），但那要实例在跑、还得真的触发业务事件；
这个脚本是**工具链**的出口 —— 测试套件失败、实验翻车、批处理出错时，人不在电脑前也能立刻知道。

凭据只从环境变量读（不落仓库、不回显）：
  OCTOPUS_SERVERCHAN_SENDKEY   必填：Server酱 SendKey
  OCTOPUS_SERVERCHAN_BASE_URL  可选：默认 https://sctapi.ftqq.com（测试时指向本地桩）

用法：
  python notify_problem.py --title "测试失败" --desp "poolapi 41/42：P26 断言失败" [--tag "服务器报警|报告"] [--dry-run]

退出码：0 = 已投递（或 --dry-run 只打印）；1 = 没配 SendKey；2 = 投递失败。
"""

import argparse
import datetime
import json
import os
import socket
import sys
import urllib.error
import urllib.parse
import urllib.request

DEFAULT_BASE_URL = "https://sctapi.ftqq.com"
TITLE_LIMIT = 64


def build_payload(title, desp, tags):
    """组出 Server酱 的表单字段：title/desp/tags（tags 形如 "服务器报警|报告"）。"""
    fields = {
        "title": "[octopus] " + title.strip()[:TITLE_LIMIT],
        "desp": desp.strip(),
    }
    if tags:
        fields["tags"] = tags
    return fields


def endpoint(base_url, sendkey):
    return "%s/%s.send" % (base_url.rstrip("/"), sendkey.strip())


def describe_result(status, text):
    """Server酱 是"HTTP 200 包错误码"那一类：code 与 data.errno 都为 0 才算成功。

    但它对无效 key 也可能直接回 400（实测）——所以非 2xx 也要把原文摘要带出来，
    否则排查时只知道"失败了"，不知道"为什么"。
    """
    summary = text.strip().replace("\n", " ")[:200]
    try:
        body = json.loads(text)
    except ValueError:
        if 200 <= status < 300:
            return False, "HTTP %s 但响应不是 JSON：%s" % (status, summary or "(空)")
        return False, "HTTP %s：%s" % (status, summary or "(空)")
    code = body.get("code")
    data = body.get("data") or {}
    errno = data.get("errno") if isinstance(data, dict) else None
    if code == 0 and (errno in (0, None)):
        return True, "已投递（code=0%s）" % ("" if errno is None else " errno=0")
    if 200 <= status < 300:
        return False, "业务错误码 code=%s errno=%s message=%s" % (
            code, errno, str(body.get("message"))[:120])
    return False, "HTTP %s：%s" % (status, summary or "(空)")


def send(fields, base_url, sendkey, timeout=20):
    data = urllib.parse.urlencode(fields).encode()
    request = urllib.request.Request(endpoint(base_url, sendkey), data=data, method="POST")
    request.add_header("Content-Type", "application/x-www-form-urlencoded")
    try:
        with urllib.request.urlopen(request, timeout=timeout) as response:
            return describe_result(response.status, response.read().decode("utf-8", errors="replace"))
    except urllib.error.HTTPError as exc:
        return describe_result(exc.code, exc.read().decode("utf-8", errors="replace"))
    except Exception as exc:  # 网络故障也要给出人话，而不是抛栈
        return False, "请求失败：%s" % exc


def main():
    parser = argparse.ArgumentParser(description="把一次问题推给用户（Server酱）")
    parser.add_argument("--title", required=True, help="一句话标题（会自动加 [octopus] 前缀）")
    parser.add_argument("--desp", required=True, help="正文（Markdown）")
    parser.add_argument("--tag", default="服务器报警|报告", help="Server酱 标签，形如 A|B")
    parser.add_argument("--dry-run", action="store_true", help="只打印将要发送的内容，不真的发")
    args = parser.parse_args()

    sendkey = os.environ.get("OCTOPUS_SERVERCHAN_SENDKEY", "").strip()
    base_url = os.environ.get("OCTOPUS_SERVERCHAN_BASE_URL", "").strip() or DEFAULT_BASE_URL
    fields = build_payload(args.title, args.desp, args.tag)

    if args.dry_run:
        print("DRY_RUN title=%s" % fields["title"])
        print("DRY_RUN desp=%s" % fields["desp"][:400])
        print("DRY_RUN tags=%s base=%s" % (fields.get("tags"), base_url))
        return 0

    if not sendkey:
        print("未配置 OCTOPUS_SERVERCHAN_SENDKEY：跳过推送（问题不会被静默吞掉，仍然打印在这里）")
        print("TITLE=%s" % fields["title"])
        print(fields["desp"])
        return 1

    ok, detail = send(fields, base_url, sendkey)
    print("NOTIFY_PROBLEM ok=%s detail=%s（host=%s）" % (ok, detail, socket.gethostname()))
    print("TITLE=%s" % fields["title"])
    print(fields["desp"])
    return 0 if ok else 2


if __name__ == "__main__":
    sys.exit(main())
