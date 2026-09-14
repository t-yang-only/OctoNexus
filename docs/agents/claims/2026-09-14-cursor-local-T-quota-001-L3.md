# Claim：T-quota-001 接线执行 L3（NM-CUR-217）

- 认领会话：Cursor（`NM-CUR-217`，2026-09-14 13:53）
- 调度依据：212-G4 开工单（163 清单 5 步），merge 顺序 L3∈{L3/L4 并行}，红线不碰 relay/
- 前置撞车处置：入场时 task/init.go 见 14:29 他车半截改动（仅 const 行），按 201 锁归属回收重做，未并行写
- 状态：完成（review 待调度验收），2026-09-14 15:5x

## 交付（163 清单逐项）

| 步骤 | 落点 | 说明 |
|---|---|---|
| ① task 5min 注册 | `internal/task/init.go` + `TaskQuotaScan` | `op.QuotaScanInterval()` 可配（`quota_scan_interval` 设置键，0=停用，缺省 5min 对齐 P5）；仿 TaskStatsSave 模式 |
| ② 凭证映射 | `internal/op/quota_scan.go`（新） | `QuotaScanTargets()`：启用+有 BaseURL 渠道 → {ID,名,BaseURL,MonitorToken,UseProxy}；**监控凭证过渡口径=ID 最小启用凭据**（P2 分离存储留字段化 TODO，单点 firstEnabledKeyToken）；阈值/周期设置键含 Validate（model/setting.go 三键追加+两条默认值） |
| ③ 消费链 | `internal/task/quota_scan.go`（新） | 采集→SHA256 指纹去重（进程内表）→ BelowThreshold 告警（现落日志，U-alert-001 换出口）→ `remaining<=0` 恒走 `op.QuotaZeroStop`（**不依赖告警阈值配置**，R-quota-002 主诉求）；**防误停：采集失败/不可解析/指纹未变一律跳过** |
| ④ 集成测试 2 例 | `internal/task/quota_scan_test.go` | 归零→停凭据→auto_stop 审计（actor=system:auto）+ 不变余额不重触发；404/垃圾体/不可达三失败形态→凭据不动零审计；映射出局口径 1 例。**实库真链路**：db.SetDBForTest+op.InitCache+httptest 桩，跑生产函数非仿制 |
| ⑤ 超时疑点结论 | 本节 | 见下 |

## ⑤ Auto-Model responses 超时疑点复现结论（136 遗留，147 裁决并 B 线）

- 现象复述（136 实录）：隔离实例 `/v1/responses` 打 `53HK/Auto-Model` 组 60–90s 无响应 HTTP:000；
  同上游换 `53HK-L/MiniMax-M2.7` 组秒通；该组 req_fail 统计吻合。
- 代码路径复核（只读 relay，红线未改一行）：`handler.go` Forward 对"无可选成员/授权取不到/凭据停用"
  一律 `request.wait(ctx, MemberRetryIntervalSeconds)` 无限重试至客户端断开（L91/106/117/128/233 五处 wait 分支）。
  Auto-Model 组成员 protocols 掩码不含 responses 时，该协议下无可选成员 → 网关按设计等待人工修配置，
  对外表现即挂死到客户端超时。
- 判定：**非网关缺陷，136 定性成立**——上游组合不接该协议 + 网关无上限等待语义叠加的用户可见症状。
- 加固建议（归 relay 车道，本线不动）：转发入口按 `requestProtocol` 预过滤成员掩码，
  整组无一支持时快速 4xx 而非无限等待；或轮次上限。提请调度登记 U-route 后续项。

## 架构决策记录

- `op→health→rhttp→op` 成环（build 实证）：映射留 op（读缓存），消费循环落 task（同时 import op+health 无环），
  采集原语不动 health——三层各自可测；首版 op 内写消费循环的草稿因成环废弃重写。

## 验证

- `go test ./internal/task/ -count=3` 绿（3 例）；model/op/health -count=1 绿；`go vet ./...` EXIT 0。
- 全量 -count=1 中 relay 包 `TestPickGroupItemBalancedFlagOffMatchesLegacy` FAIL：
  **L5 车道在途**（route_balance_test.go mtime 15:50、route.go 15:45，本车入场前不存在），非本车范围，未触碰；
  按 L5 merge 顺序（L2→L5→…）其自修或调度核。
