# 认领：T-group-002 分组嵌套选路递归展开（NM-CUR-161）

- 认领 Agent：cursor-local（2026-09-14）
- 登记台：T-group-002（todo，A 车道范围；158 claim 认领但无落盘记录，行仍 todo 无认领 Agent）
- 前置：T-group-001 done（158 指导收口：迁移重跑+旧形状表演练单测补齐，转 done，T-group-002 门开）
- 范围：`op.FlattenGroupItems(group)` DFS 展平（引用行位置 splice；路径回边剪除/
  超 GroupItemMaxDepth 截断/成员按 ID 去重）+ groupSnapshot 子分组成员 Available 回填
  + relay `pickGroupItem` 改收展平列表（纯函数，manual/failover 双分支；亲和/冷却/上限键按顶层语义不变）
  + 四单测（展平顺序/防循环/深度截断/空树）+ relay 纯函数测试（冷却跳位/亲和保持/空树零值）
- 不越线：不改 T-group-003 前端；不改 group-001 模型/迁移；B/C 车道 doing 线不碰
