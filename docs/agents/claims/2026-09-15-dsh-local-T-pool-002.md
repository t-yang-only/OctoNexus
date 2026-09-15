# claim: T-pool-002 官方账号号池转发（NM-DS-001）

- Agent：dsh-local（DeepSeek Harness，登记编号 NM-DS-001）
- 任务：`docs/agents/ledger.md` T-pool-002（todo → 本次实施）
- 认领时间：2026-09-15 09:24:40
- 门禁核对：前置三门均已过门——W1#3 `T-acct-001` done、W2#2 `T-route-002` done、`T-proto-001` 矩阵 done。
  201-L8 的「预研 only」是并行调度期锁（当时 acct-001 未完成、号池无凭据可填），门开后按用户
  「完成台账待办」指令正式实施。
- 范围：**凭据侧物化**——官方账号 → 号池渠道凭据；不新增模型表、不改选路与协议转换、
  不碰转发热路径与其他车道在途文件。
- 产出：
  - `internal/op/official_pool.go`（物化/失活停用/临期刷新/状态快照）
  - `internal/op/official_pool_test.go`（7 例）
  - `internal/model/official_account.go`（号池 DTO：同步请求/结论/状态快照）
  - `internal/server/handlers/official_account.go`（`POST /pool/sync`、`GET /pool/list`，Admin+Auth）
  - 报告 `docs/worklog/2026-09-15-接手与台账收口.md`
- 验证：go build/vet EXIT 0；新单测 7/7 PASS；`go test ./... -count=1` 九包全绿；
  隔离实例 API 冒烟（list/sync/带 provider/无 cookie 401）全通过，临时实例已停并清理。
- 未做（用户侧）：真实 OAuth provider 凭据与官方端点 Header 需线上配置后灰度（与 T-acct-001 同口径）。
