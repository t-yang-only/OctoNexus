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
| T-pool-001 | 号池管理选型：ChatGPT/Gemini 账号池开源候选+并入 octopus 方案（供用户选择） | pool | todo | cursor-local | 2026-09-13 | NM-CUR-075 认领：承接 068/070 started 行；claim 见 claims/2026-09-13-cursor-local-T-pool-001.md；只出候选清单+方案，不做共享账号池实施 |
| T-quota-001 | 余额/额度采集与阈值告警：上游余额接口定时采集落库，低于阈值告警 | quota | todo | - | 2026-09-13 | NM-CUR-107 拆分登记：承接 R-quota-001；只做采集+告警，不含自动停用（停用见 T-quota-002） |
| T-quota-002 | 余额归零自动停模型：归零停用对应模型授权/成员，手动恢复+审计 | quota | todo | - | 2026-09-13 | NM-CUR-107 拆分登记：承接 R-quota-002（用户原话）；依赖 T-quota-001 的余额数据；停用粒度与恢复口径待认领时定 |
| T-group-001 | 分组嵌套数据模型：GroupItem 支持引用子分组，防循环+级联语义+迁移 | group | todo | - | 2026-09-13 | NM-CUR-107 拆分登记：承接 R-group-001（用户原话）；只做模型层，不碰选路/UI |
| T-group-002 | 分组嵌套选路递归展开：relay 按树形递归解析，亲和/冷却/上限语义 | group | todo | - | 2026-09-13 | NM-CUR-107 拆分登记：承接 R-group-001；依赖 T-group-001；防循环+深度上限待认领时定 |
| T-group-003 | 分组嵌套前端：树形展示/子分组选择器+三语 i18n | group | todo | - | 2026-09-13 | NM-CUR-107 拆分登记：承接 R-group-001；依赖 T-group-001；只做 UI，不改选路 |
