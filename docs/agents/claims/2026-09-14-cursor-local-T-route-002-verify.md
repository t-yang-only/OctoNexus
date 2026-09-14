# 认领：T-route-002 均衡候选器复验（NM-CUR-199）

- 认领 Agent：cursor-local（NM-CUR-199，2026-09-14）
- 登记台：`docs/agents/ledger.md` T-route-002（done，NM-CUR-186 实施）
- 类型：只读复验 + 缺口单测补齐，不改已冻结语义，不接热路径，不改模型/迁移/前端
- 前置：T-group-002 done（Flatten/WithItems 口径）；balance.go 明确"只给候选定序，不另起路由状态"
- 复验范围：
  1. 权重轮转覆盖性：三成员 3/2/1 权重下 6 轮当选是否覆盖全部（现有测试只断 6 轮全覆盖，不断 6 轮序列分布与 round%total 周期取模口径）
  2. 冷却压尾 vs 剔除：冷却成员保留在队尾（到期回），与 Available=false/归零剔除的区分是否可测
  3. 同 priority 档延迟排序稳定性：SliceStable 在跨档时是否保持权重序（现有 latency 测试三成员同 priority=1，未覆盖跨档混合）
  4. 空候选/全剔除 ErrNoEligibleMember 口径
- 缺口单测计划（`internal/relay/balance_verify_test.go`，新文件不碰 186 产出）：
  - TestRankCandidatesRoundRobinSequence：权重 3/2/1 下 round 0..5 当选序列 == 经典平滑加权序列（1,2,1,3,1,2），锁定取模重放语义
  - TestRankCandidatesCooldownReturns：冷却到期（nowMs > deadline）成员回到 eligible 区而非队尾
  - TestRankCandidatesLatencyKeepsWeightOrderAcrossTiers：跨 priority 档时延迟排序不跨档（权重序优先）
- 红线：不接 pickGroupItem 热路径；不动 RouteState；密钥不贴原文
