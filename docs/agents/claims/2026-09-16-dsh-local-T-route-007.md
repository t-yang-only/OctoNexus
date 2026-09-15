# CLAIM: T-route-007 lowest_tpm_rpm（按最近 60s 消耗选路）

- 任务：T-route-007（补齐路由策略族；需求侧遗留项 lowest_tpm_rpm）
- 认领：dsh-local（NM-DS-014），2026-09-16
- 锁定范围：`internal/relay/metrics.go`（负载窗口）、`internal/relay/route.go`（两处上报点 + 分发 + 热路径注入）、
  `internal/relay/state.go`（终态记 token）、`internal/relay/strategy.go`（定序与选择器 + `routeDeps.load`）、
  `internal/relay/rpm_test.go`（新）、`internal/model/group.go`（新模式 + IsValid + 三处 binding）、
  `web/src/api/group.ts`、`web/src/components/modules/group/Editor.tsx`、三语 `locales/*.json`、
  `scripts/api-tests/run_rpm_test.py`（新）、`scripts/api-tests/mock_upstream.py`（`usage_scale` 控制）、
  `scripts/api-tests/run_all.py`、`docs/worklog/2026-09-16-近期消耗最低选路.md`、
  `docs/requirements/2026-09-15-需求事实源整合.md`（状态列）、`agent_word/*`。
- 不碰：既有策略的排序口径、转发主链路（handler 的选路调用点除外）、统计与用量链路、通知与导出链路。
- 验证口径：relay 包单测 5 例；活体 `run_rpm_test.py` 10 例；全量 `python scripts/api-tests/run_all.py`（19 套件）；
  `go test ./... -count=1`；`tsc --noEmit` + `pnpm build`。
- 状态：已完成（见 ledger 本行备注与 worklog/2026-09-16-近期消耗最低选路.md）。
