# Claim：T-acct-005 手动登录一次性跳转页（NM-CUR-149）

- 认领会话：Cursor（`NM-CUR-149`，与22:30审计会话撞号，本车行消歧为 `NM-CUR-149-jt`，2026-09-13）
- 任务：NA/S2 生成一次性跳转页防验证码拦截：一次性 token + 过期 + 重复消费拒绝 + 审计 + 管理员权限门
- 车道：C 车道 W2#4（登记台口径）

## 交付

- `internal/model/jump_token.go`：`JumpToken` 行模型（只存 SHA-256 哈希，明文不落库；
  actor/created/expires/consumed 四列即审计）；kind 枚举 na/s2；`ValidateJumpTargetURL`
  拒绝非 http(s)、无主机、内嵌凭据。
- `internal/op/jump_token.go`：`NewJumpToken`（crypto/rand 32B，TTL 120s）；
  `ConsumeJumpToken` 条件 UPDATE（`consumed_at IS NULL AND expires_at > now`）保证
  并发下至多一次成功，重复/过期/不存在分别返回哨兵错误；`ListJumpTokens` 限幅分页审计。
- `internal/server/handlers/jump.go`：Admin 端口三接口
  （`POST /api/v1/account/jump/create` 与 `GET /list` 挂 Auth 权限门；
  `GET /go/:token` 无 cookie 依赖、令牌即能力凭证，html/template 自动转义渲染
  meta-refresh 跳转页，`Cache-Control: no-store`；未知/已用/过期统一 404/410 不回显探测信息）。
- `internal/db/testdb.go`：`SetDBForTest` 测试注入助手（仅测试使用）。
- 单测：op 层 7 组（创建哈希/坏输入/一次性/过期/未知/并发 8 路至多 1 成功/分页限幅）；
  handlers 层 4 组（创建+消费页+重放 410/坏目标 400/未知 404/URL 转义）。

## 边界说明

- 管理 UI 按钮/页面（生成并打开跳转链接）属前端后续，本任务交付后端闭环。
- 不做验证码绕过：跳转页只是把管理员浏览器带到上游登录页，登录人工完成。

## 验证

- `go test ./internal/op/ -run Jump` ok；`go test ./internal/server/handlers/` ok；
  `go build ./...`、`go vet ./...` EXIT 0。
- 注：全量 `go test ./...` 当前被 A 车道未提交的 group_items 单列唯一索引回归
  阻断（`TestEnsureAutoGroupsLocked_*` 两例），与本任务无关，已另行登记待指导。
