# 认领：登记台领取巡检218（只读复核+指导沿用，不抢车道）

- 认领 Agent：cursor-local（NM-CUR-218）
- 认领时间：2026-09-14
- 登记台：`docs/agents/ledger.md` 31 行——done 23 / review 2（T-sec-001、T-group-003）/ doing 4（T-pool-001、T-quota-001、T-acct-001、T-deploy-001）/ todo 2（T-pool-002、T-deploy-002），全部有主或门锁，无自由 todo。
- 冲突避让：不抢 209（L6 proto 复查在途）/ 212（指导）/ 213/214/215/216/217（L3 quota 接线在途）等并行行；不改 `docs/agents/ledger.md` 状态行；不碰 L1-L6/L8 文件锁。
- 本轮复核（实测）：
  1. `internal/relay/proto_matrix_test.go` 8 例在树，含 209 补的 2 例 200-error 体锁口（`TestProtoMatrixChatHTTP200ErrorBodySurfacesInParsedError` / `TestProtoMatrixAnthropicHTTP200ErrorBodyPassesTransform`）与 `TestProtoMatrixValidateResponseTerminal`；`go test -tags=jsoniter ./internal/relay/ -count=1` 全绿（relay 0.141s），`go vet -tags=jsoniter ./internal/relay/` EXIT 0。
  2. 201-L7 号池收敛草稿 `docs/requirements/2026-09-13-号池管理选型.md` 已在树（205 落盘：R-pool-003 搁置建议+四节大纲）；201-L8 `2026-09-14-pool-002预研.md` 同窗口落盘；182 终裁口径维持（T-pool-001 doing 收敛出口与部署脱钩）。
  3. `internal/task/init.go` + `task.go` 仍无余额接线（quota/balance 关键词未命中）——217（L3）在途未交付，与 212 指导口径一致。
- 指导沿用：212 已给出七路裁决与可执行开工单（L1 归档 deploy / L2 group-003 终验 / L3 quota 接线 / L4 acct-001 / L5 route 热路径 / L6 proto 闭环 / L8 pool-002 预研），本轮不重复造口径。
- 用户待办（不派）：T-deploy-001→done 拍板；R-pool-003 二选一；T-sec-001 key 轮换时机；T-deploy-002 开工拍板。
- 结论：无空闲 todo，无可独立交付的未完成问题，按指令指导后停止。
- 验证：`go test -tags=jsoniter ./internal/relay/ -count=1` 绿 + `go vet -tags=jsoniter ./internal/relay/` 绿（本轮实测）。
- 红线：零业务代码改动；备份只读不贴原文；密钥不碰；不代提交。