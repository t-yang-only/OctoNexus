# 认领：T-group-002 分组嵌套选路递归展开（NM-CUR-165）

- 认领 Agent：cursor-local（2026-09-14）
- 登记台：T-group-002（A 车道 W1#2；开工时行为 todo 无认领，实测已有 162 单主 done，本轮改验证收尾不抢线）
- 前置：T-group-001 done（158 指导收口：013 迁移演练单测 4 例过）
- 承接现状（实测在树产出，本轮验收+收尾而非重写）：
  - `op.FlattenGroupItems` + `flattenInto`：DFS 展平（引用行位置 splice、路径回边剪除、超 GroupItemMaxDepth 截断、按成员行 ID 去重、缺失引用跳过）已在树；
  - `groupSnapshot` 子分组成员 Available 按展平后代回填已在树；
  - `model.Group.WithItems` + `relay.PickGroupItem(group, flat)` 可测外壳已在树；
  - handler 每轮 `op.FlattenGroupItems` 后传入选路已在树；
  - 四单测（展平顺序/防循环/深度截断/空树）`internal/op/group_flat_test.go` 已在树。
- 本轮增量：
  1. 修 manual 分支子分组块选中：`ActiveItemID` 指向子分组引用行时，当前应回零值等待
     （顶层 leaf 直选语义不变）；先收敛为确定行为并补单测锁口径；
  2. 补 relay 选路语义单测（冷却跳位/亲和无操作路径/空树零值/手动直选）；
  3. 全量验证：`go vet ./...` + `go test ./... -count=1`（op/relay -count=3）+ `go build -tags=jsoniter`；
  4. 清理确认：`internal/relay/group_tree*.go` 已不存在（164 claim 记录与实盘一致），无死引用。
- 红线：不改 T-group-001 模型/迁移；避让 169 在途的 T-group-003 前端四文件；不碰 B/C/E 车道 doing 线；
  转发失败仍只记事件不阻断等待语义。
- 收尾：T-group-002 维持 162 单主 done（本轮只追备注）；本 claim 关行；worklog 见 2026-09-14-T-group-002验证收尾165.md。
