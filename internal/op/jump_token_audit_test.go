package op

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// TestJumpTokenJSONNeverCarriesHashOrPlaintext 审计红线：任何响应/导出形状里
// 都不许出现令牌哈希或明文（R-sec-001：日志与输出绝不回显凭据的同类要求）。
func TestJumpTokenJSONNeverCarriesHashOrPlaintext(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	plain, created, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://na.example.com", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tokens, _ := ListJumpTokens(conn, 50, 0)
	if len(tokens) != 1 {
		t.Fatalf("page = %d, want 1 row", len(tokens))
	}
	blob, err := json.Marshal(tokens)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(blob)
	if strings.Contains(out, plain) {
		t.Fatalf("plaintext token leaked in list JSON: %s", out)
	}
	if strings.Contains(out, created.TokenHash) {
		t.Fatalf("token hash leaked in list JSON: %s", out)
	}
}

// TestConsumeJumpTokenOversizedInputRejected 超 128 字符输入走统一 NotFound：
// 不做哈希放大、也不泄露是否存在（与未知令牌口径一致）。
func TestConsumeJumpTokenOversizedInputRejected(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	if _, _, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://na.example.com", "admin"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := ConsumeJumpToken(conn, strings.Repeat("x", 129)); err != ErrJumpTokenNotFound {
		t.Fatalf("oversized input err = %v, want ErrJumpTokenNotFound", err)
	}
}

// TestNewJumpTokenTTLWindowIs120s 锁创建窗口口径: ExpiresAt = 创建时刻 + 120s,
// 防误改常量或时区/单调钟处理导致截获窗口被悄悄拉长。
func TestNewJumpTokenTTLWindowIs120s(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	_, token, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://na.example.com", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	window := time.Until(token.ExpiresAt)
	if window < 115*time.Second || window > 120*time.Second {
		t.Fatalf("ttl window = %v, want within (115s,120s]", window)
	}
}

// TestConsumeJumpTokenStrictExpiryBoundary 锁消费 SQL 的严格 `expires_at > now` 口径:
// 以库内回读的 expires_at 为界, 恰好等于该时刻必须判过期 (0 行受影响), 其后判过期,
// 其前一秒放行。用回读值作比较基准规避驱动对次秒精度的截断差异。
func TestConsumeJumpTokenStrictExpiryBoundary(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	store := NewJumpTokenStoreForTest(conn)
	plain, created, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://na.example.com", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var stored model.JumpToken
	if err := conn.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	hash := hashJumpToken(plain)
	// 恰等于 expires_at: 严格 > 不成立 → 不可消费。
	if affected, err := store.Consume(hash, stored.ExpiresAt); err != nil || affected != 0 {
		t.Fatalf("consume at exact expires_at affected=%d err=%v, want 0 rows", affected, err)
	}
	// 过期一秒后仍不可消费。
	if affected, err := store.Consume(hash, stored.ExpiresAt.Add(time.Second)); err != nil || affected != 0 {
		t.Fatalf("consume 1s after expiry affected=%d err=%v, want 0 rows", affected, err)
	}
	// 过期前一秒放行, 且只放行一次。
	if affected, err := store.Consume(hash, stored.ExpiresAt.Add(-time.Second)); err != nil || affected != 1 {
		t.Fatalf("consume 1s before expiry affected=%d err=%v, want 1 row", affected, err)
	}
	if affected, err := store.Consume(hash, stored.ExpiresAt.Add(-time.Second)); err != nil || affected != 0 {
		t.Fatalf("re-consume within window affected=%d err=%v, want 0 rows (one-time)", affected, err)
	}
}
