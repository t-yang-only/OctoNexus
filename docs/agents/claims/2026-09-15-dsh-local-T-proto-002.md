# 认领：T-proto-002 协议无损转化测试（NM-DS-005）

- 日期：2026-09-15
- Agent：dsh-local（DeepSeek Harness）
- 任务：按用户要求做协议无损转化测试，并把实测到的有损点修掉后复测
- 范围：internal/relay/upstream.go（补采样参数）、internal/relay/upstream_params_test.go（新增）。
  测试装置在仓库外：%TEMP%\octopus-dstest\run_lossless_tests.py + mock_upstream.py（本轮补记请求体）。
  本地新增测试渠道 DS-TEST-proto3（同一 mock-good 配三把凭据，授权协议位 2/4/8）与三个分组
  DS-TEST-proto-chat / -resp / -msg。
- 结论：矩阵 13/13 全绿；「转成 Responses 上游时 temperature 丢失」已修（5 例单测 + 活体复验）
- 遗留：各协议语义不等价字段（thinking / logprobs / tool_choice 形态）与多模态未覆盖
