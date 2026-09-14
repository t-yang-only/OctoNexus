# 认领：T-audit-003 归零停用链路（T-quota-002）复核审计

- 认领 Agent：cursor-local（NM-CUR-150）
- 认领时间：2026-09-13
- 类型：已完成修改复核审计，不新增业务范围
- 背景：登记台无自由 todo（全部 todo 有车道前置未过门；doing 线归 B/C 车道，不交叉）；
  按用户指令转审计支线。已审：T-acct-003（146）、T-acct-002/004（149）。
  本轮审尚未审过的实质代码交付：T-quota-002（NM-CUR-124）。
- 审计对象：`internal/model/quota.go`、`internal/op/quota.go`、`internal/op/quota_test.go`、
  `internal/db/db.go` 的 AutoMigrate 接线
- 审计口径：停用粒度与幂等、缓存一致性（channelKeyCache/channelGrantCache/group 可用性）、
  分页边界、审计落库、竞态（-race/-count=3）、死代码；发现可确认 bug 才修，否则标审核过
- 注意：B 车道 117 正在做 T-quota-001 调度接线并消费本链路，本审计只读 + 仅在确认 bug 时改，
  避免与其交叉
