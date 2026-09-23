#!/usr/bin/env python3
"""T-verify-005 渠道可用性的权威判据：**只信真实调用**。

## 为什么单独建这一组

2026-09-23 实测暴露了一个我犯了**两次**的错误模式：
把探测工具自身的问题，当成了被探测对象的问题。

  第 8 轮：上游 `/v1/models` 清单不完整 → 我判"模型不可用"
            （实际那个模型当时能调通）
  第 30 轮：Python 被 Cloudflare 封 UA（403 error code 1010）
            + 我拿库里的密文当明文 key 发（401）
            → 我判"openagents 的 key 403、渠道不可用"
            （实际通过 octopus 调用是 HTTP 200）

两次都是"我的测量方式有毛病，却去报告被测系统有毛病"。

## 这一组的三条硬约束

  1. **只信真实调用**：`POST /v1/chat/completions` 拿到 2xx 才算可用。
     不接受任何间接证据 —— 清单、握手、状态码之外的一切都不算。
  2. **走 octopus 的转发口**，不直连上游：
     直连要自己处理凭据解密（库里的 key 是 `enc:v1:` 密文，
     直接当明文发必然 401 —— 这正是我上次的错误）、
     还可能被 Cloudflare 按 UA 拦（`error code: 1010`）。
     **octopus 内部会解密、会用它自己的客户端**，所以转发口的结果
     才是"这个渠道对用户是否可用"的权威答案。
  3. **区分 HTTP 状态与业务结果**：HTTP 200 证明渠道通；
     `choices` 为空是内容问题（如 max_tokens 太小全用在思考上），
     **不能当成渠道故障** —— 那是另一类问题。

## 与 verify-models 的分工

  verify-models（T-verify-004）  测**单个渠道**的**全部授权模型**是否被上游接受
  本组（T-verify-005）            测**端到端**：从客户端协议进来、经选路、到上游、再回来
                                  —— 它覆盖 octopus 自身的全部环节（解密/改写/转换）
"""
import json
import sqlite3
import sys
import time

ADMIN = "http://127.0.0.1:33100"
RELAY_PORT = 33101
DB = "/opt/octonexus-deta/data/data.db"
USER, PWD = "yang", "sois=ting"

RESULTS = []


def record(name, ok, detail=""):
    RESULTS.append((name, bool(ok), str(detail)[:160]))
    print("%s  %-56s %s" % ("PASS" if ok else "FAIL", name, str(detail)[:70]))


ck = {}


def call(method, path, body=None, timeout=30):
    import urllib.error
    import urllib.request
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(ADMIN + path, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if ck.get("v"):
        req.add_header("Cookie", ck["v"])
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            for h in resp.headers.get_all("Set-Cookie") or []:
                ck["v"] = h.split(";")[0]
            return resp.status, resp.read().decode("utf-8", "replace")
    except urllib.error.HTTPError as e:
        return e.code, e.read(3000).decode("utf-8", "replace")
    except Exception as e:  # noqa: BLE001
        return 0, str(e)


def relay_chat(api_key, model, prompt="say hi", max_tokens=16, timeout=120):
    """经转发口发一次真实对话请求。

    这是本组唯一的可用性判据 —— 拿到 200 才算这个渠道对用户可用。
    """
    import http.client
    body = json.dumps({
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": max_tokens,
    })
    conn = http.client.HTTPConnection("127.0.0.1", RELAY_PORT, timeout=timeout)
    t0 = time.time()
    try:
        conn.request("POST", "/v1/chat/completions", body=body.encode(),
                     headers={"Content-Type": "application/json",
                              "Authorization": "Bearer " + api_key})
        resp = conn.getresponse()
        raw = resp.read().decode("utf-8", "replace")
        return resp.status, raw, time.time() - t0
    except Exception as e:  # noqa: BLE001
        return 0, str(e), time.time() - t0
    finally:
        conn.close()


print("=" * 70)
code, _ = call("POST", "/api/v1/user/login", {"username": USER, "password": PWD})
record("登录管理面", code == 200, "HTTP %s" % code)
if code != 200:
    sys.exit(1)

con = sqlite3.connect(DB)
row = con.execute("select api_key from api_keys where id = 1").fetchone()
con.close()
if not row:
    record("取一把转发 Key", False, "库里没有 id=1 的 Key")
    sys.exit(1)
api_key = row[0]

# ---------------------------------------------------------------- 端到端可用性
print()
print("=" * 70)
print("端到端真实调用（这是唯一的可用性判据）")
# 选几个有代表性的分组：多成员聚合分组 + 单成员分组。
TARGETS = ["High-flash", "Max-flash", "openagents/kimi-k3", "StepFun/step-5-preview"]
for model in TARGETS:
    code, raw, elapsed = relay_chat(api_key, model)
    if code == 200:
        try:
            d = json.loads(raw)
            has_choice = bool(d.get("choices"))
        except Exception:  # noqa: BLE001
            has_choice = False
        # **HTTP 200 就是渠道可用的证据**；choices 为空是内容问题，不作为失败。
        record("真实调用 %s" % model, True,
               "HTTP 200  %.1fs  choices=%s" % (elapsed, "有" if has_choice else "空(内容问题，非渠道故障)"))
    else:
        record("真实调用 %s" % model, False,
               "HTTP %s  %.1fs  %s" % (code, elapsed, raw[:70]))

# ------------------------------------------------------- 反向：伪造 Key 必须被拒
print()
print("=" * 70)
print("反向对照：伪造的转发 Key 必须被拒（否则上面的 200 证明不了什么）")
code, raw, _ = relay_chat("sk-octopus-FAKE-KEY-FOR-TEST", "High-flash", max_tokens=4)
record("伪造 Key 被拒", code == 401, "HTTP %s %s" % (code, raw[:50]))

# ------------------------------------------------------- 反向：不存在的模型
print()
print("=" * 70)
print("反向对照：不存在的分组名必须失败（否则 200 是假象）")
code, raw, _ = relay_chat(api_key, "no-such-group-zzz", max_tokens=4)
record("不存在的模型被拒", code != 200, "HTTP %s" % code)

print()
print("=" * 70)
failed = [r for r in RESULTS if not r[1]]
print("TOTAL %d, FAILED %d" % (len(RESULTS), len(failed)))
for name, _, detail in failed:
    print("  FAILED: %s  %s" % (name, detail))
sys.exit(1 if failed else 0)
