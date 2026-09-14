# 认领：T-acct-003 Token/Key 健康监控（NA-Token/S2-Token/OpenAI-Key/Anthropic-Key 四类探测；Admin-costs 留权限门不实现）

- 认领 Agent：cursor-local（NM-CUR-139）
- 认领时间：2026-09-13
- 登记台：`docs/agents/ledger.md` T-acct-003（todo，无认领）
- 前置需求：R-acct-003（用户原话五类探测）；lane D 车道 W1#4（129 转交）
- 范围：只做只读探测客户端（httptest 单测先行），复用 internal/health 只读口径
  （30s 超时 / 1MiB 限读 / 失败 ok=false 不阻断转发）；
  不含 UI、不含定时任务、不落库；Admin-costs 类按 ledger 备注留权限门不实现
- 计划：
  1. `internal/health/probe_token.go`：四类探测器 + 结果形状（类别/健康/延迟/错误）
  2. NA-Token：GET /api/token/ 带 Authorization（new-api token 用量可用性，宽容解析）
  3. S2-Token：GET /v1/models 带 bearer（复用既有形状）
  4. OpenAI-Key：GET /v1/models 带 bearer；Anthropic-Key：GET /v1/models 带 x-api-key+anthropic-version
  5. httptest 桩单测全绿；登记台账同步
