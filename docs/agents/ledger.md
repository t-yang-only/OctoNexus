# 任务登记台账（ledger）

> 认领前先看本表；认领时同步新建 `claims/YYYY-MM-DD-<agent>-<task>.md` 加锁。

| 任务 ID | 标题 | 模块 | 状态 | 认领 Agent | 登记日期 | 备注 |
|---------|------|------|------|-----------|---------|------|
| T-docs-001 | Python 原型去向决策（移子目录/另建仓/删除） | docs | todo | - | 2026-09-12 | `pyproject.toml+src/+tests/` 与上游 Go 工程共存混乱 |
| T-sec-001 | `数据备份.json` 移出工作区并轮换密钥 | security | todo | - | 2026-09-12 | 含线上渠道明文 key，绝不提交 |
| T-env-001 | 安装 Go 1.26 + Node18 + pnpm 并跑通构建 | env | todo | - | 2026-09-12 | 本机此前无 go，需验证 `web build + go run main.go start` |
| T-agents-001 | 建立多 agent 协同登记台账 | agents | review | cursor-local | 2026-09-12 | 待验收，见 worklog 2026-09-12-agents-ledger |
