# Claim：T-audit-004 T-acct-005 跳转页审计（NM-CUR-153）

- 认领会话：Cursor（`NM-CUR-153`，2026-09-13 23:55）
- 审计对象：`internal/model/jump_token.go` + `internal/op/jump_token.go` + `internal/server/handlers/jump.go`（含 logger 掩码）
- 性质：只读审计；唯一授权改动为测试隔离修复（见发现 F-1，属被审对象自身测试文件，非其他车道）

## 五口径结论：全部通过

| 口径 | 证据 | 判定 |
|---|---|---|
| 一次性消费竞态 | `Consume` 走条件 UPDATE（`token_hash=? AND consumed_at IS NULL AND expires_at>now`），SQLite 写串行化/PG 行锁下 rows=1 至多一次；并发测试 8 路 toct 1 成功（`TestConsumeJumpTokenConcurrent`） | PASS |
| 120s TTL 边界 | `expires_at > now` 严格比较，到期即拒；`TestConsumeJumpTokenExpired` 强制过期→哨兵错误；创建/消费时钟在 `NewJumpToken`/`ConsumeJumpToken` 各取一次 now，无时钟漂移放大窗口 | PASS |
| 哈希不落明文 | 列 `TokenHash string json:"-"` 不序列化；明文仅 `create` 响应一次；`TestNewJumpTokenCreatesHashedRow` 断言 hash≠plain 且 64hex；`DBDump` 备份不含 jump_tokens，且即便导出也只有哈希；日志面由 `logger.go redactLogPath` 掩码 `/go/` 后整段（`logger_test.go` 4 组用例覆盖） | PASS |
| 权限门 | create/list 挂 `middleware.Auth()` 且仅 `ServeOn(ServerAdmin)`；`go/:token` 有意无 cookie——令牌即能力凭证，只挂 Admin 端口，不挂 Relay；未知令牌统一 404 不回显存在性 | PASS |
| 审计完整性 | 四列 actor/created_at/expires_at/consumed_at + note 消费置位；`ListJumpTokens` 限幅 50 倒序分页可查询；`TestListJumpTokensBounds` 覆盖 | PASS |

## 发现

- **F-1（已修，唯一改动）**：`jump_token_test.go` 的内存库 DSN 缺 `-count=N` 计数器隔离
  （NM-CUR-042 既有惯例），`-count=3` 时 `TestListJumpTokensBounds` 撞历史行（page=2/9）。
  已按 `quota_test.go`/`auto_group_test.go` 同款 atomic-seq 后缀修复，修复后 op+handlers
  Jump 两组 `-count=3` 全绿。
- **F-2（接受风险，记录在案）**：一次性明文出现在 URL 路径段（`/go/{token}`），截获面由
  掩码日志 + no-store + 120s TTL + 一次性消费四重压缩；如后续接 CDN/反代需保持不缓存该路径。
- **F-3（范围外观察）**：A 车道 `TestSyncGroupItemsDepthCap` FAIL 与
  `TestValidateGroupTreeRefsOnCreate` PANIC 在 `70b8e09`（150 修复提交）后仍红，
  但 ledger T-group-001 已标 review——review 与红测试矛盾，提请调度/用户核（本车未触碰其文件）。

## 裁决

**过门。** T-audit-004 done；其唯一前置解除 → T-acct-005 review→done（管理 UI 按钮仍属后续）。

---
执行补充（NM-CUR-154）：
- T-audit-004 由 152 认领但无落盘记录，登记台行仍 todo；本轮（154）接手执行。
- 五项审计：见 worklog/2026-09-13-T-audit-004跳转页审计154.md。
- 另补做 T-audit-003 的 runtime 复验（-race 全包），阻断已由 150 调度解除。
