"""端到端验证：WebDAV 云备份（真实实例 + 真实 HTTP 桩）。

单测覆盖了协议细节，这里要证明的是**从管理面接口到远端文件落地**这条完整链路：
  1. 起一个最小 WebDAV 桩（PUT / PROPFIND / DELETE）；
  2. 通过管理面接口配置 webdav_url / enabled / keep；
  3. 调 /api/v1/backup/webdav/run → 桩里应该真的多出一个文件；
  4. 校验那个文件是**可解析的转储 JSON**（恢复时直接喂给导入接口）；
  5. 上传多次后按 keep 清理，且不误删桩里的无关文件；
  6. 非法 URL 被拒绝保存（400）——静默存下坏配置会让备份永远失败且没人知道。
  7. 清理：恢复设置、删掉测试产生的远端文件。
"""
import json
import os
import re
import sys
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer

ADMIN = "http://127.0.0.1:" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")
STUB_PORT = 18087
FILES = {}
LOCK = threading.Lock()
RESULTS = []
COOKIE = {}


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


class WebDAVStub(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass  # 静默：避免桩的访问日志淹没测试输出

    def _read_body(self):
        length = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(length) if length > 0 else b""

    def do_PUT(self):
        name = self.path.rsplit("/", 1)[-1]
        body = self._read_body()
        with LOCK:
            FILES[name] = body
        self.send_response(201)
        self.end_headers()

    def do_PROPFIND(self):
        with LOCK:
            names = list(FILES)
        parts = ['<?xml version="1.0"?><d:multistatus xmlns:d="DAV:">']
        for n in names:
            parts.append("<d:response><d:href>/dav/%s</d:href></d:response>" % n)
        parts.append("</d:multistatus>")
        payload = "".join(parts).encode()
        self.send_response(207)
        self.send_header("Content-Type", "application/xml")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def do_DELETE(self):
        name = self.path.rsplit("/", 1)[-1]
        with LOCK:
            FILES.pop(name, None)
        self.send_response(204)
        self.end_headers()


def call(method, path, payload=None, timeout=90):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if COOKIE.get("v"):
        req.add_header("Cookie", COOKIE["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for h in resp.headers.get_all("Set-Cookie") or []:
                COOKIE["v"] = h.split(";")[0]
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode("utf-8", "replace")
    except Exception as exc:  # noqa: BLE001
        return 0, str(exc)


def set_setting(key, value):
    return call("POST", "/api/v1/setting/set", {"key": key, "value": value})


def data_of(body):
    try:
        return json.loads(body).get("data")
    except Exception:
        return None


server = HTTPServer(("127.0.0.1", STUB_PORT), WebDAVStub)
threading.Thread(target=server.serve_forever, daemon=True).start()
BASE = "http://127.0.0.1:%d/dav" % STUB_PORT

try:
    print("== 1. 登录 ==")
    code, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    record("1 登录", code == 200, "code=%s" % code)
    if code != 200:
        print("RESULT: 无法继续")
        sys.exit(1)

    print()
    print("== 2. 非法 URL 必须被拒绝 ==")
    code, body = set_setting("webdav_url", "dav.example.com/octopus")
    record("2 缺协议头被拒 400", code == 400, "set => %s %s" % (code, body[:110]))

    print()
    print("== 3. 配置并立即备份 ==")
    code, body = set_setting("webdav_url", BASE)
    record("3 设置地址", code == 200, "code=%s" % code)
    set_setting("webdav_username", "tester")
    set_setting("webdav_keep", "2")

    with LOCK:
        FILES.clear()
        FILES["keep-me.txt"] = b"not ours"  # 无关文件：清理时不该被删

    code, body = call("POST", "/api/v1/backup/webdav/run", {})
    result = data_of(body) or {}
    record("3 立即备份返回 200", code == 200, "code=%s body=%s" % (code, body[:160]))
    uploaded = result.get("file") or ""
    record("3 返回了文件名", bool(uploaded), "file=%r" % uploaded)

    with LOCK:
        names = sorted(FILES)
    record("3 远端真的多出该文件", uploaded in names, "远端=%s" % names)

    print()
    print("== 4. 上传内容是合法转储 JSON ==")
    if uploaded:
        raw = FILES.get(uploaded, b"")
        try:
            dump = json.loads(raw.decode("utf-8"))
            has_version = isinstance(dump.get("version"), int) and dump["version"] > 0
            record("4 是合法 JSON 且带版本号", has_version,
                   "version=%s size=%d" % (dump.get("version"), len(raw)))
        except Exception as exc:  # noqa: BLE001
            record("4 是合法 JSON 且带版本号", False, "解析失败: %s" % exc)
    else:
        record("4 是合法 JSON 且带版本号", False, "上一步没拿到文件名")

    print()
    print("== 5. 保留份数生效且不误删他人文件 ==")
    # 此刻远端的本程序文件：2 个假造的旧备份 + 上一步新传的 1 份 = 3 份，keep=2。
    # 所以只该删掉最旧的那 1 份（20200101），而不是"删到只剩 2 份"之外的任何多余动作。
    with LOCK:
        FILES["octopus-backup-20200101-000000.json"] = b"{}"
        FILES["octopus-backup-20200102-000000.json"] = b"{}"
    code, body = call("POST", "/api/v1/backup/webdav/run", {})
    result = data_of(body) or {}
    pruned = result.get("pruned") or []
    with LOCK:
        names_after = sorted(FILES)
    own_after = [n for n in names_after if n.startswith("octopus-backup-")]
    record("5 只删最旧的一份", pruned == ["octopus-backup-20200101-000000.json"],
           "pruned=%s（3 份超出 keep=2，应恰好删 1 份）" % pruned)
    record("5 无关文件未被误删", "keep-me.txt" in names_after, "远端=%s" % names_after)
    record("5 清理后不超过保留份数", len(own_after) <= 2, "本程序文件数=%d %s" % (len(own_after), own_after))

    print()
    print("== 6. 列表接口 ==")
    code, body = call("GET", "/api/v1/backup/webdav/list")
    listing = data_of(body) or {}
    record("6 列表可用", code == 200 and listing.get("count", -1) >= 1,
           "code=%s count=%s" % (code, listing.get("count")))

finally:
    print()
    print("== 7. 清理 ==")
    set_setting("webdav_enabled", "false")
    set_setting("webdav_url", "")
    set_setting("webdav_username", "")
    set_setting("webdav_keep", "7")
    with LOCK:
        FILES.clear()
    print("  已恢复设置并清空桩文件")
    server.shutdown()

failed = [r for r in RESULTS if not r[1]]
print()
print("TOTAL %d, FAILED %d" % (len(RESULTS), len(failed)))
sys.exit(1 if failed else 0)
