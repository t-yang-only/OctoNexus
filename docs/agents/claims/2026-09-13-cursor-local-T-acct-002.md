# 认领：T-acct-002 中转站用户登录（New API 用户登录+自查；Sub2Api 用户登录+Keys/订阅/quotas 读取）

- 认领 Agent：cursor-local（NM-CUR-129）
- 认领时间：2026-09-13
- 登记台：`docs/agents/ledger.md` T-acct-002（todo，无认领）
- 前置需求：R-acct-002（用户原话：NA 用户账号密码登录 /api/user/login + /api/user/self；
  S2 用户邮箱密码登录，读 API Keys/订阅/platform quotas）
- 范围：只做读侧客户端（登录→会话→自查/Keys/订阅/quotas 采集形状），
  复用 internal/health 的只读采集口径；不含 UI、不含定时任务、不含停用；
  凭据只入内存态调用，落库加密口径沿用 T-acct-001 的 AccessCipher 模式（本任务不落库）
- 计划：
  1. `internal/health/relay_account.go`：NA 登录客户端（POST /api/user/login → session cookie；
     GET /api/user/self 自查 quota/used）
  2. S2 登录客户端（POST 登录 → token；GET keys/订阅/platform quotas 三读侧形状）
  3. 两者均为纯函数 + http.Client 注入，单测用 httptest 桩上游
  4. 失败只记 ok=false，不阻断转发热路径（沿用 FetchBalance 口径）
