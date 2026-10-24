# 更新日志 (CHANGELOG.md)

所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.55] - 2026-10-24
### Security
- **API Key 新建来源白名单字段持久化修复 (Round 18)**：
  - **R18-F1 (HIGH) APIKeyCreate 补齐 allowed_cidrs 落库字段**：此前 \APIKeyCreate\ 为防止 GORM 默认值标签跳过 \Enabled=false\ 显式指定了落库列名列表，但遗漏了 \llowed_cidrs\。导致新建带有 IP/CIDR 白名单的 API Key 时，白名单配置在创建写入阶段被静默丢弃，IP 防护线失效。现已补齐落库白名单，确保新建 API Key 的来源 IP 访问控制真实落库生效。

## [v0.79.54] - 2026-10-24
### Security
- **渠道模型拉取与探测专属出口节点接入 (Round 17)**：
  - **R17-F1 (HIGH) fetchModel 优先路由至渠道绑定的专属出口节点**：此前渠道配置了专属出口节点时，模型拉取与探测仅判断 \	arget.Proxy / target.ChannelProxy\，导致拉取与探测可能绕过节点直连上游泄露真实 IP，或节点未就绪时未严格 fail-closed。现严格与 elay/upstream\ 保持一致：\ProxyNodeID > 0\ 时通过 \op.ProxyNodeEndpoint\ 解析节点本地入站，不可用时严格返回 502 Bad Gateway 阻断泄露。

## [v0.79.53] - 2026-10-24
### Security
- **敏感设置项回显脱敏加固 (Round 16)**：
  - **R16-F1 (HIGH) SettingList 剔除 pool_declarative_adapters 密文回显**：在 \SettingList\ 查询中过滤排除 \pool_declarative_adapters\ 项。该项存放全部声明式号池适配器的完整 JSON 密文（内含自定义鉴权与账密 API Key 等凭据块）；此前在全局设置列表查询接口中原样返回整段密文，虽经加密但违反最小暴露原则，现统一完全收口至 \poolstore.List\ 并在传输层严格经过 \poolstore.Redact\ 彻底清除 Secret 凭据，设置管理面列表绝不明文/全密文回显。

## [v0.79.52] - 2026-10-24
### Security
- **备份导入导出凭据加固与基准往返 (Round 15)**：
  - **R15-F1 (HIGH) 异机凭据解密警告**：在 \importSecretWarnings\ 中补齐对 \Sub2APIAccounts\ 的解密校验。跨实例恢复或密钥轮换导致密文无法解开时，如实生成警告提示重新录入 API Key，杜绝静默黑盒报错。
  - **R15-F2 (HIGH) 明文凭据导入自动加密**：在 \DBImportIncremental\ 导入 \Sub2APIAccounts\ 时，通过 \sealSub2APICredentialsForStore\ 对未加密明文凭据自动执行 AES-GCM 加密落库，保证静态加密契约在导入路径生效。
  - **R15-F3 (MEDIUM) 下架基准跨机往返**：在 \Sub2APIAccountExport\ 增加 \LastSyncModelsOut\，使 \LastSyncModels\（上次成功同步的模型基准集合）随备份持久化与恢复，避免换机后丢失下架判定基准。

## [v0.79.51] - 2026-10-24
### Security
- **auth cookie 安全加固 (Round 14, R14-F1/F3)**：auth cookie 新增 \HttpOnly=true\（防止 XSS 读取 JWT，此前为 false）；\Secure\ 属性按链路判定（直连 TLS 或 \X-Forwarded-Proto: https\），反代 HTTPS 链路下自动生效，本地 http 开发环境不被破坏。
- **服务端登出与路由解绑 (Round 14, R14-F2)**：HttpOnly cookie 前端 JS 无法清除，新增 \POST /api/v1/user/logout\ 服务端清除端点，前端登出改调该端点。v0.79.50 部署后在线验证抓出登出挂在 \RequireJSON()\ 组导致无 body 请求报 415，在 v0.79.51 中将登出移入独立路由组（无 JSON/无 Auth 要求）并补全路由级无 body 登出用例。双变异（去 HttpOnly 变异、登出挂回 JSON 组变异）均命中。

## [v0.79.49] - 2026-10-24
### Security
- **登录防爆破 (Round 13, R13-F1, HIGH)**：管理面暴露公网而登录接口此前无限次试密。按用户名失败计数，5 次失败锁定 5 分钟，锁定期内即使口令正确也拒绝（429 + 剩余秒数），到期清零、登录成功清零。锁定阈值取自单测覆盖，在线验证打了 3 次错误密码（低于阈值）确认登录路径正常。
- **密码强度下限 (Round 13, R13-F2, MEDIUM)**：改密码此前只拒空串，短口令可落库。op 层加 8 位下限与「新旧不得相同」（第二道入口，CLI/RPC 同样收口）。双变异全命中。

## [v0.79.48] - 2026-10-24
### Fixed
- **停用账号三入口守卫 (Round 12, R12-F1)**：probe/sync/clean-stale 此前均不检查账号 enabled，停用账号（上游欠费/故障/主动下线）照样会打上游。三路径入口一致拒绝并提示「请先在面板启用」；前端三个按钮对停用账号隐藏。三个变异（各自去守卫）全部命中。

## [v0.79.47] - 2026-10-24
### Added
- **下架模型清理闭环 (Round 11)**：新增 \POST /api/v1/sub2api/accounts/:id/clean-stale\，把上次同步发现的下架模型从物化渠道移除。安全边界：只删「上次同步基准里已不在上游」且删前重新核对渠道现状的模型，名单外（含人工手填）绝不动；授权同步收敛；摘要归零。空名单/已清理/名单与渠道无交集三种场景明确拒绝。账号卡片在 stale_count>0 时显示「清理下架模型(N)」按钮（二次确认 + 清单展示）。

## [v0.79.45] - 2026-10-24
### Added
- **同步摘要持久可见 (Round 9)**：账号表新增 \last_sync_summary\（AutoMigrate 加列），sync 成功时写入 {added_count, stale_count, stale[]}；账号卡片常驻展示「上次同步：新增 X，上游已下架 Y」并列出下架模型名（引导到渠道页人工确认后清理）——此前该信息只在 toast 闪现，刷新即丢。
### Fixed
- **R5-F1 用例重建**：原断言「并发期间人工写入的模型必存活」落在渠道锁不覆盖的交错窗口上（锁保证 sync 之间互斥，不覆盖面板保存与 sync 读-写交叠），偶发失败。按纪律将该场景分类为设计边界并写入注释；判据改为锁真正保证的性质：并发 sync 无库竞争错误、并发前写入的人工模型必存活。5 次复跑稳定，去锁变异仍红。

## [v0.79.44] - 2026-10-24
### Added
- **同步最小间隔保护 (Round 8, R8-F1)**：同一账号 10 秒内的重复 sync 直接拒绝并提示剩余等待秒数，误连打（双击/多标签页）不再把同样的请求反复打到上游；`last_sync_at` 由 AutoMigrate 加列。R4/R5/R6 的连续同步判据相应拨开间隔，各守卫用例互不掩盖（R5-F1 去锁变异复验仍命中）。

## [v0.79.43] - 2026-10-24
### Added
- **同步结果可观测化 (Round 6)**：`POST /api/v1/sub2api/accounts/:id/sync-models` 返回新增 `added`/`added_count`（本次新并入的模型）与 `stale`/`stale_count`（上游已下架的模型）。前端 toast 从「成功同步 N 个」升级为「新增 X 个，当前共 N 个；上游已下架 Y 个（未自动删除，请到渠道页人工确认）」。
- **下架判据基准 (R6-F1)**：`sub2api_accounts.last_sync_models` 记录上次成功同步时上游返回的集合（AutoMigrate 加列）。Stale 只报「上次在上游、这次不在」的模型——物化预置与手填模型从未进入该集合，不会被误报成下架；首次同步无基准不报。Stale 仅罗列，绝不自动删除（R3-F1 人工配置保护）。
### Tests
- `internal/op/sub2api_r6_test.go`：首次 Added=2/Stale=0；上游扩容 Added 仅含新模型；上游缩容 Stale 报出下架且模型仍在渠道（只报告不删除）。变异（用渠道存量当下架基准）→ 首次同步即误报 5 个预置模型，用例红。

## [v0.79.42] - 2026-10-24
### Fixed
- **并发读-改-写窗口互斥 (R5-F1)**：`Sub2APIAccountSyncModels` 的「读渠道 → 合并模型/授权 → 写渠道」全程按渠道 ID 分锁（进程内 `sync.Map` 互斥）。此前并发 sync 或 sync 与面板编辑同时进行时，后写者的旧快照会经 ChannelUpdate 的整体替换语义静默抹掉先写者的手动模型。
- **授权凭据名跟随渠道现状 (R5-F2)**：新增授权不再硬编码 `default`，改为取渠道现有凭据名（`ChannelDetailGet` 按名称排序，取值确定）；渠道无任何凭据时报「请先在面板为该渠道添加渠道 Key」的可执行错误，替代原先一句看不出该补什么的 `channel key "default" does not belong to channel`。
- **无凭据账号可执行报错 (R5-F3)**：凭据为空的账号同步前直接报「请先编辑账号补全 API Key」，不再发出无认证请求让上游 401 或内部 JSON 解析错误替我们报错。
### Tests
- `internal/op/sub2api_r5_test.go` 新增 3 项隔离用例：20 轮 × (8 路 sync + 8 路手动追加) 混跑断言手动模型存活（F1）；物化凭据改名为 custom-key 后新增模型授权必须挂到新名（F2）；无凭据账号必须报可执行错误（F3）。
- 变异检查：F1 去掉锁 → 并发用例第 0 轮即红（manual-gamma 丢失）；F2 改回硬编码 default → 改名用例红（错误原文即设计预期）；F3 守卫失效 → 无凭据用例红（退化为「unexpected end of JSON input」）。三项变异全部命中，还原后 `go test ./internal/op/` 全绿。

## [v0.79.41] - 2026-10-23
### Added
- **动态模型发现与自动同步 (Round 4)**：新增 `POST /api/v1/sub2api/accounts/:id/sync-models`，从上游 `/v1/models` 拉取真实可用模型，去重后增量合并进物化渠道的模型与授权（协议位按账号 APIProtocol 自适应：chat_completions/responses/anthropic/adaptive），并走 ChannelUpdate 刷新分组授权；前端账号卡片新增「同步模型」入口（channel_id>0 时可见）。
### Tests
- `internal/op/sub2api_test.go` 增至 12 项：新增上游指纹（Bearer 送达与 `/v1/models` 路径归一化）、去重、幂等二次同步、未物化/上游 500/账号不存在三条守卫路径。

## [v0.79.40] - 2026-10-21
### Added
- **Relay 动态解密 CustomHeader**：`internal/relay/channel.go` 支持密文 Header 实时解密注入，确保依赖 Cookie/Token 的上游网关在真实转发与探测中均自洽生效。
- **数据库迁移 018**：自动扫描历史渠道 CustomHeader 并对明文敏感头进行 AES-256-GCM 加密清洗。
- **前端编辑弹窗与关联缓存刷新**：账号卡片增加编辑模式，修改后自动联动刷新渠道与分组缓存。
### Fixed
- **增量同步保护 (R3-F1)**：账号更新不再全量冲掉渠道的人工模型与计费配置。
- **解绑阻断 (R3-F4)**：解绑删除渠道失败不再静默返回成功。
- **禁用物化渠道修复 (R3-F7)**：彻底修复创建禁用账号时物化渠道反转为启用的问题。

## [v0.79.39] - 2026-10-21
### Added
- Sub2API 账号更新与渠道物化实时联动。

## [v0.79.38] - 2026-10-21
### Fixed
- 解决 Codex 独立审查发现的 F1-F8 缺陷。