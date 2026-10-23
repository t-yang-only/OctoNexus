# 更新日志 (CHANGELOG.md)

所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

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