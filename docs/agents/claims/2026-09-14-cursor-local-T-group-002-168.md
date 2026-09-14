# 认领：T-group-002 分组嵌套选路递归展开（NM-CUR-168）

- 登记台：`docs/agents/ledger.md` T-group-002（todo，无认领 Agent；162/164 claim 会话均未关行，本行负责 A 车道 W1#2 收口）
- 前置：T-group-001 done（158 指导收口：013 迁移重跑+旧形状表演练单测，T-group-002 门已开）
- 现状盘点（本会话实测）：
  - `internal/relay/group_tree*.go` 已不在树（164 的清理动作已落地），单路径已收敛为
    `op.FlattenGroupItems` + relay `pickGroupItem(group.WithItems(flat))`；
  - handler.go 102 行已按每轮展平后传入；route.go 232 与 245 存在重复 `itemOf` 历史已核对
    （实际仅一份，早期 grep 误报）；build -tags=jsoniter 当前为绿。
- 本行剩余范围（ ponytail 最小收口）：
  1. 复核 `op/group_flat_test.go` 四单测可用性（order/cycle/depth-cap/empty），修正构造错误；
  2. 补 relay 选路语义测试（冷却跳位/亲和保持/空树零值/手动模式直取）；
  3. 复查 manual 模式 ActiveItemID 指向子分组引用行时的语义（块内首叶子=整块选中，
     按 158 claim 冻结口径在展平层落地，避免第二套运行时语义）；
  4. 全量验证：build -tags=jsoniter + vet + test -count=1。
- 红线：不改 T-group-001 模型/迁移；不改 T-group-003 前端；不碰 B/C 车道 doing 线；密钥不贴原文。
