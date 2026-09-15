# CLAIM: T-export-001 请求级明细导出（CSV + 面板按钮）

- 任务：T-export-001（来源 U-key-001 / 需求事实源 §4 第 5 条）
- 认领：dsh-local（NM-DS-013），2026-09-16
- 锁定范围：`internal/op/log.go`（抽出共用查询）、`internal/op/log_export.go`（新）、`internal/op/log_export_test.go`（新）、
  `internal/server/handlers/log.go`（新增一个路由与处理函数）、`web/src/api/log.ts`、
  `web/src/components/modules/log/FilterBar.tsx`、三语 `locales/*.json`、
  `scripts/api-tests/run_log_export_audit.py`、`scripts/api-tests/run_all.py`（登记套件）、
  `docs/worklog/2026-09-16-请求级明细导出.md`、`docs/requirements/2026-09-15-需求事实源整合.md`（状态列）、`agent_word/*`。
- 不碰：转发与统计写入链路、`/history` 的既有分页语义与响应形状、`relay_logs` 结构（不新增列）。
- 验证口径：op 包导出单测 3 例；活体 `run_log_export_audit.py` 11 例（含与 `/history` 的字段级等价）；
  全量 `python scripts/api-tests/run_all.py`；`go test ./... -count=1`；`tsc --noEmit` + `pnpm build`。
- 状态：已完成（见 ledger 本行备注与 worklog/2026-09-16-请求级明细导出.md）。
