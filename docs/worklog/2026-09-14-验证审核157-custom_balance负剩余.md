# 2026-09-14 验证审核 157：跳转页审计复核 + custom_balance 负剩余 bug 修复 + race 环境复测

- 台账编号：`NM-CUR-157`｜路由：`auto-skills` 仅命中基座 `using-superpowers`
- 用户指令：去登记台领任务并完成，随时登记汇报；有问题登记等待指导后跳过未完成转其他支线；
  无未完成问题就验证之前的修改找 bug，没有 bug 标记审核过。

## 1. 登记台扫描结论（领任务）

开工实测 `go build -tags=jsoniter`/`go vet`/`go test ./internal/...`：
- 健康基线：除 A 车道 group 嵌套 2 例（`TestSyncGroupItemsDepthCap`/
  `TestValidateGroupTreeRefsOnCreate`）外全绿；该 2 例在我验证窗口内被 A 车道并行会话
  自行收敛转绿（`internal/op` 复跑 `ok`，未触碰其文件）。
- 所有 ledger `todo` 均车道前置未过门（group-002/003、acct-001 在途、pool/route/proto/deploy
  依赖未满足），无可领空闲 todo。`T-audit-004`（跳转页审计）在我读台账期间已被
  `NM-CUR-153` 认领并 done。→ 按规则转**验证支线**。

## 2. 跳转页（T-acct-005）审计复核：维持「审核过」

对 `internal/model/jump_token.go`、`internal/op/jump_token.go`、
`internal/server/handlers/jump.go`、`internal/server/middleware/logger.go` 独立复核
`NM-CUR-153`/`152` 的五口径，逐条与代码一致，无新增 bug：

| 口径 | 复核证据 | 判定 |
|---|---|---|
| 一次性消费竞态 | `Consume` 条件 UPDATE `token_hash=? AND consumed_at IS NULL AND expires_at>now`，rowsAffected 判成功；`TestConsumeJumpTokenConcurrent` 8 路并发恰好 1 成功 | PASS |
| 120s TTL 边界 | `expires_at > now` 严格大于；`TestConsumeJumpTokenExpired` 强制过期→`ErrJumpTokenExpired` | PASS |
| 哈希不落明文 | 列 `TokenHash json:"-"`；明文仅 create 响应一次；超 128 字符输入走空串哈希统一 NotFound（防哈希放大）；访问日志 `/go/` 后整段 `[REDACTED]`（logger_test 2 组）；list JSON 不含哈希/明文（jump_token_audit_test） | PASS |
| 权限门 | create/list 挂 `middleware.Auth()` 且仅 `ServeOn(ServerAdmin)`；`go/:token` 令牌即凭证仅挂 Admin 端口不上 Relay；未知统一 404 | PASS |
| 审计完整性 | actor/created_at/expires_at/consumed_at + note 五列落行；`ListJumpTokens` 限幅 50 倒序分页 | PASS |

**结论：跳转页实现无 bug，维持 `T-acct-005`/`T-audit-004` done。**

## 3. 验证支线发现 1 处真 bug 并修复：custom_balance used-only/超额负剩余

`internal/health/custom_balance.go` `FetchCustomBalance`（T-acct-004 手动 HTTP 余额适配，
状态 review，尚未接入调度）缺 `ParseBalancePayload` 已确立的「used-only 不可推剩余」口径：

```go
// 修复前：仅配 UsedPath 时 Quota/Remaining 路径皆空 → 默认推导
if cfg.RemainingPath == "" {
    snap.Remaining = snap.Quota - snap.Used   // 0 - used = 负数
}
```

复现（临时测试，已删）：`{"used":40}` + 仅 `UsedPath:"used"` →
`err=nil, Remaining=-40, Quota=0`。**负剩余一旦接入 T-quota-002 归零停用链路即误停用**，
与 147 在 `ParseBalancePayload` 修的「只剩已用单字段」bug 同类。

修复（同口径）：

```go
if cfg.RemainingPath == "" && cfg.QuotaPath == "" {
    // 只剩"已用"单字段时无从推知总额/剩余（推导会得到负剩余，
    // 下游归零停用会误判），与 ParseBalancePayload 同口径视为采集失败。
    return BalanceSnapshot{}, errors.New("cannot infer remaining from used-only paths")
}
...
if snap.Remaining < 0 {
    // 上游已用>总额（超额脏数据）时归零而非透传负数，避免阈值/归零停用语义漂移。
    snap.Remaining = 0
}
```

附带修第二边界：`quota<used` 超额时原逻辑同样产出负 `Remaining`，统一夹 0。

回归测试（`custom_balance_audit_test.go` 新增 2 例）：
- `TestCustomBalanceRejectsUsedOnly`：仅 used 路径必须报错，不得返回负剩余。
- `TestCustomBalanceClampsOverspendRemaining`：`{"quota":10,"used":40}` → 剩余夹 0 非负。

## 4. T-audit-003 runtime `-race` 复测：登记为环境阻塞（非代码缺陷）

150 调度解除了 A 车道编译阻断后，本轮实测 `-race`：

```
==...==ERROR: ThreadSanitizer failed to allocate 0x0000059d0000 (94175232) bytes
        at 0x100ec8a730000 (error code: 87)
```

- health、model 两包均稳定复现（重试 2 次一致），非「缺 race syso」（152 线的猜测不成立，
  gcc 15.2 在位、`-race` 能编译链接）；实为 Windows 下 ThreadSanitizer 保留影子内存区
  失败（error 87 = ERROR_INVALID_PARAMETER），属本机环境内存布局限制。
- **代码可达的 runtime 复验部分已完成**：quota+jump+health+handlers 决定性 `-count=3`
  全绿（含 `TestConsumeJumpTokenConcurrent` 真并发 8 路恰好 1 成功，条件 UPDATE 由 DB 串行化
  保证，不依赖 race 检测器）。`-race` 数据竞争扫描这一项仍受环境阻塞，登记等待环境
  （如换机/调高内存提交限制）或指导，不阻断读侧 + 决定性并发结论。
- 维持 `T-audit-003` review、`T-quota-002` done（读侧 150 已盖章，本轮无新增 bug）。

## 5. 本轮变更清单

- 代码修复：`internal/health/custom_balance.go`（used-only 拒绝 + 超额夹 0）。
- 回归测试：`internal/health/custom_balance_audit_test.go`（+2 例）。
- 文档：本 worklog、`docs/agents/claims/...T-audit-005`、ledger 加 T-audit-005 行 +
  T-audit-003 备注更新、worklog README 索引、agent_word 登记表/日志/文件清单/技能建议。
- 验证窗口实测：`go build -tags=jsoniter`/`go vet ./...` 全绿；
  `go test -tags=jsoniter -count=1 ./internal/...` 6 个含测试包全 `ok`，0 FAIL 0 panic。

## 6. 遗留 / 提请

- 跳转页管理 UI 按钮（T-acct-005 后续）仍未做。
- custom_balance 接入调度 + 手动订阅落库/UI（T-acct-004 W2）须用户先拍板存储位置
  （渠道凭据 vs 独立表）后开工。
- A 车道 T-group-001 review 转 done 仍需「迁移重跑 + 旧形状表演练」2 单测（152 §3 门 1），
  归 A 车道交付，本车未触碰。
- `-race` 数据竞争扫描需可保留 TSan 影子内存的环境补跑（登记等待）。
