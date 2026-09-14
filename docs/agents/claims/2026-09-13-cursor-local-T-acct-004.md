# Claim：T-acct-004 手动订阅 + 通用 HTTP 余额（NM-CUR-145）

- 认领会话：Cursor（`NM-CUR-145`，2026-09-13）
- 任务：无接口套餐手工录入 schema + 自定义 JSON 余额接口适配
- 车道：D 车道 W2#3（登记台口径）

## 范围与红线

- 只做读侧适配：给定端点 + JSON path 配置，取回 quota/used/remaining 三元组。
- 不做通知、不做自动停用、不接转发热路径（归属 T-quota-001/T-quota-002 既有口径）。
- 凭据以 bearer 单字段传输，与转发 key 分离（沿用 FetchBalance/ProbeToken 口径）。

## 本轮交付

- `internal/health/custom_balance.go`：`CustomBalanceConfig`（Endpoint/Token/Timeout/三 path）+
  `Validate`（端点必填、至少一个 path、点分 path 合法性）+ `FetchCustomBalance`
  （30s 默认超时、1MiB 限读、非 2xx 报错、宽容数字/字符串解析、缺 remaining/quota 时互相推导）。
- `internal/health/custom_balance_test.go`：正常/data 嵌套/推导分支/坏 path 拒绝/超大响应体拒绝/超时拒绝。

## 边界说明（诚实口径）

- 手动订阅"录入/落库/UI"未做——本轮只交付通用 HTTP 余额适配核心与单测；
  落库 schema 归属后续（需用户确认存储位置：channel 扩展字段 vs 独立表）。
- Admin-costs 类管理接口不涉及（权限门既定关闭）。

## 验证

- `go test ./internal/health/` ok（含既有 balance/probe/relay_account 用例回归）。
- `go build -o NUL .` EXIT 0；`go vet ./...` EXIT 0。
