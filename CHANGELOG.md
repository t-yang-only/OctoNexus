# 更新日志 (CHANGELOG.md)

所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.37] - 2026-10-21
### Added
- **Sub2API 渠道自动物化联动**：支持将反代账号一键注册物化为系统渠道（`sync_channel`），实现与路由、分压与选路的直接闭环。
- **前端快捷选项**：增加物化复选框与连接探针实时状态反馈。

## [v0.79.36] - 2026-10-20
### Added
- **Sub2API 订阅反代网关核心架构接入**：
  - 支持 9 大平台（Claude, OpenAI, Gemini, Antigravity, Grok, Kimi, DeepSeek, OpenCode, Cline）。
  - 支持 4 大协议分流模式（Chat Completions, Responses, Anthropic, Adaptive）。
  - 接入 AES-256-GCM 独立凭据加密机制（AAD: `sub2api:account`）。
  - 自动数据库迁移与全新建表校验测试。
  - 前端卡片化管理面板（平滑替代原官方账号）。
### Deprecated
- 弃用旧版官方账号反代机制。

## [v0.79.35] - 2026-10-20
### Changed
- 生产配置与群组全量同步完成，部署至 `apidate.yangonly.com`。
