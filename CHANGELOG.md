# 更新日志 (CHANGELOG.md)

所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

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