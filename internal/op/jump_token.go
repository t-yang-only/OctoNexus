package op

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-acct-005 手动登录一次性跳转令牌（NA/S2）。
// 口径：明文只在创建响应里出现一次，库内仅存 SHA-256 哈希；
// TTL 120s 压缩截获窗口；消费走条件 UPDATE（consumed_at IS NULL AND
// expires_at > now），并发下至多一次成功，重复消费被拒绝；
// 令牌行即审计记录（actor/created/expires/consumed 四列）。
// 管理员权限门由路由侧 middleware.Auth()+ServerAdmin 端口承担，本层不重复校验。

var (
	// ErrJumpTokenNotFound 表示令牌不存在（含哈希不匹配）。统一口径避免探测有效令牌。
	ErrJumpTokenNotFound = errors.New("jump token not found")
	// ErrJumpTokenConsumed 表示令牌已被消费（重复使用被拒绝）。
	ErrJumpTokenConsumed = errors.New("jump token already consumed")
	// ErrJumpTokenExpired 表示令牌已过期。
	ErrJumpTokenExpired = errors.New("jump token expired")
)

// JumpTokenStore 是可注入的令牌存储：生产走全局库，测试传直连内存库。
type JumpTokenStore interface {
	Create(token *model.JumpToken) error
	FindByHash(hash string) (model.JumpToken, error)
	Consume(hash string, now time.Time) (int64, error)
}

type jumpTokenDBStore struct{ conn *gorm.DB }

func (s jumpTokenDBStore) Create(token *model.JumpToken) error {
	return s.conn.Create(token).Error
}

func (s jumpTokenDBStore) FindByHash(hash string) (model.JumpToken, error) {
	var token model.JumpToken
	err := s.conn.Where("token_hash = ?", hash).First(&token).Error
	return token, err
}

func (s jumpTokenDBStore) Consume(hash string, now time.Time) (int64, error) {
	result := s.conn.Model(&model.JumpToken{}).
		Where("token_hash = ? AND consumed_at IS NULL AND expires_at > ?", hash, now).
		Updates(map[string]any{"consumed_at": now, "note": "consumed"})
	return result.RowsAffected, result.Error
}

// NewJumpTokenStoreForTest 暴露直连内存库的存储实现（仅测试接线用）。
func NewJumpTokenStoreForTest(conn *gorm.DB) JumpTokenStore {
	return jumpTokenDBStore{conn: conn}
}

func jumpTokenStore(conn *gorm.DB) JumpTokenStore {
	if conn != nil {
		return jumpTokenDBStore{conn: conn}
	}
	return jumpTokenDBStore{conn: db.GetDB()}
}

// NewJumpToken 生成一枚跳转令牌：返回明文（仅此一次）与落库行。
// kind 限 na/s2；targetURL 须为无内嵌凭据的 http(s) 绝对地址。
func NewJumpToken(conn *gorm.DB, kind, targetURL, actor string) (string, model.JumpToken, error) {
	if !model.ValidJumpTokenKind(kind) {
		return "", model.JumpToken{}, fmt.Errorf("unknown jump token kind %q", kind)
	}
	if err := model.ValidateJumpTargetURL(targetURL); err != nil {
		return "", model.JumpToken{}, err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", model.JumpToken{}, fmt.Errorf("generate jump token: %w", err)
	}
	plain := hex.EncodeToString(raw)
	now := time.Now()
	token := model.JumpToken{
		TokenHash: hashJumpToken(plain),
		Kind:      kind,
		TargetURL: targetURL,
		Actor:     actor,
		ExpiresAt: now.Add(time.Duration(model.JumpTokenTTLSeconds) * time.Second),
	}
	if err := jumpTokenStore(conn).Create(&token); err != nil {
		return "", model.JumpToken{}, fmt.Errorf("persist jump token: %w", err)
	}
	return plain, token, nil
}

// ConsumeJumpToken 一次性消费令牌，成功返回跳转目标行。
// 不存在/已消费/已过期分别返回哨兵错误；条件更新保证并发下至多一次成功。
func ConsumeJumpToken(conn *gorm.DB, plain string) (model.JumpToken, error) {
	store := jumpTokenStore(conn)
	hash := hashJumpToken(sanitizeJumpTokenInput(plain))
	affected, err := store.Consume(hash, time.Now())
	if err != nil {
		return model.JumpToken{}, fmt.Errorf("consume jump token: %w", err)
	}
	token, err := store.FindByHash(hash)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return model.JumpToken{}, ErrJumpTokenNotFound
		}
		return model.JumpToken{}, fmt.Errorf("load jump token: %w", err)
	}
	if affected == 0 {
		// 分类失败原因仅用于本地诊断响应；对无令牌探测者统一走 NotFound。
		if token.ConsumedAt != nil {
			return model.JumpToken{}, ErrJumpTokenConsumed
		}
		return model.JumpToken{}, ErrJumpTokenExpired
	}
	return token, nil
}

// ListJumpTokens 倒序分页返回令牌审计行（limit 限幅防无界查询）。
func ListJumpTokens(conn *gorm.DB, limit, offset int) ([]model.JumpToken, int64) {
	if limit <= 0 || limit > model.JumpTokenListPageLimit {
		limit = model.JumpTokenListPageLimit
	}
	if offset < 0 {
		offset = 0
	}
	target := jumpTokenQuery(conn)
	var total int64
	if err := target.Count(&total).Error; err != nil {
		return nil, 0
	}
	var tokens []model.JumpToken
	if err := target.Order("id DESC").Limit(limit).Offset(offset).Find(&tokens).Error; err != nil {
		return nil, total
	}
	return tokens, total
}

func jumpTokenQuery(conn *gorm.DB) *gorm.DB {
	if conn != nil {
		return conn.Model(&model.JumpToken{})
	}
	return db.GetDB().Model(&model.JumpToken{})
}

func hashJumpToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// sanitizeJumpTokenInput 拒绝超长输入，避免把随机哈希运算变成放大点。
func sanitizeJumpTokenInput(plain string) string {
	if len(plain) > 128 {
		return ""
	}
	return plain
}
