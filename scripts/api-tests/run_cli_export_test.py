"""CLI 配置导出（吸收上游 lingyuins/octopus 的 CLI Config Export）活体套件。

口径：把「转发口地址 + 网关 Key + 分组名」组装成各客户端能直接粘贴的配置。

本套件要证明五件事（缺任何一条都不算交付）：

  E1 鉴权：接口匿名不可用——导出内容**含 Key 明文**，匿名可读等于把钥匙给出去。
  E2 五个目标都能导出，且**都带上模型名**（只给 base URL 与 key，用户还得回去翻面板）。
  E3 按 key id 取明文：面板不必为了导出一份配置先把 key 复制一遍。
  E4 校验可执行：缺地址/缺模型/非法目标要 400，且缺协议头的错误信息要指出该写 http(s)://
     （客户端对这种情况只会报含糊的连接失败）。
  E5 形态正确：Codex 给 config.toml 且 base_url 带 /v1、key 不写进文件；
     Claude Code 用 ANTHROPIC_AUTH_TOKEN 而不是 ANTHROPIC_API_KEY。
"""
import json
import os
import sqlite3
import sys
import urllib.error
import urllib.request

ROOT = r"D:\奇怪的软件\octopus"
DB = os.environ.get("OCTOPUS_DB", os.path.join(ROOT, "data", "data.db"))
ADMIN = "http://" + os.environ.get("OCTOPUS_ADMIN_HOST", "127.0.0.1") + ":" + os.environ.get("OCTOPUS_ADMIN_PORT", "13303")

COOKIE = {}
RESULTS = []
TARGETS = ["claude_code", "codex", "gemini_cli", "cherry_studio", "openai_compatible"]


def record(name, ok, detail):
    RESULTS.append((name, bool(ok), detail))
    print(("PASS  " if ok else "FAIL  ") + name + " :: " + detail)


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
    except urllib.error.HTTPError as exc:
        return exc.code, {"_raw": exc.read().decode()[:400]}
    except Exception as exc:  # noqa: BLE001
        return 0, {"_raw": "%s: %s" % (type(exc).__name__, exc)}


def call_anon(method, path, payload=None, timeout=30):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read().decode()[:200]
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read().decode()[:200]
    except Exception as exc:  # noqa: BLE001
        return 0, str(exc)


def scalar(sql, args=()):
    conn = sqlite3.connect("file:" + os.path.abspath(DB).replace("\\", "/") + "?mode=ro", uri=True)
    try:
        row = conn.execute(sql, args).fetchone()
        return row[0] if row else None
    finally:
        conn.close()


def login():
    status, _ = call("POST", "/api/v1/user/login", {"username": "admin", "password": "admin"})
    return status == 200


def generate(tool, base, model, key_id):
    status, body = call("POST", "/api/v1/cli-export/generate",
                        {"tool": tool, "base_url": base, "model": model, "api_key_id": key_id})
    return status, (body or {}).get("data") or {}


def main():
    if not login():
        record("E0 登录", False, "admin 登录失败")
        return 1
    record("E0 登录", True, "admin/admin 登录成功")

    key_id = scalar("select id from api_keys where enabled = 1 order by id limit 1")
    key_plain = scalar("select api_key from api_keys where enabled = 1 order by id limit 1")
    group = scalar("select name from groups order by id limit 1")
    if not key_id or not group:
        record("E0 装置", False, "库内没有可用 Key 或分组")
        return 1
    record("E0 装置", True, "key_id=%s group=%s" % (key_id, group))

    base = "https://gw.example.com"

    # --- E1 鉴权 -------------------------------------------------------------
    s1, _ = call_anon("POST", "/api/v1/cli-export/generate",
                      {"tool": "codex", "base_url": base, "model": group, "api_key_id": key_id})
    record("E1 匿名不可用", s1 in (401, 403), "anon=%s" % s1)

    # --- E2 五个目标都带上模型名 ---------------------------------------------
    missing = []
    for tool in TARGETS:
        status, data = generate(tool, base, group, key_id)
        content = data.get("content") or ""
        if status != 200 or group not in content:
            missing.append("%s(status=%s,hasModel=%s)" % (tool, status, group in content))
    record("E2 五个目标都能导出且带模型名", not missing, "问题项: %s" % (missing or "无"))

    # --- E3 按 key id 取明文 -------------------------------------------------
    status, data = generate("codex", base, group, key_id)
    steps = "\n".join(data.get("steps") or [])
    record("E3 服务端按 key id 取明文",
           status == 200 and key_plain and (key_plain in steps or key_plain in (data.get("content") or "")),
           "key 已注入导出结果（不在日志里回显）")

    # --- E4 校验 -------------------------------------------------------------
    bad = []
    code, _ = call("POST", "/api/v1/cli-export/generate", {"tool": "codex", "base_url": "", "model": group, "api_key_id": key_id})
    bad.append(("缺地址", code))
    code, _ = call("POST", "/api/v1/cli-export/generate", {"tool": "codex", "base_url": base, "model": "", "api_key_id": key_id})
    bad.append(("缺模型", code))
    code, _ = call("POST", "/api/v1/cli-export/generate", {"tool": "vscode", "base_url": base, "model": group, "api_key_id": key_id})
    bad.append(("非法目标", code))
    all400 = all(c == 400 for _, c in bad)
    # 缺协议头：错误信息必须指出该写 http(s)://
    code, body = call("POST", "/api/v1/cli-export/generate",
                      {"tool": "codex", "base_url": "gw.example.com", "model": group, "api_key_id": key_id})
    raw = json.dumps(body, ensure_ascii=False)
    hint_ok = code == 400 and "http" in raw
    record("E4 校验可执行（含协议头提示）", all400 and hint_ok,
           "%s；缺协议头=%s hint=%s" % (", ".join("%s=%s" % b for b in bad), code, hint_ok))

    # --- E5 形态正确 ---------------------------------------------------------
    _, codex = generate("codex", base, group, key_id)
    codex_ok = (codex.get("format") == "toml"
                and "%s/v1" % base in (codex.get("content") or "")
                and key_plain not in (codex.get("content") or ""))
    _, claude = generate("claude_code", base, group, key_id)
    claude_content = claude.get("content") or ""
    claude_ok = "ANTHROPIC_AUTH_TOKEN" in claude_content and "ANTHROPIC_API_KEY=" not in claude_content
    # 末尾斜杠要归一化，不能拼出 //v1
    _, slash = generate("codex", base + "/", group, key_id)
    slash_ok = "//v1" not in (slash.get("content") or "")
    record("E5 各客户端形态正确",
           codex_ok and claude_ok and slash_ok,
           "codex.toml=%s key不入文件=%s claude用AUTH_TOKEN=%s 斜杠归一=%s" %
           (codex.get("format") == "toml", key_plain not in (codex.get("content") or ""), claude_ok, slash_ok))

    print("\n" + "=" * 50)
    failed = [r for r in RESULTS if not r[1]]
    for name, ok, detail in RESULTS:
        if not ok:
            print("FAILED  " + name + " :: " + detail)
    print("TOTAL %d, FAILED %d" % (len(RESULTS), len(failed)))
    return 0 if not failed else 1


if __name__ == "__main__":
    sys.exit(main())
