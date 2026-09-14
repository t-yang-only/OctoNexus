package model

import (
	"fmt"
	"net/url"
	"time"
)

// JumpToken 是手动登录一次性跳转令牌（T-acct-005）。
// 库内只存令牌 SHA-256 哈希，明文只在创建响应中出现一次；
// 行本身即审计记录：actor 创建人、expires_at 过期、consumed_at 消费时刻。
type JumpToken struct {
	ID         uint64     `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt  time.Time  `json:"created_at" gorm:"autoCreateTime;index"`
	TokenHash  string     `json:"-" gorm:"uniqueIndex;not null"` // sha256 hex，明文永不落库
	Kind       string     `json:"kind" gorm:"index;not null"`    // na | s2
	TargetURL  string     `json:"target_url" gorm:"not null"`
	Actor      string     `json:"actor"`
	ExpiresAt  time.Time  `json:"expires_at" gorm:"index"`
	ConsumedAt *time.Time `json:"consumed_at"`
	Note       string     `json:"note"`
}

// 跳转令牌口径常量：短 TTL 降低截获窗口；种类与中转站登录域一一对应。
const (
	JumpTokenKindNA        = "na" // New API 系中转站登录页
	JumpTokenKindS2        = "s2" // Sub2Api 中转站登录页
	JumpTokenTTLSeconds    = 120
	JumpTokenListPageLimit = 50
)

// ValidJumpTokenKind 校验跳转目标种类。
func ValidJumpTokenKind(kind string) bool {
	return kind == JumpTokenKindNA || kind == JumpTokenKindS2
}

// ValidateJumpTargetURL 校验跳转目标：仅 http/https、必须有主机名、
// 禁止内嵌凭据（防把带密码的 URL 写进跳转页），拒绝 javascript 等伪协议。
func ValidateJumpTargetURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("target url is required")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("target url is invalid: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("target url scheme must be http or https")
	}
	if parsed.Host == "" {
		return fmt.Errorf("target url must include a host")
	}
	if parsed.User != nil {
		return fmt.Errorf("target url must not embed credentials")
	}
	return nil
}
