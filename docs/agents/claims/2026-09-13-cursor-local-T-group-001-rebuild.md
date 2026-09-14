# 认领：T-group-001 分组嵌套数据模型（重建，148 终裁后）

- 认领 Agent：cursor-local（NM-CUR-141）
- 认领时间：2026-09-13 21:43
- 登记台：`docs/agents/ledger.md` T-group-001（todo，无认领，A 车道）
- 前置：W0-1 不过门终裁（148）；113 产物灭失、134 复核无落盘
- 交付口径（148 裁决引用 107 拆分）：
  1. `GroupItem` 支持引用子分组：`ChildGroupID` 可空 + 与 `ChannelGrantID` 互斥校验
  2. 防循环：子分组链不得成环，深度上限约束
  3. `internal/db/migrate/013.go` 迁移：删旧唯一索引/按新形状校验（不与 113 旧稿冲突，从零写）
  4. 单测：嵌套引用匹配、互斥、防循环、迁移幂等
  5. `go build/vet/test -count=1` 全绿；不改 relay 选路（T-group-002 职责）
- 状态：进行中
