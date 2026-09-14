# Claim：T-route-002 均衡请求第一版（NM-CUR-186）

- 认领会话：Cursor（`NM-CUR-186`，2026-09-14 13:05）
- 依据：183 巡检确认 W1 全过门可用；登记台 A 车道 W2#2 登记语义
- 状态：完成，2026-09-14 13:2x

## 交付

- `internal/relay/balance.go`（新文件，纯函数，不碰既有 pickGroupItem/RouteState）：
  `rankCandidates`——①平滑加权轮询（权重=priority 倒序分；current 累加器跨 round 展平，保证轮转覆盖），
  ②剔除语义：`Available=false`（渠道禁用/凭据停用）与归零授权集合剔除、冷却中压队尾保留，
  ③`LatencyProvider` 可插拔（同档延迟升序、无数据沉底、nil=关闭）。
  亲和/冷却/上限仍归顶层 RouteState——本包只给"候选定序"，不另起路由状态。
- `internal/relay/balance_test.go`：三单测（加权定序轮转覆盖/剔除三分支/延迟插件）。

## 修复记录（测试驱动出的真 bug）

- 初版 `smoothWeightedOrder` 每 round 重置累加器且 `picked` 剪枝，`round%3` 恒选权重首——
  轮转覆盖测试复现 `covered map[1:true]`；已改"从 0 逐轮展开到 round 取当选者"经典语义，转绿。

## 验证

- relay 三单测过；`go test ./... -count=1` 八包全 ok；`go vet ./...`、`go build -tags=jsoniter ./...` 待收口时一并取证。
- 边界：未接转发热路径（pickGroupItem 接线属后续小步，避免与他人在途 relay 改动冲突）；未改模型/迁移/前端。
