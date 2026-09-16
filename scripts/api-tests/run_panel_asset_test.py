#!/usr/bin/env python3
"""面板静态资源套件：验证"页面确实随二进制发布了"，而不只是源码里存在。

为什么单独立一条套件：前端要进二进制必须走「停实例 → pnpm build → go build → 重启」，
漏掉任何一步的效果都是"源码里明明有、打开面板却没有"。这类回归不会让任何后端测试变红，
所以由这条套件盯着：它直接看正在服务的产物里有没有这个页面的标记。

用法：python run_panel_asset_test.py（需要实例在跑，默认 http://127.0.0.1:13303）
"""

import glob
import gzip
import os
import re
import sys
import urllib.error
import urllib.request

ADMIN = os.environ.get("OCTOPUS_ADMIN_URL", "http://127.0.0.1:13303")
ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
ASSETS = os.path.join(ROOT, "static", "out", "assets")

PASSED = 0
FAILED = 0


def record(name, ok, detail=""):
    global PASSED, FAILED
    if ok:
        PASSED += 1
        print("PASS  %s :: %s" % (name, detail), flush=True)
    else:
        FAILED += 1
        print("FAIL  %s :: %s" % (name, detail), flush=True)


def fetch(url):
    """返回 (status, content_type, bytes)；网络错误返回 (0, '', b'')。"""
    try:
        with urllib.request.urlopen(url, timeout=15) as response:
            return response.status, response.headers.get("Content-Type", ""), response.read()
    except urllib.error.HTTPError as error:
        return error.code, error.headers.get("Content-Type", ""), error.read()
    except Exception as error:  # noqa: BLE001 - 连不上就是失败，原因照实显示
        return 0, "", str(error).encode("utf-8")


def asset_text(path):
    """读构建产物文本；vite 只留 .js.gz 时先解压。"""
    raw = open(path, "rb").read()
    if path.endswith(".gz") or raw[:2] == b"\x1f\x8b":
        raw = gzip.decompress(raw)
    return raw.decode("utf-8", errors="replace")


def main():
    status, content_type, body = fetch(ADMIN + "/")
    text = body.decode("utf-8", errors="replace")
    record(
        "P1 首页返回 HTML（面板本身可访问）",
        status == 200 and "text/html" in content_type.lower() and "<script" in text,
        "HTTP %s type=%s 长度=%d" % (status, content_type, len(body)),
    )

    scripts = re.findall(r'src="([^"]+\.js)"', text)
    entry_ok = False
    entry_detail = "HTML 里没有入口脚本引用"
    for src in scripts[:4]:
        url = src if src.startswith("http") else ADMIN + (src if src.startswith("/") else "/" + src)
        code, script_type, script_body = fetch(url)
        if code == 200 and "javascript" in script_type.lower() and len(script_body) > 1000:
            entry_ok = True
            entry_detail = "%s → HTTP 200 type=%s %d KB" % (src, script_type, len(script_body) // 1024)
            break
        entry_detail = "%s → HTTP %s type=%s" % (src, code, script_type)
    record("P2 入口脚本真的可下载（不是 404 的空壳页面）", entry_ok, entry_detail)

    files = sorted(glob.glob(os.path.join(ASSETS, "*.js")) + glob.glob(os.path.join(ASSETS, "*.js.gz")))
    if not files:
        record("P3 构建产物含号池页标记", False, "static/out/assets 下没有 js 产物")
        print("PANEL total=%d pass=%d fail=%d" % (PASSED + FAILED + 1, PASSED, FAILED + 1))
        return 1

    # 页面是懒加载的，可能落在任意 chunk 里：对全部产物取并集再找标记。
    bundle = "\n".join(asset_text(path) for path in files)
    markers = ["unifiedPool", "/api/v1/pool/kinds", "/api/v1/pool/entries"]
    missing = [marker for marker in markers if marker not in bundle]
    record(
        "P3 构建产物含号池页标记（前端是否真的重建过）",
        not missing,
        "产物 %d 个，缺 %s" % (len(files), missing or "无"),
    )

    batch_markers = ["批量探活", "Probe selected", "batchDisable"]
    missing_batch = [marker for marker in batch_markers if marker not in bundle]
    record(
        "P5 批量动作界面进了产物（选择/批量按钮真的构建了）",
        not missing_batch,
        "缺 %s" % (missing_batch or "无"),
    )

    notify_markers = ["alert_serverchan_sendkey", "Server酱推送", "ServerChan push"]
    missing_notify = [marker for marker in notify_markers if marker not in bundle]
    record(
        "P6 Server酱 通知渠道进了产物（设置键 + 三语文案）",
        not missing_notify,
        "缺 %s" % (missing_notify or "无"),
    )

    locales = {"简体": "统一号池", "繁體": "統一號池", "English": "Unified pool"}
    missing_locale = [name for name, marker in locales.items() if marker not in bundle]
    record(
        "P4 三语文案都进了产物（i18n 没漏语言）",
        not missing_locale,
        "缺 %s" % (missing_locale or "无"),
    )

    print("PANEL total=%d pass=%d fail=%d" % (PASSED + FAILED, PASSED, FAILED), flush=True)
    return 0 if FAILED == 0 else 1


if __name__ == "__main__":
    sys.exit(main())
