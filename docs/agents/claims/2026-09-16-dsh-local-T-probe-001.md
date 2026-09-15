# CLAIM: T-probe-001 主动探活（冷却成员提前解除冷却）

- 任务：T-probe-001（来源 R-probe-001 / 需求事实源 §4 第 1 条，U-mon-001 的主动探活缺口）
- 认领：dsh-local（NM-DS-011），2026-09-16
- 锁定范围：`internal/relay/probe.go`、`internal/relay/route.go`（仅新增 clearMemberCooldown）、
  `internal/op/route_probe.go`、`internal/task/probe.go`、`internal/task/init.go`（仅加注册）、
  `internal/model/setting.go`（仅新增两个键 + 两条 Validate）、`internal/server/handlers/setting.go`（仅加一条热更新 case）、
  `web/src/api/setting.ts`、`web/src/components/modules/setting/System.tsx`、三语 `locales/*.json`（仅加键）、
  `scripts/api-tests/run_probe_test.py`、`scripts/api-tests/mock_upstream.py`（新增 /__control）、
  `scripts/api-tests/run_all.py`（新增套件登记）、`docs/worklog/2026-09-15-主动探活.md`、
  `docs/requirements/2026-09-15-需求事实源整合.md`（状态列）、`agent_word/*`。
- 不碰：`internal/relay/handler.go` 的转发主循环、`balance.go` 的 SWRR、既有策略 picker、`op/http` 出站实现。
- 验证口径（完成后回填）：
  - 单测 `go test ./internal/relay/ -run TestProbe -count=1` 6 例；
  - 全量 `go test ./... -count=1` 九包；
  - 活体套件 `scripts/api-tests/run_probe_test.py`（15 例）；
  - 全量矩阵 `python scripts/api-tests/run_all.py`（16 套件）。
- 状态：已完成（见 ledger 本行备注与 worklog/2026-09-15-主动探活.md）。
