# 认领：T-group-001 迁移演练单测 + audit-003/acct-004 指导收口（NM-CUR-158）

- 认领 Agent：cursor-local
- 认领时间：2026-09-14
- 背景：用户本轮授权"需要指导的任务按你的想法指导并完成"。盘点三件：
  1. T-audit-003：runtime 复验已在 NM-CUR-152 补做（quota -count=3 ok；-race 为
     本机 Go 发行包缺 race syso 的环境限制），台账滞留 review → 指导收口转 done；
  2. T-group-001：转 done 唯一前置"补迁移重跑+旧形状表演练单测"——013 迁移在树、
     两红例已由 153/156 修绿，但演练单测无人补 → 本会话认领补上（migrate/013_test.go），
     完成即解锁 T-group-002 与 T-deploy-001 的门；
  3. T-acct-004：适配核心 149 审核无 bug，review 滞留 → 指导验收转 done；
     其"W2 开工前存储位置拍板"给出建议口径（独立 ManualSubscription 表，
     理由：手动订阅非上游凭据、与渠道 key 生命周期不同），W2 开工时终裁。
- 范围：仅迁移测试 + 台账/登记同步；不碰 156 在途的 group.go/group_nesting_test.go；
  不实施 T-group-002（解锁后另行认领，避免与 A 车道撞线）
