# 认领：T-research-001 参考仓库对标研究（第一轮）

- Agent: `cursor-local`（coordinator）
- 认领日期：2026-09-13
- NM 编号：NM-CUR-017
- 任务：侦察 `reference/api-monitor`、`reference/litellm`，与 octopus 对标，输出差距与可借鉴清单，并把需求登记进 `docs/agents/需求登记.md`
- 状态：done

## 计划

1. [x] 侦察 api-monitor：README + 架构（监控控制台，规则引擎+通知渠道+加密）
2. [x] 侦察 litellm：ARCHITECTURE + router_strategy + proxy/hooks（100+ 供应商网关，路由/限流/预算/guardrail）
3. [x] 与 octopus 现状逐条对标，凝练差距 G1–G6
4. [x] 产出报告 `docs/research/2026-09-13-参考仓库对标研究.md`
5. [x] 需求登记 `docs/agents/需求登记.md`：R-alert-001 / R-sec-001 / R-quota-001 / R-route-001 / R-limit-001 / R-probe-001
6. [x] 拆出后续研究单元 T-research-002/003/004 供多 agent 认领
7. [x] 更新 ledger + worklog + 索引

## 产出

- 报告：`docs/research/2026-09-13-参考仓库对标研究.md`
- 需求：`docs/agents/需求登记.md`
- 台账：`docs/agents/ledger.md` 新增 4 条 research 任务
- 实施建议顺序：R-alert-001 → R-sec-001 → R-quota-001 → R-route-001 → R-limit-001

## 遗留 / 下一步

- 需求均为 `待确认`，等用户拍板转 `已确认` 后立功能任务。
- T-research-002/003/004 待认领（其他 agent 可并行）。
