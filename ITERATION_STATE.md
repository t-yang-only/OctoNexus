# 持续迭代进度看板 (ITERATION_STATE.md)

## 1. 基础状态
- **当前轮次**：Round 3 / 256
- **目标截止时间**：2026-10-22 12:00 (Asia/Shanghai)
- **执行主体**：DSH (唯一实施/编码/测试/运维总控)
- **规划与审查顾问**：Codex 6.1 Sol High (通过 de_coi 派发与独立审查)
- **当前部署版本**：v0.79.40 @ `apidate.yangonly.com`

---

## 2. 轮次进度与质量台账

| 轮次 | 核心任务 | 交付物/提交 | Codex 审查评分 | 状态 | 关键缺陷/整改要点 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Round 1** | 弃用旧官方账号反代，接入 Sub2API 订阅反代网关架构 | `b2d4a0c`, `6b837e4` (v0.79.36/37) | **48 / 100** (建议退回整改) | 已审查 | 发现 8 项 HIGH（模型/授权缺口、enabled零值覆盖、代理未生效、探测误报、备份遗漏、明文头等） |
| **Round 2** | 根据 Codex 审查意见加固 Sub2API 闭环 (F1-F10) | `457687e` (v0.79.38) | **65 / 100** (+17 显著改善) | 已审查 | 修复 8 项 HIGH；模型授权物化打通、显式 false 保留、敏感头从 Extra 剥离加密、备份恢复闭环、数据库 017 索引自愈迁移 |
| **Round 3** | 渠道物化深度联动、更新一致性与敏感头全链路隔离 | `v0.79.40` | **68 / 100** (已复审) | 交付就绪 | 彻底解决 R3-F1~R3-F8 全部缺陷：增量保护渠道人工配置、CustomHeader 密文存储与 Relay 动态解密注入、全链路一致性与解绑阻断、数据库 018 历史脱敏迁移、前端编辑与多缓存联动 |
| **Round 4** | 动态模型发现与渠道分组自动同步 | `c437216` + `ca1d5c6` (v0.79.41) | 自审闭环（Codex 交叉验证因宿主重启中断，明确记录为未完成） | 已部署 | 新增 `POST /api/v1/sub2api/accounts/:id/sync-models`：上游 `/v1/models` 拉取 → 去重 → 增量合并进物化渠道模型/授权（协议位随 APIProtocol 自适应）；12 项单测（上游指纹/去重/幂等/三条守卫）全绿；HK4H8G apidate 热部署验证（commit c4372162，前端资产 hash 一致） |
| **Round 5** | Round 4 功能独立审查闭环（自审 + 判据 + 变异） | 本轮提交 (v0.79.42) | 自审（Codex 派发反复被宿主重启中断，改以自审+变异检查闭环并如实记录） | 已部署 | 修复 R5-F1（sync 读-改-写窗口无渠道级互斥，并发覆盖手动模型）/ R5-F2（grant 硬编码 default，凭据被改名后报不可执行错误）/ R5-F3（无凭据账号静默发无认证请求）；3 项隔离用例 + 3 项变异全部命中，还原后 op 包全绿 |
| **Round 6** | 同步结果可观测化（新增/下架报告） | 本轮提交 (v0.79.43) | 待 Codex 审查 | 已部署 | sync 返回值与前端 toast 新增 added/stale 报告；`last_sync_models` 基准字段（AutoMigrate）使下架判据只认「上次在上游」——首轮实现曾把物化预置模型误报为下架，由 R6 用例当场抓出后改为基准制；Stale 只报告不自动删除 |

---

## 3. Codex Round 3 详细审查结论与整改对照 (全量闭环)
- **R3-F1 (增量同步保护)**：`Sub2APIAccountUpdate` 改为增量同步，仅更新受管基础字段，完整保留运维在渠道上手动追加的自定义端点路径、配额倍率、专属模型与授权，杜绝无辜抹平。
- **R3-F2 (敏感头在 Relay 与 Probe 间自洽注入)**：`internal/relay/channel.go` 增加 `secret.IsSealed` 解密支持，渠道表以 `channel:header` 密文落库敏感头；Relay 转发与 Probe 探测时解密还原，保证外部依赖 Cookie/Token 的网关通过验证，同时 DB 无明文泄露。
- **R3-F3 (写操作原子一致性)**：调整写库顺序，账号校验并成功持久化后才更新渠道，防止账号保存失败留下孤儿已生效渠道。
- **R3-F4 (解绑删除失败严格拦截)**：解绑删除关联渠道时捕获 `ChannelDel` 错误，失败立即阻断并报错，禁止静默清零。
- **R3-F5 (坏密文保护)**：未显式提供新凭据时绝不重刷密文；原有凭据解密失败显式报错，杜绝被清空。
- **R3-F6 (历史敏感头迁移)**：新增 `internal/db/migrate/018.go`，在启动时自动扫描并对存量历史明文敏感 Header 执行 AES-256 密文加密升级。
- **R3-F7 (禁用物化渠道 Enabled 修复)**：创建渠道后若账号为禁用态，显式调用 `ChannelEnabled(id, false)`，彻底消除 GORM default 标签反转问题。
- **R3-F8 (前端编辑与缓存联动)**：卡片增加「编辑」入口支持即时修改别名、地址、Key、启停与备注；mutation 成功后同时触发渠道和分组的 Query 缓存失效。

### Codex 通道受阻记录（Round 5/6 审查）
- 本机 codex CLI（0.164.0）可启动，但执行中反复卡在远端通道重连（chatgpt.com 系服务不可达；`-c 'mcp_servers={}'` 禁用 MCP 后仍 Reconnecting 1/5→2/5 停滞，三次派发合计等待逾 60 分钟无产出）。非账号额度问题；本会话无 Cockpit Tools，无授权备用账号。按目标第四条采用恢复方式：以自审+隔离用例+变异检查闭环（Round 5/6 均已如此交付），Codex 独立评分留待网络条件允许后补派，台账不虚构评分。

### Round 4 审查结论与 Round 5 整改对照（全量闭环）
- **R4 结论**：功能主路径正确（上游指纹 Bearer 送达、路径归一化、去重、幂等、协议位映射、前端三缓存失效均实证无缺陷）；JSON 畸形处理完备（类型不匹配/空列表/缺 data 均报错）。
- **R5-F1（并发窗口，MEDIUM）**：`Sub2APIAccountSyncModels` 的 ChannelDetailGet→合并→ChannelUpdate 无渠道级互斥，而 ChannelUpdate 是整体替换语义（channel.go 注释实证）——并发 sync 或 sync 与面板编辑混跑时后写者的旧快照会抹掉手动模型。修法：按渠道 ID 分锁（进程内 `sync.Map`，单二进制部署即覆盖全部并发源），读-改-写全程持锁。
- **R5-F2（凭据名硬编码，LOW）**：grant 构造硬编码 `KeyName: "default"`，与物化命名（sub2api.go:325/537）一致故正常路径无缺陷；但运维在面板删掉/改名 default 后，ChannelUpdate 以 `channel key "default" does not belong to channel`（channel.go:721-723）拒绝，错误不可执行。修法：取渠道现有凭据名（排序确定），无凭据报「请先在面板为该渠道添加渠道 Key」。
- **R5-F3（无凭据静默，LOW）**：Credentials 为空时静默发出无认证请求，上游 401 只回 `上游模型端点返回错误 HTTP 401`。修法：进入即报「请先编辑账号补全 API Key」。
- **Round 4/5 的 Codex 交叉验证状态**：两次派发均因宿主在执行中重启而中断（输出文件随 /tmp 清失，遗留孤儿进程已清理）。按纪律如实记录为未完成，本轮以「自审（逐行代码实证）+ 隔离用例 + 三项变异全部命中」闭环；Codex 独立评分待宿主稳定后补派，不虚构。

---

## 4. 自动化测试与门禁证据
- `internal/op/sub2api_test.go`：12 项严密单元测试 100% 全部通过（动态模型同步上游指纹/去重/幂等/三条守卫路径）。
- `internal/op/sub2api_r5_test.go`（新增）：3 项隔离用例全绿——20 轮×(8 路 sync+8 路手动追加) 混跑手动模型存活；凭据改名后授权跟随；无凭据可执行报错。
- 变异检查（每个守卫一个隔离用例，变异全部命中）：F1 去锁→第 0 轮红；F2 回硬编码→红（错误原文即预期）；F3 守卫失效→红（退化为内部 JSON 错误）。
- 全仓 Go 单元测试 `go test ./internal/op/`：通过（op 包 1.157s，0 FAIL；本轮仅改 op 包，按改动范围取最小充分集）；`go vet ./internal/op/` 无输出；改动文件 gofmt 干净。
- 前端无改动（Round 5 纯后端加固），Round 4 的 `pnpm tsc --noEmit`/`pnpm lint` 0 警告证据继续有效。
- 线上测试环境热部署：HK4H8G `apidate.yangonly.com` 运行 `v0.79.41`（commit c4372162），服务正常活跃，前端资产 hash 与本地构建一致；官方备份导入已生效（channels 21/keys 60 密文/groups 349）。

---

## 5. 汇报状态
- 当前周期累计已完成轮次：8 / 10
- Round 7（部署闭环补全）：v0.79.43 上线后真实 Key 打 relay，High-flash 分组 → deepseek-flash 上游 200（0.98s，正常 completion），满足 T-deploy-003「真实 Key 发一条 200」；另实证 apidate 的 Sub2API 账号数为 0（官方备份 version 5 不含该表），sync 系列功能（Round 4/6）在此实例仅有单测覆盖，在线验证待用户在面板创建账号后自然发生
- Round 8（最小间隔保护，fa36c4e / v0.79.44 已部署）：账号级 10s 同步间隔 + 剩余等待提示，误连打不再反复打上游；R4/R5/R6 连续同步判据拨开间隔（各守卫互不掩盖，R5-F1 去锁变异复验命中）；R8 变异命中；全仓 21 包 0 FAIL；上线后真实转发 200（0.78s）、DB 无损（21/60/sealed60）
- 下次 Server 酱汇报阈值：Round 10
- Round 5 已部署上线（v0.79.42，HK4H8G apidate，验证：Version/Commit、匿名 401、资产 hash 一致、DB 无损 channels=21/keys=60/sealed=60/groups=349）。
Round 6 已部署上线（v0.79.43 @ 7adf6fa，含前端改动）：线上资产 index-DKJ-G_RL.js 与本地构建一致、匿名 401、Version/Commit 正确、DB 无损（21/60/sealed60/349）。