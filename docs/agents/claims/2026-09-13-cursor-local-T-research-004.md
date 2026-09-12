# 认领：T-research-004 告警通知设计稿

- Agent: `cursor-local`（coordinator）
- 认领日期：2026-09-13
- NM 编号：NM-CUR-021
- 任务：事件源盘点 + 通知渠道抽象 + 规则 schema 设计稿
- 状态：done

## 计划

1. [x] 盘点事件源 E1–E6（group SSE / route SSE / request 终态 / 健康探测 / 目录变化 / 价格同步）
2. [x] `internal/notify/` 抽象：ChannelType(webhook/smtp) + Alert + Notifier(含 DryRun 真实测试)
3. [x] `NotifyRule/NotifyEvent` schema（scope/condition/threshold/window + 收敛去重）
4. [x] 产出 `docs/research/2026-09-13-T-research-004-告警通知设计稿.md`
5. [x] 更新 ledger 状态 done
