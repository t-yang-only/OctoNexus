# 认领：T-fetch-001 模型拉取版本段重复修复（NM-DS-004）

- 日期：2026-09-15
- Agent：dsh-local（DeepSeek Harness）
- 任务：修用户报的模型拉取失败（/v1/v1/models），并用真实渠道把「拉取即自动建 渠道名/模型名 分组」验证一遍
- 范围：internal/model/channel.go（+ 新增 channel_url_test.go）、internal/server/handlers/channel.go、
  internal/relay/channel.go、internal/op/usage_query_test.go（测试隔离）。**密钥不入库**：真实渠道 key 只在仓库外的
  临时脚本里使用，未写入任何被 git 跟踪的文件，也未进文档/提交/记忆。
- 产出：worklog docs/worklog/2026-09-15-模型拉取双前缀修复.md；本地新增渠道 DBG-53HK / DBG-53HK-L / DBG-THQ 及各自动分组
- 结论：单测 5 例、九包 test 全绿、活体 15/15（含 base 带 /v1 的两家）
- 遗留：api.apikey.fun 需可用网络/代理；跳转页文案口径变更待用户拍板
