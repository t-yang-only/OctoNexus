# 认领：T-test-002 本地部署实测（NM-DS-002）

- 日期：2026-09-15
- Agent：dsh-local（DeepSeek Harness）
- 任务：本地部署 + 导入 `数据备份(勿提交).json` + 多格式/超时切换/后台统计核查 + 缺陷修复与复测
- 范围：只碰本地实例、本地测试库与 relay/op 用量明细实现；不碰转发主流程、不改迁移、不提交任何密钥。
- 产出：`internal/op/usage.go`（三处修复）、`internal/op/usage_query_test.go`（5 例）、
  `docs/worklog/2026-09-15-本地部署与后台统计修复.md`、ledger T-test-002 行。
- 结论：多格式 11/11、真实上游 6/6、超时切换 5/5、后台审计 13/13；用量明细三缺陷已修并复测。
- 遗留（未改，需用户拍板）：前端日志正文 id 口径（历史行未接入，潜在不一致）；
  `stats_save_interval` 改值不热生效。
