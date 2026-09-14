package health

// T-acct-003 Token/Key 健康监控：四类只读探测（Admin-costs 留权限门，不实现）。
// 设计来源：R-acct-003（用户原话五类）+ T-acct-002 既有读侧客户端口径。
// 本文件只定义探测类别与结果形状；探测器实现见 probe_token_client.go。
// 口径沿用 FetchBalance：30s 超时、1MiB 限读、失败 Healthy=false 不阻断转发热路径。
// 本包不落库、不做定时调度（属后续任务）；Admin-costs 需官方管理权限，
// 按登记台备注留权限门（AdminCostsProbeEnabled=false 时不发起请求）。
const (
	// AdminCostsProbeEnabled 是 Admin-costs 探测权限门：默认关闭，开启前需显式授予管理权限。
	AdminCostsProbeEnabled = false

	// AnthropicVersionHeader 是 Anthropic /v1/models 探测要求的版本头。
	AnthropicVersionHeader = "anthropic-version"
	// AnthropicAPIVersion 是当前探测使用的 API 版本。
	AnthropicAPIVersion = "2023-06-01"
)

// ProbeKind 是探测类别。
type ProbeKind string

const (
	ProbeKindNAToken      ProbeKind = "na_token"      // New API Token：Authorization 头查 token 可用性。
	ProbeKindS2Token      ProbeKind = "s2_token"      // Sub2Api Token：bearer 查 /v1/models。
	ProbeKindOpenAIKey    ProbeKind = "openai_key"    // OpenAI Key：bearer 查 /v1/models。
	ProbeKindAnthropicKey ProbeKind = "anthropic_key" // Anthropic Key：x-api-key 查 /v1/models。
	ProbeKindAdminCosts   ProbeKind = "admin_costs"   // OpenAI Admin costs：需管理权限，权限门默认关闭。
)

// ProbeResult 是一次健康探测的结果。
type ProbeResult struct {
	Kind       ProbeKind `json:"kind"`        // 探测类别。
	BaseURL    string    `json:"base_url"`    // 被探测的站点根地址。
	Healthy    bool      `json:"healthy"`     // 探测是否通过。
	StatusCode int       `json:"status_code"` // 上游 HTTP 状态码（网络失败为 0）。
	LatencyMs  int64     `json:"latency_ms"`  // 探测耗时毫秒。
	Error      string    `json:"error"`       // 失败原因，成功为空。
}

// ValidProbeKind 校验探测类别取值。
func ValidProbeKind(kind ProbeKind) bool {
	switch kind {
	case ProbeKindNAToken, ProbeKindS2Token, ProbeKindOpenAIKey, ProbeKindAnthropicKey, ProbeKindAdminCosts:
		return true
	default:
		return false
	}
}
