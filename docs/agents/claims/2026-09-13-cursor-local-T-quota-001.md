# 认领：T-quota-001 余额/额度采集与阈值告警（只读采集，不停用）

- 认领 Agent：cursor-local（NM-CUR-117）
- 认领时间：2026-09-13
- 登记台：`docs/agents/ledger.md` T-quota-001（todo，无认领）
- 未选 T-pool-001（075/076 claim 在仓，owner 在途）、T-quota-002（含自动停用动作，需 T-quota-001 数据先行）、
  T-group-002/003（声明依赖 T-group-001，113 doing 在途）；按"依赖最少、可独立交付"选 T-quota-001。
- 来源：R-quota-001（需求登记.md，待确认，代录）+ T-research-002 移植清单 P1/P2/P4/P5。
- 范围（只做采集+告警，不含自动停用；停用见 T-quota-002）：
  1. `internal/health/balance.go`：new-api 系 `GET {base}/api/user/self` 余额采集（宽容字段匹配；
     监控凭证与转发 key 分离存储；失败只记事件不阻断转发）。
  2. `internal/health/fingerprint.go`：余额 payload SHA256 指纹，仅变化落库/触发事件。
  3. `internal/task` 注册低频采集任务（默认 5min，可配；避免烧上游额度）。
  4. 阈值告警事件（余额低于阈值 → 事件源，消费方为 U-alert-001；本任务只产事件，不做通知通道）。
- 红线：只借鉴字段语义与接口形状，逐文件重写，不复制 api-monitor 文件；
  不动转发热路径；密钥不入库不贴原文；`reference/` 不入库。
- 验收：`go build -tags=jsoniter` + `go vet ./...` + `go test -count=1 ./...` +
  双 check 脚本；新增单测覆盖指纹变化判定与宽容字段解析。
