# 认领：T-group-002 分组嵌套选路递归展开（NM-CUR-174）

- 登记台：`docs/agents/ledger.md` T-group-002（todo，无认领 Agent；A 车道 W1#2）
- 前置：T-group-001 done（158 指导收口：013 迁移重跑+演练单测，门已开）
- 背景：162/164/167/168 均登记认领但工作区无 tree 落盘（`internal/relay/group_tree*.go` 不存在，
  relay 无 flatten 代码），按 ledger 163 冻结令重领后开工
- 范围（登记台口径）：
  1. `op.FlattenGroupItems(group)`：DFS 展平（引用行位置 splice；路径回边剪除/
     超 GroupItemMaxDepth 截断/成员按 ID 去重/缺失引用跳过）；
  2. `groupSnapshot` 子分组成员 `Available` 按展平后代回填；
  3. relay `pickGroupItem` 改收展平列表（纯函数，manual/failover 双分支；亲和/冷却/上限键按顶层语义不变）；
  4. 四单测（展平顺序/防循环/深度截断/空树）+ relay 选路语义测试（冷却跳位/亲和保持/空树零值）。
- 红线：不改 T-group-001 模型/迁移已完成部分；不改 T-group-003 前端（169 i18n 已交付）；
  不碰 B/C/E 车道 doing 线；转发失败仍只记事件不阻断等待语义。
- 验收：`go build -tags=jsoniter` + `go vet ./...` + `go test -count=1 ./...` +
  `internal/op` `internal/relay` `-count=3` + 双 check。
