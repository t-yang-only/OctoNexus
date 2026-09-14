# 认领：任务登记台领取巡检196（只读巡检+指导，不抢车道）

- 认领 Agent：cursor-local（NM-CUR-196）
- 认领时间：2026-09-14
- 登记台：`docs/agents/ledger.md` 31 行——done 22 / review 2（T-sec-001 待用户 key 轮换；T-group-003 已由 184 独立复验转 review 待验收）/ doing 4（T-pool-001 / T-quota-001 / T-acct-001 / T-deploy-001，均有主在途）/ todo 3（T-pool-002 / T-proto-001 / T-deploy-002，全部前置未过门）。
- 冲突避让：不抢 191/192/193/194/195 在途行；不碰 171/178/184 的 web 脏文件；不碰 183 exec 的 deploy D 阶段与 `internal/op/backup.go` 未提交 WIP；不改 `docs/agents/ledger.md` 状态行（避让并行会话）。
- 本轮结论：无空闲 todo 可领，无可独立交付的未完成问题。按用户指令给待指导任务指导后停止。
- 指导输出：见 `docs/worklog/2026-09-14-任务登记台领取巡检196.md`（deploy-001 待用户拍板转 done / group-003 待验收 / pool-001 按 182 终裁走 R-pool-003 / quota-001+acct-001 属 R2 / proto-001 与 deploy-002 门禁重申）。
- 验证：`go build -tags=jsoniter .` EXIT 0（本轮实测）；`go vet -tags=jsoniter ./internal/op/` EXIT 0；其余链路沿用 183/184/186/191 实测结论，不重复跑全量。
- 红线：零业务代码改动；备份只读不贴原文；密钥不碰；不代提交。
