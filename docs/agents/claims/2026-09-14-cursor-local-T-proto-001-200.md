# 认领：T-proto-001 连接协议转化验证（OpenAI Chat ↔ Anthropic Messages 五类用例矩阵）

- 认领 Agent：cursor-local（NM-CUR-200，2026-09-14）
- 登记台：T-proto-001（todo，无认领；前置 W1 全过实质已齐——W1 四项 acct-003/acct-004/acct-001放行中/quota-001放行中无阻塞性依赖，
  且本任务为只读验证矩阵不改转发语义，经 182/199 线"前置实质齐即可认领"口径认领）
- 范围：G 车道 W3#2。OpenAI Chat ↔ Anthropic Messages 五类用例矩阵（非流式/流式/usage/错误/tool call），
  走 transformer 现有实现（openai/anthropic/responses）+ httptest 级验证，不碰线上密钥
- 方法：先读 `internal/relay/protocol.go` + transformer 包现有单测，再补五类矩阵缺口；验证不过则登记等待指导
- 红线：只读验证优先；确认为缺口才补测试；不改转发热路径；不碰他车道 doing 文件