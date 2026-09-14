# 认领：T-proto-001 L6 矩阵缺口复查（NM-CUR-209，只读优先+观察项闭环）

- 认领 Agent：cursor-local（NM-CUR-209，2026-09-14）
- 登记台：`docs/agents/ledger.md` T-proto-001（done，NM-CUR-200 矩阵 6 例在树；201-L6 指派本轮做缺口复查+观察项闭环）。
- 调度依据：NM-CUR-201 §1 L6（`internal/relay/transformer/**` + `proto_matrix_test.go` 锁文件；**禁碰 handler.go/route.go/balance.go（L5 锁）**）、§2 合并顺序（L5 先、L6 后）。
- 范围（只读优先）：
  1. 复查 200 报告登记的观察项：`validateResponse` 只覆盖 Responses 终态；Chat/Anthropic 非流式 200-失败终态依赖 `TransformResponse` 侧抛错（`upstream.go:92` 包装证据）。
  2. 实证 axonhub 源码（`llm@v0.0.0-20260909170523-3786f2c5c8de`）：openai `TransformResponse`（400+ 抛错、空体抛错、其余 `ToLLMResponse` 常返 nil err，含 200-error 体）；anthropic 同式（400+/空体/JSON 错抛错，其余常返 nil err）。
  3. 缺口确认才补 httptest：200-error 体（Chat `{"error":...}` 200 / Anthropic `{"type":"error",...}` 200）在 `TransformResponse` 常返 nil err 且 `validateResponse` 仅 Responses 生效 → relay 侧第二道门缺失，补 2 例回归锁口径。
- 红线：只补测试文件 `internal/relay/proto_matrix_test.go`（L6 锁文件），不改 `protocol.go`/`upstream.go`/transformer（L6 只读优先口径）；不碰 L5 三文件；密钥不碰；备份只读。
- 验收：新增 2 例 PASS + `go test -tags=jsoniter ./internal/relay/ -count=1` 全绿 + `go vet` 相关包绿。
