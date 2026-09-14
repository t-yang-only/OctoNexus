# 认领：T-group-002 分组嵌套选路递归展开（NM-CUR-164）

- 登记台：`docs/agents/ledger.md` T-group-002（todo，无认领 Agent；162 行 claim 保留参考但其会话未关行，本行负责 A 车道 W1#2）
- 来源：R-group-001 + ledger T-group-002 登记语义 + 158 claim 设计（158 本人要求语义冻结与四单测）
- 前置：T-group-001 done（158 指导收口：013 迁移重跑+演练单测，门已开）
- 范围：
  1. `op.FlattenGroupItems(group)`：DFS 展平（引用行位置 splice；路径回边剪除/超 GroupItemMaxDepth 截断/成员按 ID 去重/缺失引用跳过）；
  2. `groupSnapshot` 子分组成员 `Available` 按展平后代回填（兑现注释承诺），不展开整树；
  3. relay `PickGroupItem(group, flat)` 可测外壳 + `handler` 每轮展平后传入；亲和/冷却/上限键挂顶层（语义冻结）；
  4. 四单测（展平顺序/防循环/深度截断/空树）+ relay 选路语义测试（冷却跳位/亲和保持/空树零值/手动模式）；
  5. 清理仓内已失效的并行产物 `internal/relay/group_tree*.go`（158 误判为"已在树"后他方已改走 Pickshell 路线，该双文件引用不存在的 load 回调与私函数，已不被任何代码引用且 build 失败）。
- 红线：不改 T-group-001 模型/迁移；不改 T-group-003 前端；不碰 B/C 车道 doing 线；转发失败仍只记事件不阻断等待语义。
- 验收：`go build -tags=jsoniter` + `go vet ./...` + `go test -count=1 ./...` + 双 check + `tsc`；新增单测覆盖四口径。
