package model

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Sub2APIPlatform 支持接入的平台类型
type Sub2APIPlatform string

const (
	Sub2APIPlatformAnthropic   Sub2APIPlatform = "anthropic"
	Sub2APIPlatformOpenAI      Sub2APIPlatform = "openai"
	Sub2APIPlatformGemini      Sub2APIPlatform = "gemini"
	Sub2APIPlatformAntigravity Sub2APIPlatform = "antigravity"
	Sub2APIPlatformGrok        Sub2APIPlatform = "grok"
	Sub2APIPlatformKimi        Sub2APIPlatform = "kimi"
	Sub2APIPlatformDeepSeek    Sub2APIPlatform = "deepseek"
	Sub2APIPlatformOpenCode    Sub2APIPlatform = "opencode_go"
	Sub2APIPlatformCline       Sub2APIPlatform = "cline"
)

// Sub2APIAccountType 订阅账号类型
type Sub2APIAccountType string

const (
	Sub2APIAccountTypeOAuth      Sub2APIAccountType = "oauth"       // OAuth订阅反代账号
	Sub2APIAccountTypeAPIKey     Sub2APIAccountType = "apikey"      // 平台标准 API Key / Session Token
	Sub2APIAccountTypeSetupToken Sub2APIAccountType = "setup-token" // Claude Setup Token
)

// Sub2APIAccount 接入 Sub2API 的统一订阅账号与反代配置
type Sub2APIAccount struct {
	ID          int                `json:"id" gorm:"primaryKey"`
	Name        string             `json:"name" gorm:"not null"`
	Platform    Sub2APIPlatform    `json:"platform" gorm:"not null;index"`
	AccountType Sub2APIAccountType `json:"account_type" gorm:"not null;default:'oauth'"`
	BaseURL     string             `json:"base_url" gorm:"not null"`                                // 反代目标基地址
	AccountMode string             `json:"account_mode" gorm:"not null;default:'payg'"`             // payg | coding | go
	APIProtocol string             `json:"api_protocol" gorm:"not null;default:'chat_completions'"` // chat_completions | responses | anthropic | adaptive
	Enabled     bool               `json:"enabled" gorm:"not null"`                                 // 默认值在入口层赋予，禁止 gorm default:true 避免覆写显式 false
	ProxyNodeID int                `json:"proxy_node_id" gorm:"not null;default:0"`                 // 出口节点绑定
	RateLimit   int                `json:"rate_limit" gorm:"not null;default:0"`                    // 并发或RPM限制
	Credentials string             `json:"-" gorm:"not null;default:''"`                            // 加密存储的凭据 JSON
	Extra       string             `json:"extra" gorm:"not null;default:'{}'"`                      // 自定义请求头覆写或高级配置
	Notes       string             `json:"notes"`
	ChannelID   int                `json:"channel_id" gorm:"not null;default:0"` // 物化绑定的渠道 ID
	// LastSyncModels 记录上一次成功同步时上游返回的模型集合(JSON 数组文本)。
	// 它是"下架"判据的基准: 只有上次在上游、这次不在的模型才算下架;
	// 物化预置与手填模型从未进入过该集合, 不会被误报为下架(R6-F1)。
	LastSyncModels string    `json:"-" gorm:"not null;default:''"`
	CreatedAt      time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (Sub2APIAccount) TableName() string {
	return "sub2api_accounts"
}

func ValidateSub2APIPlatform(p Sub2APIPlatform) error {
	switch p {
	case Sub2APIPlatformAnthropic, Sub2APIPlatformOpenAI, Sub2APIPlatformGemini,
		Sub2APIPlatformAntigravity, Sub2APIPlatformGrok, Sub2APIPlatformKimi,
		Sub2APIPlatformDeepSeek, Sub2APIPlatformOpenCode, Sub2APIPlatformCline:
		return nil
	default:
		return fmt.Errorf("不支持的 Sub2API 平台类型: %s", p)
	}
}

func ValidateSub2APIBaseURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("反代基地址不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("反代基地址解析失败: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("反代基地址协议必须为 http 或 https")
	}
	if u.Host == "" {
		return fmt.Errorf("反代基地址缺少主机名")
	}
	return nil
}

func ValidateSub2APIAccountType(t Sub2APIAccountType) error {
	switch t {
	case Sub2APIAccountTypeOAuth, Sub2APIAccountTypeAPIKey, Sub2APIAccountTypeSetupToken:
		return nil
	default:
		return fmt.Errorf("不支持的 Sub2API 认证形式: %s", t)
	}
}

func ValidateSub2APIProtocol(proto string) error {
	switch proto {
	case "chat_completions", "responses", "anthropic", "adaptive":
		return nil
	default:
		return fmt.Errorf("不支持的 Sub2API 协议类型: %s", proto)
	}
}

func ValidateSub2APIAccountMode(mode string) error {
	switch mode {
	case "payg", "coding", "go":
		return nil
	default:
		return fmt.Errorf("不支持的 Sub2API 账号模式: %s", mode)
	}
}

func ValidateSub2APIName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("账号别名不能为空")
	}
	return nil
}

func ValidateSub2APIRateLimit(limit int) error {
	if limit < 0 {
		return fmt.Errorf("速率限制不能为负数")
	}
	return nil
}
