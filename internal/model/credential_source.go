package model

import "time"

// 采集凭据的两种形态。
const (
	CredentialKindLogin = "login" // 交互登录：octopus 反代登录页，人在页面里登录，服务端捕获会话
	CredentialKindPack  = "pack"  // 全自动包：逆向出来的登录+查账描述，octopus 长期自动跑
)

// 采集凭据的状态。
const (
	CredentialStatusPending    = "pending"    // 还没登录/没跑过
	CredentialStatusAuthorized = "authorized" // 会话可用，最近一次读到了账
	CredentialStatusFailed     = "failed"     // 最近一次失败（原因见 last_error）
)


// CredentialSource 是一个"账号级采集凭据"：它回答"这家中转站的余额/用量怎么读"。
//
// 存在的理由：绝大多数中转站不给能读余额的 API Key 接口（实测 15 家里 12 家 404、
// 3 家要站点面板访问令牌），但它们的面板本身有余额页。于是只有两条路能拿到数：
//   - 人工在 octopus 托管的登录页里登录一次，octopus 捕获会话（Kind=login）；
//   - 自己把面板的登录/查账接口逆向成一份"包"，交给 octopus 长期自动跑（Kind=pack）。
//
// 两种形态共用同一份会话与同一套读取步骤（collector.Pack），因此实现上是一个东西的两个入口。
type CredentialSource struct {
	ID          int    `gorm:"primaryKey" json:"id"`
	Name        string `gorm:"uniqueIndex" json:"name"`         // 唯一名，便于面板辨认
	Kind        string `json:"kind"`                            // login（交互登录）/ pack（全自动包）
	Site        string `json:"site"`                            // 站点根地址（交互登录的反代目标）
	// ProxyNodeID 是这条凭据的出网出口（0 = 直连，保持既有行为）。
	// 站点会看到出口的 IP 而不是本机真实 IP —— 这是"不让我的 IP 被拉黑"的实现点。
	ProxyNodeID int `json:"proxy_node_id" gorm:"column:proxy_node_id"`
	ChannelID   int    `json:"channel_id"`                      // 绑定渠道（0=不绑定，仅本地查看）
	Enabled     bool   `json:"enabled"`                         // 注意：不加 gorm default 标签（零值会被写成 true）
	AutoRefresh bool   `json:"auto_refresh"`                    // 纳入余额扫描周期
	Units       string `json:"units"`                           // usd / points：读数单位（包自己声明）
	Status      string `json:"status"`                          // pending（未登录）/ authorized / failed
	Username    string `json:"username"`                        // 登录名（展示用；密码只进密文）
	Hosts       string `json:"hosts"`                           // 采集包声明的主机（逗号分隔，展示与审计用）
	PackCipher  string `gorm:"column:pack_cipher" json:"-"`     // 密文：采集包 JSON
	CredCipher  string `gorm:"column:cred_cipher" json:"-"`     // 密文：账号密码等凭据
	SessionCipher string `gorm:"column:session_cipher" json:"-"` // 密文：登录会话（Cookie/令牌）
	LastReadAt  string `json:"last_read_at"`
	LastError   string `json:"last_error"`
	LastBalance float64 `json:"last_balance"`
	LastUsed    float64 `json:"last_used"`
	LastQuota   float64 `json:"last_quota"`
	LastCurrency string `json:"last_currency"`
	Note        string `json:"note"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CredentialSourceInput 是管理面的写入载荷。
// Pack 与凭据都是"留空表示不改"：面板改一个开关不必把整份包重发一遍（也就不会因为漏发而清空）。
type CredentialSourceInput struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Site        string `json:"site"`
	// ProxyNodeID 用指针区分「没给」与「显式改成直连(0)」：
	// 老写法是 int，面板只改出口而不重发 site 时会被静默丢弃（实测复现过）。
	ProxyNodeID *int `json:"proxy_node_id"`
	ChannelID   int    `json:"channel_id"`
	Enabled     *bool  `json:"enabled"`
	AutoRefresh *bool  `json:"auto_refresh"`
	Pack        string `json:"pack"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Captcha     string `json:"captcha"`
	OTP         string `json:"otp"`
}

// CredentialSourceDetail 是给面板看的形态：行 + 采集包原文 + 会话状态。
// 凭据与密文一律不出现在这里（只有 has_credentials 这个布尔）。
type CredentialSourceDetail struct {
	CredentialSource
	PackText       string `json:"pack_text"`
	HasCredentials bool   `json:"has_credentials"`
	HasSession     bool   `json:"has_session"`
	LoginURL       string `json:"login_url"`      // 交互登录的临时地址（未开始时为空）
	Steps          []string `json:"steps,omitempty"` // 最近一次采集的步骤轨迹（脱敏）
}

// CollectorReading 是一次采集的对外结果（余额管线与面板共用）。
type CollectorReading struct {
	SourceID  int     `json:"source_id"`
	ChannelID int     `json:"channel_id"`
	Units     string  `json:"units"`
	Balance   float64 `json:"balance"`
	Used      float64 `json:"used"`
	Quota     float64 `json:"quota"`
	Currency  string  `json:"currency"`
	Username  string  `json:"username"`
	Note      string  `json:"note"`
	At        string  `json:"at"`
}
