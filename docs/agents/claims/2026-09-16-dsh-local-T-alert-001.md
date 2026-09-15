# CLAIM: T-alert-001 多渠道通知（飞书/钉钉/企微/SMTP + 发送前真实测试）

- 任务：T-alert-001（来源 R-alert-001 / 需求事实源 §4 第 4 条，U-alert-001 的渠道层缺口）
- 认领：dsh-local（NM-DS-012），2026-09-16
- 锁定范围：`internal/notify/`（新增 channels.go 与 channels_test.go、webhook.go 仅加 Title 字段）、
  `internal/model/setting.go`（新增 10 个通知设置键与校验）、`internal/server/handlers/setting.go`（新增两个路由与处理函数）、
  `internal/task/quota_scan.go` 与 `internal/relay/probe.go`（仅把 PostWebhook 换成 Send）、
  `web/src/api/setting.ts`、`web/src/components/modules/setting/System.tsx`、三语 `locales/*.json`、
  `README.md`/`README_zh.md`（仅加一行环境变量说明）、`scripts/api-tests/run_notify_audit.py`、
  `scripts/api-tests/mock_upstream.py`（新增 /notify/* 桩）、`scripts/api-tests/run_all.py`（新增套件登记）、
  `docs/worklog/2026-09-16-多渠道通知.md`、`docs/requirements/2026-09-15-需求事实源整合.md`（状态列）、`agent_word/*`。
- 不碰：转发主链路（handler/route/strategy）、统计与用量链路、号池链路、既有 webhook 的报文形状（保持向后兼容）。
- 验证口径：notify 包单测 8 例；活体 `run_notify_audit.py` 14 例（含真实事件的端到端投递与设置复原）；
  全量 `python scripts/api-tests/run_all.py`（17 套件）；`go test ./... -count=1` 九包；`tsc --noEmit` + `pnpm build`。
- 状态：已完成（见 ledger 本行备注与 worklog/2026-09-16-多渠道通知.md）。
