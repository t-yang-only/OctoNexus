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

    locale_markers = ["streamIdleTimeout", "流式无进展上限", "串流無進展上限", "Stream idle limit"]
    missing_idle = [marker for marker in locale_markers if marker not in bundle]
    record(
        "P7 流式无进展上限进了产物（分组表单字段 + 三语文案）",
        not missing_idle,
        "缺 %s" % (missing_idle or "无"),
    )

    # P8 授权按凭据实际支持的模型收敛（上游 #387）：三语文案 + 界面真的引用了这个键，
    # 只查文案会漏掉"文案在但界面没连上"的空壳。
    grant_markers = ["grantUnsupported", "该凭据不支持此模型", "該憑證不支援此模型", "This key does not serve this model"]
    missing_grant = [marker for marker in grant_markers if marker not in bundle]
    record(
        "P8 授权按凭据支持的模型收敛进了产物（键被引用 + 三语文案）",
        not missing_grant,
        "缺 %s" % (missing_grant or "无"),
    )

    # P9 智能路由（mode = smart，对齐阶跃 Step Router 的用法）：模式选项、阈值字段与三语文案都要进产物。
    smart_markers = ["smart_route_threshold", "form.smartThreshold", "智能路由", "智能路由複雜度閾值",
                     "Smart routing complexity threshold"]
    missing_smart = [marker for marker in smart_markers if marker not in bundle]
    record(
        "P9 智能路由进了产物（模式选项 + 阈值字段 + 三语文案）",
        not missing_smart,
        "缺 %s" % (missing_smart or "无"),
    )

    # P10 日志卡片的上游轮次（上游 #395 的「重试详情」最小切片）：字段键被界面引用 + 三语文案进产物。
    attempts_markers = ["attempts", "上游轮次", "上游輪次", "Upstream rounds"]
    missing_attempts = [marker for marker in attempts_markers if marker not in bundle]
    record(
        "P10 日志卡片显示上游轮次（字段被引用 + 三语文案）",
        not missing_attempts,
        "缺 %s" % (missing_attempts or "无"),
    )

    # P11 总余额（T-balance-001）: 首页余额卡片、字段与三语文案都要进产物。
    balance_markers = ["balance/summary", "points_per_unit", "未读到余额", "未讀到餘額",
                       "No balance yet", "Monthly left"]
    missing_balance = [marker for marker in balance_markers if marker not in bundle]
    record(
        "P11 总余额卡片进产物（接口路径 + 未知余额文案 + 三语文案）",
        not missing_balance,
        "缺 %s" % (missing_balance or "无"),
    )

    # P12 判定理由 + 请求非法时的取向（T-decision-001 / T-retry-003）: 日志卡片字段与设置项都要进产物,
    # 三语文案齐全。前者让"为什么走了这个成员"在界面上可看, 后者让 400 类取向不必改配置文件。
    decision_markers = ["requestFaultFailfast", "relay_request_fault_action", "判定理由",
                        "换成员再试（默认）", "換成員再試（預設）", "Return the upstream error immediately"]
    missing_decision = [marker for marker in decision_markers if marker not in bundle]
    record(
        "P12 判定理由与取向开关进产物（日志字段 + 设置项 + 三语文案）",
        not missing_decision,
        "缺 %s" % (missing_decision or "无"),
    )

    # P13 智能路由的显式档位（T-smart-007）：档位按钮的文案与提示进产物，三语齐全。
    tier_markers = ["smart_tier", "tierDecision", "tierExecution", "智能路由档位", "智能路由檔位",
                    "Smart routing tier"]
    missing_tier = [marker for marker in tier_markers if marker not in bundle]
    record(
        "P13 智能路由显式档位进产物（字段 + 按钮文案 + 三语提示）",
        not missing_tier,
        "缺 %s" % (missing_tier or "无"),
    )

    # P14 手动订阅（R-acct-004): 无接口站点的手录入口与它的三条口径文案都要进产物, 三语齐全。
    # 标记挑的是「面板真的会调用/真的会显示」的东西：列表数据来自余额快照（manual_subscriptions），
    # 面板只调写接口（create/update/delete），因此不拿未被引用的 subscription/list 当标记
    # —— 未引用的导出会被打包器摇掉，拿它当标记会假红（本轮实测过一次）。
    manual_markers = ["subscription/create", "subscription/delete", "manual_subscriptions", "manual_total", "手录合计",
                      "手錄合計", "Manual total", "只有「启用 + 未过期」"]
    missing_manual = [marker for marker in manual_markers if marker not in bundle]
    record(
        "P14 手动订阅进产物（接口路径 + 口径文案 + 三语文案）",
        not missing_manual,
        "缺 %s" % (missing_manual or "无"),
    )

    # P15 分压与限流监控（T-allocate-001 / T-monitor-001）: 首页那一页要真的进产物 ——
    # 监控接口路径、分压模式的选项文案、分压设置项的键名与三语文案, 四类标记都要在 bundle 里找到。
    # 标记挑的是「面板真的会调用/真的会显示」的东西: 页面数据来自 /api/v1/monitor/allocation,
    # 设置键会被设置页的输入控件写到, 因此都不属于"未被引用会被摇掉"的那类。
    allocation_markers = [
        "monitor/allocation",                 # 监控页数据源
        "route_allocate_health_weight",       # 分压设置项（会被设置控件引用）
        "route_member_rpm_limit",
        "route_ratelimit_cooldown_max_seconds",
        "allocate",                           # 分组模式下拉里的选项值
        "额度分压", "額度分壓", "Quota spread",
        "分压与限流监控", "分壓與限流監控", "Quota spread and rate-limit monitor",
        "剩余请求数", "剩餘請求數", "Remaining requests",
    ]
    missing_allocation = [marker for marker in allocation_markers if marker not in bundle]
    record(
        "P15 分压与限流监控进产物（接口路径 + 模式文案 + 设置键 + 三语文案）",
        not missing_allocation,
        "缺 %s" % (missing_allocation or "无"),
    )

    # P16 速度维度进产物（T-speed-001）: 「速度不好」的应对也要在面板上可见可调 ——
    # 五个速度设置键（设置页控件会引用）+ 监控列与卡片 + 三语文案。看门狗倍数与下限这两个键
    # 是"会被写进设置表"的控件键名，监控列是页面真的渲染的列标题，因此都不是被摇掉的那类。
    speed_markers = [
        "route_allocate_speed_weight",        # 速度折扣强度（设置页控件引用）
        "route_speed_slow_ttfb_ms",
        "route_speed_slow_tps",
        "route_speed_first_event_multiple",   # 自适应首帧看门狗倍数
        "route_speed_first_event_floor_ms",
        "速度（首帧 / 吞吐）", "速度（首幀 / 吞吐）", "Speed (TTFB / throughput)",
        "速度应对（首帧与吞吐）", "速度應對（首幀與吞吐）", "Speed countermeasures",
        "慢成员", "慢成員", "Slow",
    ]
    missing_speed = [marker for marker in speed_markers if marker not in bundle]
    record(
        "P16 速度维度进产物（设置键 + 监控列 + 三语文案）",
        not missing_speed,
        "缺 %s" % (missing_speed or "无"),
    )

    # P17 入站正文上限（T-bodylimit-001）: 这一项之所以要给面板标记, 是因为它的**默认值**
    # 必须能被用户改到（Codex 大正文被自家网关挡掉时, 面板上得能找到开关）。
    bodylimit_markers = [
        "relay_max_request_body_bytes",
        "入站请求体上限", "入站請求體上限", "Inbound request body limit",
        "上限（字节）", "上限（位元組）", "Limit (bytes)",
    ]
    missing_bodylimit = [marker for marker in bodylimit_markers if marker not in bundle]
    record(
        "P17 入站正文上限进产物（设置键 + 三语文案）",
        not missing_bodylimit,
        "缺 %s" % (missing_bodylimit or "无"),
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
