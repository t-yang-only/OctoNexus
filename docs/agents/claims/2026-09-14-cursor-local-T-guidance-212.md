# 认领：T-guidance-212 待指导任务指导（7 路逐项裁决+可执行开工单）

- 认领 Agent：cursor-local（NM-CUR-212，2026-09-14）
- 登记台：`docs/agents/ledger.md`（done 24+ / review 2 / doing 4 / todo 2，全部有主或门锁，无自由 todo）
- 用户指令：给需要请求指导的任务进行指导。
- 范围：**只读指导，不碰他车道 doing 文件，不改业务代码，不擅自改 ledger 状态机**（ledger 追口径由调度 commit 统一做，本 claim 只给裁决+开工单）。
- 历史指导沿用：181（group-003 复核有效/deploy-001 D1-D9/pool 等待跳过）、188（deploy 续跑+pool 二选一+sec-001 时机三问）、208（pool-003 二选一关闭条件/quota 防误停例+acct 条件 UPDATE/group-003 终验口径给 195/deploy-001 归档 commit 指导）。本轮 212 不重复造口径，只做**终态核验+未闭环项开工单**。

## 1. 七路逐项裁决（2026-09-14 约 15:00 实测）

| # | 需指导项 | 现状核验 | 裁决 |
|---|---|---|---|
| G1 | T-pool-001 代写/收回二选一（188-Q2 起，135/154/158/160/174/176 陈旧 started 行悬挂） | `docs/requirements/2026-09-13-号池管理选型.md` **已在树**（NM-CUR-205 落盘 201-L7 收敛草稿：建议 R-pool-003 搁置+四节大纲，182 终裁口径） | **已闭环**。开工单：调度收敛 135/154/153dup/158×2/159/160/165/184/185 七类陈旧 started 行（按 150 惯例批量关行，保留 171/181/195/199/206/207/209 有效在途）；T-pool-001 doing 维持等用户对 R-pool-003 二选一（要全文则按 §2 大纲展开即过门） |
| G2 | T-deploy-001 D2–D9 执行归属（188-Q1 起） | 183 执行完毕 D1–D9 全绿（验收报告在树未提交）；193 复核：rawCreate P0 修复在树 WIP（`internal/op/backup.go`），唯一真缺口已补 `backup_raw_test.go` 三例（现 `intPtrOf` 1 定义、`TestCreateRowsRawRejectsUnknown` 1 例，198 去重后健康）；全量八包绿+vet 绿（193/198 实测） | **归属 183 续跑归档**。开工单（L1）：`git add internal/op/backup.go internal/op/backup_raw_test.go + 183 验收报告 + claim` → 密钥扫描 → commit（信息带 T-deploy-001）→ deploy-001 行维持 doing 等用户拍板转 done；commit 前必 `go test ./internal/op -count=1` |
| G3 | T-sec-001 key 轮换时机（188-Q3 起，阻塞 deploy-002 上线门） | ledger review 维持；备份在仓库外只读引用，无新泄露面 | **不代办，用户动作**。开工单：deploy-002 todo 维持；用户轮换线上渠道 key + 确认旧备份处置后，sec-001 review→done，deploy-002 方可开工 |
| G4 | T-quota-001 接线（163 清单 5 步，B 车道在途无落盘） | task/init.go 无接线（182/201 两轮核验一致） | **开工单（L3，R2 并行）**：①task 注册 5min 周期（仿 TaskStatsSave）；②channel→监控凭证映射；③阈值事件→QuotaZeroStop 消费；④集成测试 2 例（归零→停凭据→审计 / 未知失败→不停用，防误停是第一验收项）；⑤Auto-Model responses 超时疑点复现结论；红线不碰 relay/ |
| G5 | T-acct-001 处理器（163 清单 5 步，C 车道在途无落盘） | 无处理器落盘（182/201 两轮核验一致） | **开工单（L4，R2 并行）**：authorize/callback/读取三接口+路由挂载 → PKCE state（签发/过期/一次消费/replay 拒绝）→ official_account AutoMigrate → 密文落库（响应 JSON 禁明文）→ 3 单测（state 过期/重复 code/密文 round-trip）；合法 OAuth 禁验证码绕过；红线不碰 task/health |
| G6 | T-group-003 终验（review，195 在途） | 171 实施+184 复验（tsc 0 错/eslint 0 错/build/vet 绿）；195 验收 claim 在途 | **开工单（L2，接力 195 不另起 claim）**：终验链 tsc+eslint+build/vet+op/router 单测全绿 → review→done；禁动 go 文件 |
| G7 | T-route-002 热路径（L5）/ T-proto-001 闭环（L6）/ T-pool-002 预研（L8） | route-002 done（186/197/199 三轮证据齐）；proto-001 done（200 矩阵 6 例全绿）；pool-002 预研草稿已在树（204，`docs/requirements/2026-09-14-pool-002预研.md`） | **开工单**：L5 按 201 口径接热路径（flag 默认关+开/关单测，L5 先合）；L6 复查 transformer 终态不对称登记项（只读优先，L6 后合，禁同时 commit）；L8 预研已交付，pool-002 todo 维持不开工实现 |

## 2. 陈旧 started 行收敛清单（给调度的关行名单，150 惯例）

可关（任务实质已收敛，他会话早已停止）：135（pool-001 零交付，草稿已由 205 收敛）、154、153dup（01:40 行）、158×2（02:18/02:19 双开工声明，实质归 158 指导收口行）、159、160（156 调度实质已转为 182/201 口径）、165、184、185、206、207（巡检类 started 空转）。保留有效在途：171（group-003 实施主体）、181（指导主体已完成实为关行遗留）、195（group-003 验收）、199（route-002 复验主体已完成实为关行遗留）、209（L6 复查）、212（本轮）。

## 3. 合并顺序重申（201 口径不变）

L2（纯 web）→ L5（relay handler/route/balance）→ L6（relay transformer/matrix，合前必 relay 单测）→ L3/L4（不同包并行）→ L1（deploy 收尾最后合）→ L7/L8 文档随时。relay 双路禁同时 commit。

## 4. 用户待办（不派给子 agent，原 201 四项维持）

1. T-deploy-001→done 拍板（R1 验收结论）；2. R-pool-003 二选一（要全文/搁置）；3. T-sec-001 key 轮换时机；4. T-deploy-002 开工拍板。
