# 认领：登记台领取巡检188（只读巡检+待指导任务指导，零业务代码改动）

- 认领 Agent：cursor-local（NM-CUR-188，2026-09-14）
- 登记台：`docs/agents/ledger.md` 31 行——done 23 / review 2（T-sec-001 用户动作；T-group-003 待验收）/ doing 4（T-pool-001/T-quota-001/T-acct-001/T-deploy-001，均在途有主）/ todo 2（T-pool-002/T-proto-001，前置未过门；T-route-002 已 done，T-deploy-002 前置 deploy-001）。
- 冲突避让：不抢 184/186/187 在途行；不碰 171/178 的 web 9 文件 dirty；不碰 183 exec 的 deploy D 阶段；T-route-002 的 claim 文件已存在但内容空壳，ledger 已 done，以 186 报告为准不重写。
- 本轮结论：**无空闲 todo 可领，无可独立交付的未完成问题**。按用户指令先给待指导任务指导，然后停止。
- 指导输出：见 `docs/worklog/2026-09-14-登记台巡检指导188.md`（deploy-001 D2-D9 执行口径 / pool-001 收敛出口 / quota-001+acct-001 R2 门 / proto-001 开工顺序 / sec-001 不代办）。
- 验证：`go build -tags=jsoniter` EXIT 0（本轮实测）；其余链路沿用 184/186/187 实测结论，不重复跑全量。
- 红线：零业务代码改动；备份/密钥不碰；不代提交。
