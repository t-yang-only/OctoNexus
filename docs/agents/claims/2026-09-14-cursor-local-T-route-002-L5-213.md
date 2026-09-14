# 认领：T-route-002 L5 均衡热路径落地（NM-CUR-213）

- 认领 Agent：cursor-local（NM-CUR-213，2026-09-14）
- 登记台：`docs/agents/ledger.md` T-route-002（done；201 调度 L5 热路径落地开工单，212-G7 重申）
- 调度依据：NM-CUR-201 §1 L5（Ownership：`internal/relay/handler.go`、`internal/relay/route.go`、`internal/relay/balance*.go`）+ §2 合并顺序（**L5 先合，L6 禁同时 commit**）+ 212-G7 开工单。
- 范围（最小接线，feature flag 默认关）：
  1. `model.SettingKeyRouteBalanceEnabled` 新设置键（默认 "false"，`SettingGetBool` 读取）；
  2. handler.go 每轮选成员处：flag 开时先 `rankCandidates`（展平后的 Available 成员 + RouteState 冷却快照）
     定序取首选，flag 关时维持 `pickGroupItem` 原路径**零行为变化**；
  3. round 计数器按顶层分组持有（RouteState 新增内部字段），亲和/冷却/上限语义仍归 pickGroupItem 既有链路，
     balance 只改"候选谁先被尝试"的顺序；
  4. 单测：flag 关（现行为回归）+ flag 开（候选顺序按 rank 序）两侧覆盖。
- 红线：未开 flag 行为零变化；不碰 transformer/（L6 锁）；不碰 web/（L2 锁）；不碰 op/backup*.go（L1 锁）；
  不碰 task/health（L3/L4 锁）；密钥不贴原文。
- 验收：`go build -tags=jsoniter ./...` + `go vet ./internal/relay/ ./internal/model/` +
  `go test ./internal/relay/ -count=1`（新旧单测全绿）→ worklog + claim 关行；不代提交。
