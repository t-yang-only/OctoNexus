# 认领：T-deploy-001 R1 部署收口测试 D1–D9 执行（NM-CUR-183）

- 认领 Agent：cursor-local（NM-CUR-183，2026-09-14）
- 登记台：T-deploy-001（doing，182 已开工 dispatch claim；无执行人实际跑 D 阶段）
- 182 残门作废已裁决（仓库无号池选型需求，R-pool-003 待用户确认），本 claim 只做**执行交付**
- 范围：按 `docs/worklog/2026-09-13-收口部署测试规划152.md §4` 跑 D1–D9，产出《R1 发布候选验收报告》
- 红线：备份只读不贴原文；隔离库+隔离端口；测后整删；密钥绝不提交；不碰他车道 doing 文件
- 隔离基线：`../octopus-本地数据/数据备份(勿提交).json`，rows_affected 对齐 067/136
  （channels 12/keys 36/models 259/grants 264/groups 340/items 615/llm_infos 132）