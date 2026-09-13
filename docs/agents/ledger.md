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
| T-pool-001 | 号池管理选型：ChatGPT/Gemini 账号池开源候选+并入 octopus 方案（供用户选择） | pool | done | cursor-local | 2026-09-13 | 按137线路收口：E 车道 135 会话产出一份候选清单并入 T-pool-002 设计稿；登记台 068–136 号池占位行全部标"已收敛"；后续实体工作在 T-pool-002/T-route-002，不再新增选型行 |
| T-quota-001 | 余额/额度采集与阈值告警：上游余额接口定时采集落库，低于阈值告警 | quota | doing | cursor-local | 2026-09-13 | B 车道/117：W1#1。W0 余额链已过门（解析/指纹/采集/事件在仓）；剩 task 5min 调度+channel→监控凭证映射+QuotaZeroStop 集成测试 1 例；过门才开 W2 |
| T-quota-002 | 余额归零自动停模型：归零停用对应模型授权/成员，手动恢复+审计 | quota | done | cursor-local | 2026-09-13 | NM-CUR-124 实施：凭据级停用幂等+手动恢复+QuotaAction审计+2单测；链路验收随 B 车道 W1 集成测试一并确认 |
| T-group-001 | 分组嵌套数据模型：GroupItem 支持引用子分组，防循环+级联语义+迁移 | group | review | cursor-local | 2026-09-13 | A 车道/134：W0#1。以当前树文件+迁移+非缓存测试实证复核，结论写本表；过门放行 T-group-002/003，不过门打回重建 |
| T-group-002 | 分组嵌套选路递归展开：relay 按树形递归解析，亲和/冷却/上限语义 | group | todo | - | 2026-09-13 | A 车道：W1#2，前置 W0#1 过门。展平/防循环/深度截断/空树四单测+语义冻结口径（亲和/冷却/上限按顶层） |
| T-group-003 | 分组嵌套前端：树形展示/子分组选择器+三语 i18n | group | doing | cursor-local | 2026-09-13 | F 车道/120：W2#1，前置 W1#2 过门；门过前只做三语 i18n 骨架，禁写 child_group_id 字段 |
| T-acct-001 | 官方账号授权接入：OpenAI/Gemini/Claude 官方授权扫码+套餐/健康/5H7D窗口读取 | account | doing | cursor-local | 2026-09-13 | C 车道/128：W1#3。authorize/callback/读取三接口+PKCE state+密文落库+state 过期/一次性 code 单测；合法官方授权，禁验证码绕过 |
| T-acct-002 | 中转站用户登录：NA 用户登录+自查；S2 用户登录+Keys/订阅/quotas读取 | account | done | cursor-local | 2026-09-13 | NM-CUR-129 完成：读侧客户端 health/relay_account*.go（NA cookie 会话+S2 bearer，两形状宽容解析）+httptest 单测；落库/UI/定时属后续任务；报告见 worklog/2026-09-13-T-acct-002中转站登录129.md |
| T-acct-003 | Token/Key 健康监控：NA-Token/S2-Token/Admin-costs/OpenAI-Key/Anthropic-Key 五类探测 | account | todo | - | 2026-09-13 | D 车道/129 转：W1#4。四类 httptest 先行；Admin-costs 留权限门不实现 |
| T-acct-004 | 手动订阅+通用 HTTP 余额：无接口套餐手工录入；自定义 JSON 余额接口适配 | account | todo | - | 2026-09-13 | D 车道：W2#3。schema+校验+超时+1MB 上限+JSON path 错误单测 |
| T-acct-005 | 手动登录跳转页：NA/S2 生成一次性跳转页防验证码拦截 | account | todo | - | 2026-09-13 | C 车道：W2#4，前置 W1#3 过门。一次性 token+过期+重复消费拒绝+审计+管理员权限门 |
| T-pool-002 | 官方账号号池转发：Google/ChatGPT 官方账号当渠道做轮询转发 | pool | todo | - | 2026-09-13 | E 车道：W3#1，前置 W1#3+W2#2+T-proto-001 矩阵过门；OpenAI/Gemini 先行，Claude 后续 |
| T-route-002 | 均衡请求：号池内轮询/加权/最低延迟，老 068/070 选型链收敛到此 | route | todo | - | 2026-09-13 | A 车道：W2#2，前置 W1#1 过门。加权轮询第一版+健康/冷却/归零剔除+最低延迟可插拔接口 |
| T-proto-001 | 连接协议转化：官方账号↔OpenAI/Anthropic/Gemini 协议互转验证 | relay | todo | - | 2026-09-13 | G 车道：W3#2，前置 W1 全过。OpenAI Chat↔Anthropic Messages 五类用例矩阵（非流式/流式/usage/错误/tool call） |
| T-test-001 | isolated backup acceptance: import local backup to separate DB and verify auth/channels/groups/protocols | test | doing | cursor-local | 2026-09-13 | H 车道/136：W1#5。备份副本→隔离库→双端口→验证→删库留报告；不改备份源文件 |
| T-log-002 | 本地日志错误分析：读 7 份日志包定位历史错误并修复 | log | todo | - | 2026-09-13 | I 车道/133：W0#3。只读分析报告（证据行+分类+修复优先级）；修错另立项 |
