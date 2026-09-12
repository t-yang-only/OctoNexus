# 认领：T-research-002 api-monitor connectors 精读

- Agent: `cursor-local`（coordinator）
- 认领日期：2026-09-13
- NM 编号：NM-CUR-021
- 任务：精读 `reference/api-monitor/internal/connectors`（余额/公告接口与指纹 diff），输出 octopus 移植清单
- 状态：done

## 计划

1. [x] 读 `connectors.go` 接口三件套 + 12 种实现注册
2. [x] 读 `newapi.go` 用户余额/余额/Key 枚举调用
3. [x] 读 `watch_sources.go` 通告/目录 Watch + SHA256 指纹
4. [x] 读 `scanner.go` 调度 + `notify.go` 渠道 + `crypto/secrets.go` AES-GCM
5. [x] 产出 `docs/research/2026-09-13-T-research-002-api-monitor移植清单.md`（P1–P8）
6. [x] 更新 ledger 状态 done
