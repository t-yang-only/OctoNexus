#!/usr/bin/env python3
"""T-usability-001/006/007/008/009 的活体测试。

## 为什么需要这一组

前 19 轮陆续加了五类"可用性诊断"能力。它们的单元测试都过了，
但**单元测试证明不了"在真实服务上能用"** —— 本轮之前每次都是手工敲命令验证的，
散在会话记录里，下次改动没人能一键复跑。

这组用例把那些手工验证固化成可重复执行的判据：
上的是真实数据库与真实转发口，走的是真实的 HTTP 请求。

## 判据取向

每条断言都盯**外部可观测事实**（HTTP 状态、响应字段、数值关系），
不断言"函数被调用过"。取不到数据时**如实报 FAIL 并说明原因**，
不用"跳过"掩盖 —— 跳过在输出里很像通过。
"""
import json
import sqlite3
import sys
import urllib.error
import urllib.request

ADMIN = "http://127.0.0.1:33100"
DB = "/opt/octonexus-deta/data/data.db"
USER, PWD = "yang", "sois=ting"

RESULTS = []


def record(name, ok, detail=""):
    RESULTS.append((name, bool(ok), str(detail)[:200]))
    print("%s  %s%s" % ("PASS" if ok else "FAIL", name, ("  :: " + str(detail)[:160]) if detail else ""))


ck = {}


def call(method, path, body=None, timeout=300):
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
        return e.code, e.read().decode("utf-8", "replace")
    except Exception as e:  # noqa: BLE001
        return 0, str(e)


def data_of(raw):
    try:
        return json.loads(raw).get("data")
    except Exception:  # noqa: BLE001
        return None


print("=" * 64)
print("登录")
code, _ = call("POST", "/api/v1/user/login", {"username": USER, "password": PWD})
record("A0 登录管理面", code == 200, "HTTP %s" % code)
if code != 200:
    sys.exit(1)

# ---------------------------------------------------------------- 配置层诊断
print()
print("=" * 64)
print("T-usability-001 配置层诊断")
code, raw = call("GET", "/api/v1/channel/diagnose")
d = data_of(raw) or {}
summary = d.get("summary") or {}
record("A1 诊断接口可用", code == 200 and bool(d), "HTTP %s" % code)
record("A2 汇总字段齐全",
       all(k in summary for k in ("channels", "total_models", "usable_models", "broken_models")),
       str(summary)[:120])
# 数值自洽：可用 + 不可用 = 总数。这一条能抓住"只改了一处计数"的常见错。
total = summary.get("total_models") or 0
usable = summary.get("usable_models") or 0
broken = summary.get("broken_models") or 0
record("A3 可用+不可用=总数", usable + broken == total,
       "usable=%s broken=%s total=%s" % (usable, broken, total))
# 原因必须可执行（不是"不可用"三个字）
reasons = list((d.get("reasons") or {}).keys())
record("A4 原因可执行", all(len(r) > 4 for r in reasons) if reasons else True,
       "原因样例: %s" % (reasons[:2],))

# ---------------------------------------------------------------- 通过率归因
print()
print("=" * 64)
print("T-usability-007/008 通过率与归因分类")
code, raw = call("GET", "/api/v1/log/fault-stats?window=500")
d = data_of(raw) or {}
record("B1 通过率接口可用", code == 200 and bool(d), "HTTP %s" % code)
window = d.get("window") or 0
parts = sum((d.get(k) or 0) for k in
            ("success", "canceled", "request_fault", "member_fault", "transient_fault", "unclassified"))
record("B2 分桶之和=窗口", parts == window, "sum=%s window=%s" % (parts, window))
# 渠道健康度必须排除「请求非法」—— 这是该功能的全部意义
bad = []
for ch in (d.get("channels") or []):
    denom = (ch.get("success") or 0) + (ch.get("member_fault") or 0) + (ch.get("transient_fault") or 0)
    expect = (100.0 * ch.get("success") / denom) if denom else 0
    if abs((ch.get("channel_rate") or 0) - expect) > 0.5:
        bad.append((ch.get("channel"), ch.get("channel_rate"), round(expect, 1)))
record("B3 渠道健康度排除请求非法", not bad, "偏离的渠道: %s" % (bad[:2],))

# ---------------------------------------------------------------- 上游核查
print()
print("=" * 64)
print("T-usability-006 配置 vs 上游")
con = sqlite3.connect(DB)
cid, cname = con.execute("select id, name from channels where enabled=1 order by id limit 1").fetchone()
con.close()
code, raw = call("GET", "/api/v1/channel/%d/upstream-check" % cid)
d = data_of(raw) or {}
record("C1 上游核查接口可用", code == 200 and bool(d), "渠道=%s HTTP %s" % (cname, code))
if d.get("probe_ok"):
    # 关键：字段名不能暗示"可用"（上游清单不完整，未列出 ≠ 调不通）
    record("C2 用 listed_count 而非 effective_count",
           "listed_count" in d and "effective_count" not in d, str(sorted(d.keys()))[:120])
    record("C3 带语义警告", bool(d.get("warning")), str(d.get("warning"))[:100])
    # 差异清单必须是真的差异（配置有、上游没有）
    cfg = d.get("configured_count") or 0
    listed = d.get("listed_count") or 0
    miss = len(d.get("missing_upstream") or [])
    record("C4 差异数自洽", listed + miss == cfg, "listed=%s missing=%s configured=%s" % (listed, miss, cfg))
else:
    record("C2 probe 失败时给出原因", bool(d.get("probe_error")), str(d.get("probe_error"))[:100])

# ---------------------------------------------------------------- 分组使用
print()
print("=" * 64)
print("T-usability-009 分组使用情况")
code, raw = call("GET", "/api/v1/group/usage")
d = data_of(raw) or {}
record("D1 分组使用接口可用", code == 200 and bool(d), "HTTP %s" % code)
record("D2 用过+没用过=总数",
       (d.get("used") or 0) + (d.get("unused") or 0) == (d.get("total") or 0),
       "used=%s unused=%s total=%s" % (d.get("used"), d.get("unused"), d.get("total")))
record("D3 自动+手工=总数",
       (d.get("auto") or 0) + (d.get("manual") or 0) == (d.get("total") or 0),
       "auto=%s manual=%s total=%s" % (d.get("auto"), d.get("manual"), d.get("total")))
# 按调用次数倒序：常用的在前
counts = [g.get("call_count") or 0 for g in (d.get("groups") or [])]
record("D4 按调用次数倒序", counts == sorted(counts, reverse=True), "前 5: %s" % counts[:5])

# ---------------------------------------------------------------- 实测（可选）
print()
print("=" * 64)
print("T-verify-004 真实调用级实测")
con = sqlite3.connect(DB)
row = con.execute("""select c.id, c.name from channels c
  join channel_models m on m.channel_id=c.id
  join channel_grants g on g.channel_model_id=m.id
  where c.enabled=1 group by c.id having count(g.id) between 1 and 2 limit 1""").fetchone()
con.close()
if row:
    cid, cname = row
    code, raw = call("POST", "/api/v1/channel/%d/verify-models" % cid)
    d = data_of(raw) or {}
    record("E1 实测接口可用", code == 200 and bool(d), "渠道=%s HTTP %s" % (cname, code))
    record("E2 结果计数自洽",
           (d.get("usable") or 0) + (d.get("unusable") or 0) == (d.get("total") or 0),
           "usable=%s unusable=%s total=%s" % (d.get("usable"), d.get("unusable"), d.get("total")))
    # 网络失败（status=0）与上游拒绝必须可分辨
    for r in (d.get("results") or []):
        if not r.get("usable"):
            if r.get("status") == 0:
                record("E3 网络失败给出原因", bool(r.get("error")), "%s: %s" % (r.get("model"), r.get("error")))
            else:
                record("E3 上游拒绝带状态码", r.get("status") > 0, "%s: HTTP %s" % (r.get("model"), r.get("status")))
            break
else:
    record("E1 找到可实测的渠道", False, "没有「已启用凭据+已授权」1-2 个模型的渠道，无法覆盖实测路径")

# ---------------------------------------------------------------- 汇总
print()
print("=" * 64)
failed = [r for r in RESULTS if not r[1]]
print("TOTAL %d, FAILED %d" % (len(RESULTS), len(failed)))
for name, _, detail in failed:
    print("  FAILED: %s  %s" % (name, detail))
sys.exit(1 if failed else 0)
