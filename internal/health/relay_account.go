package health

// T-acct-002 中转站用户登录读侧客户端（New API 用户登录+自查；Sub2Api 用户登录+三读）。
// 设计来源：R-acct-002（用户原话）+ T-research-002 connectors 语义重述（逐行重写，未复制）。
// 本文件只做"登录→会话→读取"的纯函数客户端：
//   - New API：POST /api/user/login（账号密码）→ session cookie；GET /api/user/self 自查 quota/used。
//   - Sub2Api：POST 登录（邮箱密码）→ bearer token；GET API Keys / 订阅 / platform quotas。
// 侧信道口径沿用 FetchBalance：超时 30s、失败 ok=false 不阻断转发热路径、响应体限读 1MiB。
// 凭据只经内存态调用，本包不落库；落库加密沿用 T-acct-001 AccessCipher 模式。
const (
	// RelayLoginNewAPITimeoutSeconds 是 NA/S2 单次请求超时秒数（与 FetchBalance 对齐）。
	RelayLoginNewAPITimeoutSeconds = 30

	// NAUserLoginPath 是 New API 用户登录端点相对路径。
	NAUserLoginPath = "/api/user/login"
	// NAUserSelfPath 是 New API 用户自查端点相对路径。
	NAUserSelfPath = "/api/user/self"

	// S2LoginPath 是 Sub2Api 用户登录端点相对路径（邮箱密码换 token）。
	S2LoginPath = "/api/auth/login"
	// S2APIKeysPath 是 Sub2Api API Keys 列表端点相对路径。
	S2APIKeysPath = "/api/keys"
	// S2SubscriptionsPath 是 Sub2Api 订阅端点相对路径。
	S2SubscriptionsPath = "/api/subscriptions"
	// S2PlatformQuotasPath 是 Sub2Api platform quotas 端点相对路径。
	S2PlatformQuotasPath = "/api/platform/quotas"
)

// RelaySelfSnapshot 是中转站用户自查快照：剩余额度三元组（上游口径，不做币种换算）。
type RelaySelfSnapshot struct {
	Quota     float64 `json:"quota"`     // 总额度。
	Used      float64 `json:"used"`      // 已用。
	Remaining float64 `json:"remaining"` // 剩余。
}

// RelayAPIKeySummary 是中转站用户名下一条 API Key 的读侧摘要。
type RelayAPIKeySummary struct {
	ID     int64  `json:"id"`               // 上游侧 Key 主键。
	Name   string `json:"name"`             // Key 名称。
	Sk     string `json:"sk"`               // Key 明文（sk-...），调用方须按密文口径保管。
	Status string `json:"status"`           // 上游侧状态原文（enabled/disabled 等）。
}

// RelaySubscriptionSummary 是中转站用户一条订阅的读侧摘要。
type RelaySubscriptionSummary struct {
	ID        int64  `json:"id"`         // 订阅主键。
	Plan      string `json:"plan"`       // 套餐名称。
	ExpiresAt string `json:"expires_at"` // 过期时间（上游原文，空表示未知）。
}
