# 认领：T-group-002 分组嵌套选路递归展开（递归解析 + 语义冻结 + 深度截断）

- 认领 Agent：cursor-local（NM-CUR-119）
- 认领时间：2026-09-13
- 登记台：`docs/agents/ledger.md` T-group-002（todo，无认领）
- 未选 T-pool-001（075/076 claim 在仓，118 等 30+ 行 owner 在途）、T-quota-002（含自动停用动作，需 T-quota-001 数据先行）、
  T-group-003（纯 UI，与本任务无依赖但可并行，留给前端会话）；按"依赖刚就绪、可独立交付"选 T-group-002。
- 前置：T-group-001 已 review（113 模型层：`ChildGroupID/ChildGroupName` + 互斥校验 + 防循环 + 013 迁移，未提交在树上）。
- 来源：R-group-001（需求登记.md，待确认）+ ledger T-group-002 登记语义。
- 范围（只做选路展开，不碰模型层/UI）：
  1. `internal/relay/group_tree.go`：按树形递归解析分组（子分组引用展开为叶子授权成员；
     防循环二次校验 + `GroupNestingMaxDepth` 深度截断；空树/全不可用返回哨兵错误）。
  2. 语义冻结：亲和（`MemberAffinitySeconds`）、冷却（`MemberCooldownSeconds`）、
     重试上限（`MemberMaxAttempts`）一律按**顶层分组**口径执行，子分组自带 RelayConfig 不下钻；
     故障切换顺序按展开后的叶子顺序（父成员顺序优先，子内保持原子块）。
  3. 手动模式：`ActiveItemID` 指向子分组引用成员时，整块子成员视为当前选中。
  4. 单测：纯内存构造分组树（无需 DB），覆盖展平顺序/防循环/深度截断/空树/语义冻结声明。
- 红线：不改模型层字段与迁移；不改前端；密钥不贴原文；失败只记事件不阻断转发。
- 验收：`go build -tags=jsoniter` + `go vet ./...` + `go test -count=1 ./...` +
  双 check 脚本；新增单测覆盖主路径。
