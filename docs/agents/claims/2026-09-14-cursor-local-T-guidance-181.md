# 认领：T-guidance-181 待指导任务指导（只读验收+等待跳过，不抢车道）

- 认领 Agent：cursor-local（NM-CUR-181）
- 认领时间：2026-09-14
- 登记台：`docs/agents/ledger.md` 31 行——done 22 / review 2（T-sec-001 待用户 key 轮换；T-group-003 已由 NM-CUR-187 独立复验转 review）/ doing 4（T-pool-001 / T-quota-001 / T-acct-001 / T-deploy-001，均为他车道在途或已开工）/ todo 4（T-pool-002 / T-route-002 已由 NM-CUR-186 转 done 待 ledger 落行 / T-proto-001 / T-deploy-002，全部前置未过门或待落账）。
- 用户指令：有问题则登记等待指导后跳过转支线；无未完成问题则停止；有需指导任务则按我方想法指导后完成本任务。
- 范围：只读验收 + 指导，不认领他车道 doing，不碰门禁 todo，不改业务代码，不改 `docs/agents/ledger.md`（避让 184/187 并行会话）。
- 分支1 T-group-003（已转 review，无需重复验收）：本轮只读复核 171 实施完整在途——`web/src/api/group.ts` 双引用、`utils.ts` g:/c: 键、`Editor.tsx` ChildPickerSection、`Card/ItemList` 徽标、三语 `nestedHint/addChild/childBadge/childHint/childGroupFallback/noChildCandidate` 在位；`tsc --noEmit` 0 错、本轮 6 文件 eslint 0 错、`go build -tags=jsoniter` 过、`go vet ./...` 过、`go test ./internal/relay/ ./internal/op/ ./internal/model/` 绿。后端零改动。结论：187 转 review 有效，本会话不重复转状态。
- 分支2 T-pool-001（等待指导后跳过，标记未完成）：`docs/requirements/2026-09-13-号池管理选型.md` 仍不在树（`Test-Path=False`）；NM-CUR-182 终裁残门作废（需求台无仓库选型需求，R-pool-002 为官方账号转发；报告需求改立 R-pool-003 待用户确认）；本分支按指令登记等待指导后跳过，不代写，避免与 174/176 代写行双写同一报告。
- 指导对象：T-deploy-001（182 已开工 doing，183 已完成 D1 构建双绿）：按 152 §4 D1–D9 执行；D2 隔离导入对齐 136 基线（12/36/259/264/340/615/132）；红线备份只读不贴原文、隔离库+隔离端口、测后整删、密钥不提交。
- 验收：`docs/worklog/2026-09-14-待指导任务指导181.md` 落盘；登记表 181 翻已完成；停止。
