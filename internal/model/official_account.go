package model

import (
	"fmt"
	"time"
)

// OfficialAccountProvider 是官方账号的服务商: 接入形态按各官方授权机制分别实现。
type OfficialAccountProvider string

const (
	OfficialAccountProviderOpenAI OfficialAccountProvider = "openai" // OpenAI 官方账号: OAuth 授权接入。
	OfficialAccountProviderGemini OfficialAccountProvider = "gemini" // Gemini 官方账号: Google OAuth 授权接入。
	OfficialAccountProviderClaude OfficialAccountProvider = "claude" // Claude 官方账号: Anthropic OAuth 授权接入。
)

// OfficialAccountStatus 是官方账号的接入状态。
type OfficialAccountStatus string

const (
	OfficialAccountStatusPending OfficialAccountStatus = "pending" // 授权链接已生成, 待扫码/确认。
	OfficialAccountStatusActive  OfficialAccountStatus = "active"  // 凭据有效, 可读取套餐/健康/窗口。
	OfficialAccountStatusExpired OfficialAccountStatus = "expired" // 凭据过期, 需刷新或重新授权。
	OfficialAccountStatusRevoked OfficialAccountStatus = "revoked" // 用户在官方侧撤销, 需重新授权。
	OfficialAccountStatusError   OfficialAccountStatus = "error"   // 最近一次读取失败, 见 LastError。
)

// OfficialAccount 是接管的官方账号: 经官方授权链接/扫码接入, 凭据加密入库。
// 账号本身不直接参与转发: T-pool-002 把账号映射为渠道凭据后才进入选路。
// 凭据加密口径关联 R-sec-001/U-sec-001: AccessCipher/RefreshCipher 为加密后密文,
// 解密密钥走环境变量/密钥服务, 绝不落库, 接口与日志绝不回显明文。
// 套餐/窗口读的是官方侧元数据快照: 档位名称、5H/7D 可用窗口余量、健康态,
// 由接入成功后的读取接口回填, 定时刷新由 T-quota-001/T-acct-003 消费本表。
type OfficialAccount struct {
	ID            int                     `json:"id" gorm:"primaryKey"`                                            // 账号主键。
	Provider      OfficialAccountProvider `json:"provider" gorm:"not null;index:idx_official_account,unique"`      // 服务商。
	ExternalName  string                  `json:"external_name" gorm:"not null;index:idx_official_account,unique"` // 官方侧账号标识 (邮箱/组织名), 同服务商内唯一, 人工识别用。
	Status        OfficialAccountStatus   `json:"status" gorm:"not null;default:pending;index"`                    // 接入状态。
	AccessCipher  string                  `json:"-" gorm:"not null"`                                               // 加密后的访问凭据, 不出 JSON。
	RefreshCipher string                  `json:"-" gorm:"not null;default:''"`                                    // 加密后的刷新凭据 (无刷新机制的服务商为空), 不出 JSON。
	ExpiresAt     *time.Time              `json:"expires_at"`                                                      // 访问凭据过期时间, 无过期机制为空。
	PlanTier      string                  `json:"plan_tier"`                                                       // 套餐档位 (官方侧名称原样存, 如 Plus/Pro/Team), 为空表示尚未读取。
	Window5H      string                  `json:"window_5h" gorm:"column:window_5h"`                               // 5 小时可用窗口快照 (官方侧原样, 如 "62%" 或剩余量), 为空表示尚未读取。
	Window7D      string                  `json:"window_7d" gorm:"column:window_7d"`                               // 7 天可用窗口快照, 语义同上。
	Healthy       bool                    `json:"healthy"`                                                         // 最近一次健康检查是否通过。
	LastCheckedAt *time.Time              `json:"last_checked_at"`                                                 // 最近一次健康/窗口读取时间。
	LastError     string                  `json:"last_error"`                                                      // 最近一次失败原因, 成功后清零。
	CreatedAt     time.Time               `json:"created_at"`
	UpdatedAt     time.Time               `json:"updated_at"`
}

// OfficialAccountCreateRequest 是发起接入的请求: 服务商必选, 账号标识在回调确认后回填。
type OfficialAccountCreateRequest struct {
	Provider OfficialAccountProvider `json:"provider" binding:"required,oneof=openai gemini claude"` // 服务商。
}

// OfficialAccountCallbackRequest 是授权回调/扫码确认的请求: 携带官方侧一次凭证。
// OneTimeCode 只能使用一次, 后端换取 token 后即丢弃, 绝不落库。
type OfficialAccountCallbackRequest struct {
	AccountID   int    `json:"account_id" binding:"required"`    // 待确认的账号主键 (pending 态)。
	OneTimeCode string `json:"one_time_code" binding:"required"` // 官方侧一次性凭证 (授权码/扫码 token)。
	State       string `json:"state" binding:"required"`         // 防 CSRF 的 state, 必须与发起时签发的一致。
}

// OfficialPoolSyncRequest 是号池同步请求: provider 留空表示同步全部服务商。
// 同步只物化/刷新凭据侧映射, 不拉模型也不改协议位: 那是渠道拉取模型流程的职责。
type OfficialPoolSyncRequest struct {
	Provider OfficialAccountProvider `json:"provider"` // 服务商; 留空同步全部三类。
}

// OfficialPoolSyncResult 是单个服务商一次号池同步的结论, 同时充当同步响应条目。
// 字段给的是操作者判断"号池能不能用"所需的最小集: 凭据侧物化了几条、
// 有多少因账号失活被停用、刷新了几条临期 token; 模型与授权数是既有拉取流程的产物, 一并回显。
type OfficialPoolSyncResult struct {
	Provider    OfficialAccountProvider `json:"provider"`        // 服务商。
	ChannelID   int                     `json:"channel_id"`      // 号池渠道主键; 0 表示尚未建立。
	ChannelName string                  `json:"channel_name"`    // 号池渠道名。
	Keys        int                     `json:"keys"`            // 本轮结束时该渠道下启用的凭据数。
	Disabled    int                     `json:"disabled"`        // 本轮被停用的凭据数(账号非 active)。
	Refreshed   int                     `json:"refreshed"`       // 本轮成功刷新 access token 的账号数。
	Models      int                     `json:"models"`          // 号池渠道已有模型数(拉取模型流程产出)。
	Grants      int                     `json:"grants"`          // 号池渠道已有授权数(模型×凭据组合)。
	Notes       []string                `json:"notes,omitempty"` // 跳过或需人工处理的事项, 逐条说明原因。
}

// OfficialPoolMember 是统一号池视图里的一行: 一个账号与它物化出的那条凭据。
// 只回凭据名称与启用状态, 绝不回显凭据内容(密文/明文都不出接口)。
type OfficialPoolMember struct {
	AccountID    int                   `json:"account_id"`    // 账号主键。
	ExternalName string                `json:"external_name"` // 官方侧账号标识。
	Status       OfficialAccountStatus `json:"status"`        // 账号接入状态。
	ExpiresAt    *time.Time            `json:"expires_at"`    // 访问凭据过期时间。
	PlanTier     string                `json:"plan_tier"`     // 套餐档位快照。
	Window5H     string                `json:"window_5h"`     // 5 小时窗口余量快照。
	Window7D     string                `json:"window_7d"`     // 7 天窗口余量快照。
	Healthy      bool                  `json:"healthy"`       // 最近一次健康检查是否通过。
	LastError    string                `json:"last_error"`    // 最近一次失败原因。
	KeyName      string                `json:"key_name"`      // 凭据名称; 空表示尚未物化。
	KeyExists    bool                  `json:"key_exists"`    // 是否已物化凭据行(非活跃账号只停用不删行)。
	KeyEnabled   bool                  `json:"key_enabled"`   // 该凭据当前是否启用。
}

// OfficialPoolStatus 是号池当前映射的只读快照, 供界面核对"哪个账号映射成了哪条凭据"。
// Members 是逐账号明细: 界面拿这一个接口即可画出统一号池视图(openai/gemini/claude 同一张表),
// 无需再自己把账号表与渠道凭据表 join 起来。
type OfficialPoolStatus struct {
	Provider    OfficialAccountProvider `json:"provider"`     // 服务商。
	ChannelID   int                     `json:"channel_id"`   // 号池渠道主键; 0 表示尚未建立。
	ChannelName string                  `json:"channel_name"` // 号池渠道名。
	Accounts    int                     `json:"accounts"`     // 该服务商已接入的账号数(含非 active)。
	ActiveKeys  int                     `json:"active_keys"`  // 已物化的启用凭据数。
	Models      int                     `json:"models"`       // 已有模型数。
	Grants      int                     `json:"grants"`       // 已有授权数。
	Members     []OfficialPoolMember    `json:"members"`      // 逐账号明细(账号 → 凭据映射)。
}

// ValidateOfficialAccountProvider 校验服务商取值在三类官方账号范围内。
func ValidateOfficialAccountProvider(provider OfficialAccountProvider) error {
	switch provider {
	case OfficialAccountProviderOpenAI, OfficialAccountProviderGemini, OfficialAccountProviderClaude:
		return nil
	default:
		return fmt.Errorf("unknown official account provider %q, want openai/gemini/claude", string(provider))
	}
}
