# 认领：T-audit-002 中转站登录（T-acct-002）与自定义余额（T-acct-004）复核审计

- 认领 Agent：cursor-local（NM-CUR-149）
- 认领时间：2026-09-13
- 类型：已完成修改复核审计，不新增业务范围
- 背景：登记台已无空闲 todo 待领（T-acct-005/T-pool-002/T-route-002/T-proto-001 均
  车道前置未过门，T-group-002 同）；上轮 T-acct-003 已审（146 审核过），本轮审计
  尚未审过的两个已完成任务的客户端实现
- 审计对象：
  1. `internal/health/relay_account.go` + `relay_account_client.go`（T-acct-002，129）
  2. `internal/health/custom_balance*.go`（T-acct-004，145，另一会话产出）
- 审计口径：鉴权头、超时/限读、宽容解析边界、错误吞没、测试覆盖、与既有口径一致性
  （anti-200-HTML / 失败不阻断转发）；发现可确认问题才修，否则标审核过
