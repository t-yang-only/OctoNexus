# 认领：T-route-002 L5 均衡热路径落地收口（NM-CUR-230）

- 认领 Agent：cursor-local（NM-CUR-230，2026-09-14）
- 登记台：`docs/agents/ledger.md` T-route-002（213 实施后轮转 done/doing 抖动，本轮做收口核验+台账定稿）
- 前序：claim 2026-09-14-cursor-local-T-route-002-L5-213（213：flag + 接线 + 单测 + 011/013 迁移指针编译兼容修复）
- 本轮范围（收口，不重写）：
  1. 实测 L5 三件套在树：`model.SettingKeyRouteBalanceEnabled`（默认 false fail-closed）、
     `RouteState.balanceRound`（顶层轮转计数）、`pickGroupItemHot`（handler 统一入口，flag 关零行为变化）、
     `pickGroupItemBalanced`（rankCandidates 定序 + 原 pickGroupItem 复用）；
  2. 补 op 侧测试支撑缺口：`SettingSetStringForTest`（仅缓存替换，不碰 DB）——213 测试引用了该入口但 op 未落盘；
  3. 台账定稿：T-route-002 doing→done（213 实测证据 + 本轮复跑证据），备注写明 flag 默认关红线；
  4. 不碰 L1 backup*/L2 web/L3 task-health/L4 handlers-official/L6 transformer 的锁文件。
- 验收：`go vet`（relay/model/op）+ `go test ./internal/relay/ -count=1` + `go test ./... -count=1` 全包 +
  `go build -tags=jsoniter ./...` + ReadLints 无错 → worklog + README 索引 + ledger 行 done；不代提交。
