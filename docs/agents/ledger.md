# 任务登记台账（ledger）

> 认领前先看本表；认领时同步新建 `claims/YYYY-MM-DD-<agent>-<task>.md` 加锁。

| 任务 ID | 标题 | 模块 | 状态 | 认领 Agent | 登记日期 | 备注 |
|---------|------|------|------|-----------|---------|------|
| T-docs-001 | Python 原型隔离（已完成，待验收归档） | docs | done | cursor-local | 2026-09-12 | NM-CUR-021：src/+tests/+pyproject.toml 移入 reference/python-prototype/（gitignore 不入库），原地 .pytest_cache/egg-info 已清理；遗留：docs/python-prototype-README.backup.md 与本任务关系待确认 |
| T-sec-001 | `数据备份.json` 移出工作区（密钥轮换待用户执行） | security | review | cursor-local | 2026-09-12 | NM-CUR-021：文件已移至仓库外 `../octopus-本地数据/`，验证从未入 git 历史/索引；gitignore 早已覆盖 `数据备份*.json`；待用户：轮换线上渠道 key 并确认旧备份处置 |
| T-env-001 | 安装 Go 1.26 + Node18 + pnpm 并跑通构建 | env | done | cursor-local | 2026-09-12 | NM-CUR-021 验证：本机 Go 1.27（C:/Program Files/Go）+ Node v25.2.1/pnpm 12.3.4；`go build -tags=jsoniter` 通过、`go vet ./...` 通过、`go test ./...` 全过(op/router/handlers)；前端 `pnpm install --prefer-offline`（npmmirror）成功 + `pnpm run build` 通过（tsc+vite，产物 static/out），含嵌入二进制构建验证 |
| T-agents-001 | 建立多 agent 协同登记台账 | agents | done | cursor-local | 2026-09-12 | NM-CUR-023 验收通过：README/registry/ledger/worklog 四件套齐备且后续 8 个任务真实流转，worklog 2026-09-13-全面收尾与审核报告 §5 |
| T-research-001 | 参考仓库对标研究第一轮（octopus vs api-monitor vs litellm） | research | done | cursor-local | 2026-09-13 | 报告 docs/research/2026-09-13-参考仓库对标研究.md；差距 G1-G6 已转登 需求登记.md |
| T-research-002 | api-monitor connectors 精读：余额/公告接口与指纹 diff 移植清单 | research | done | cursor-local | 2026-09-13 | 报告 docs/research/2026-09-13-T-research-002-api-monitor移植清单.md（P1–P8）；claim 见 claims/2026-09-13-cursor-local-T-research-002.md |
| T-research-003 | litellm router_strategy/cooldown 精读：pickGroupItem 扩展设计稿 | research | done | cursor-local | 2026-09-13 | 报告 docs/research/2026-09-13-T-research-003-路由策略设计稿.md；claim 见 claims/2026-09-13-cursor-local-T-research-003.md |
| T-research-004 | 告警通知设计稿：事件源盘点+渠道抽象+规则 schema | research | done | cursor-local | 2026-09-13 | 报告 docs/research/2026-09-13-T-research-004-告警通知设计稿.md；claim 见 claims/2026-09-13-cursor-local-T-research-004.md |
