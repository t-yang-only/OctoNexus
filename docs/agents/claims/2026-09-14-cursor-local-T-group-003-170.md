# 认领：T-group-003 分组嵌套前端 i18n 骨架（NM-CUR-170）

- 认领 Agent：cursor-local（2026-09-14）
- 登记台：`docs/agents/ledger.md` T-group-003（todo，F 车道 W2#1；无认领 Agent，120 claim 保留历史但已回退作废）
- 前置：T-group-001 done（158 指导收口）+ T-group-002 done（162 补齐四单测），门禁允许 i18n 骨架
- 范围（严格按登记台门禁）：只做三语 i18n 骨架（`nested` 展示键：子分组徽标/ unavailable 直达展开提示），
  禁写 `child_group_id` 提交字段（后端 GroupItemInput 已有该字段，但前端选择器写路径待 T-group-002 全量验收后另行开工）
- 不越线：不改后端 Go 文件；不碰 B/C 车道 doing 线；不改选路语义；Card/Editor/Create 提交路径一律不动
- 验收：`tsc --noEmit` + `go build -tags=jsoniter`（前端改动不影响后端，仅复验不断链）
