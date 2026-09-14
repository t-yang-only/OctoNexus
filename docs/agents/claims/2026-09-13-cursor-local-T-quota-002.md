# 认领：T-quota-002 余额归零自动停模型（停用授权/成员，手动恢复+审计）

- 认领 Agent：cursor-local（NM-CUR-124）
- 认领时间：2026-09-13
- 登记台：`docs/agents/ledger.md` T-quota-002（todo，无认领）
- 用户指派：本轮用户明确选择 T-quota-002（T-log-002/T-test-001 均备注"分配待用户指派"，不抢）。
- 前置：T-quota-001 doing 在途（117 产物 `internal/health/` 四文件在仓：采集→指纹→阈值事件）；
  本任务消费其 `BalanceSnapshot/ThresholdEvent` 语义，不重复造采集。
- 来源：R-quota-002（需求登记.md，用户原话）+ ledger T-quota-002 登记语义。
- 范围（只做停用+恢复+审计，不做采集/通知）：
  1. 停用粒度：归零（`Remaining <= 0`）停用**渠道凭据**（`ChannelKey.Enabled=false`），
     连带效果经现有 `ChannelGrantGet`（凭据停用即不可转发）与 `ChannelGrantCandidates`/
     `GroupList` 可用性口径自然生效；不停渠道整行（同渠道多凭据互不牵连），不删授权行。
  2. 触发源：`health.ThresholdEvent`（阈值 0 即归零）→ `op.QuotaZeroStop` 幂等执行；
     重复事件、已停用凭据再次触发均为 no-op。
  3. 手动恢复：`op.QuotaManualRestore(channelKeyID)` 重新启用凭据；恢复本身记审计，
     不自动恢复（避免归零抖动反复开关）。
  4. 审计：`model.QuotaAction` 落库（停用/恢复：渠道/凭据/触发剩余/操作人/时间），查询接口按渠道倒序分页。
  5. 缓存一致：停用/恢复后 `reloadChannelChildren` 刷新该渠道三类缓存（复用 074/088 已验证路径）。
- 红线：不停用逻辑不进转发热路径（只在事件消费处调用）；不改采集包；不删数据只置位；
  密钥不入库不贴原文；`reference/` 不入库。
- 验收：`go build -tags=jsoniter` + `go vet ./...` + `go test -count=1 ./...` +
  双 check 脚本；新增单测覆盖停用幂等/恢复/审计落库。
