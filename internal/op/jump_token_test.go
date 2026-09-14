package op

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var jumpTokenTestDBSeq int64

// openJumpTokenTestDB 直连内存库；DSN 按测试名+计数器隔离（NM-CUR-042 count-safe 惯例），
// 防 -count=N 重跑复用共享缓存库导致行号与计数断言撞历史数据。
func openJumpTokenTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&jumpTokenTestDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.JumpToken{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn
}

func TestNewJumpTokenCreatesHashedRow(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	plain, token, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://relay.example.com/login", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(plain) != 64 {
		t.Fatalf("token length = %d, want 64 hex chars", len(plain))
	}
	if token.TokenHash == plain || token.TokenHash == "" {
		t.Fatalf("hash must not equal plaintext and must be set: %q", token.TokenHash)
	}
	if token.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expires_at = %v, want future", token.ExpiresAt)
	}
}

func TestNewJumpTokenRejectsBadInput(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	if _, _, err := NewJumpToken(conn, "oauth", "https://x.com", "admin"); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, _, err := NewJumpToken(conn, model.JumpTokenKindNA, "javascript:alert(1)", "admin"); err == nil {
		t.Fatal("javascript scheme accepted")
	}
	if _, _, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://u:p@x.com", "admin"); err == nil {
		t.Fatal("embedded credentials accepted")
	}
	if _, _, err := NewJumpToken(conn, model.JumpTokenKindS2, "", "admin"); err == nil {
		t.Fatal("empty url accepted")
	}
}

func TestConsumeJumpTokenOnce(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	plain, _, err := NewJumpToken(conn, model.JumpTokenKindS2, "https://s2.example.com", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	token, err := ConsumeJumpToken(conn, plain)
	if err != nil || token.TargetURL != "https://s2.example.com" {
		t.Fatalf("first consume = %v err=%v", token, err)
	}
	if _, err := ConsumeJumpToken(conn, plain); !errors.Is(err, ErrJumpTokenConsumed) {
		t.Fatalf("second consume err = %v, want ErrJumpTokenConsumed", err)
	}
}

func TestConsumeJumpTokenExpired(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	plain, token, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://na.example.com", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := conn.Model(&model.JumpToken{}).Where("id = ?", token.ID).
		Update("expires_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatalf("force expire: %v", err)
	}
	if _, err := ConsumeJumpToken(conn, plain); !errors.Is(err, ErrJumpTokenExpired) {
		t.Fatalf("expired consume err = %v, want ErrJumpTokenExpired", err)
	}
}

func TestConsumeJumpTokenUnknown(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	if _, err := ConsumeJumpToken(conn, "deadbeef"); !errors.Is(err, ErrJumpTokenNotFound) {
		t.Fatalf("unknown token err = %v, want ErrJumpTokenNotFound", err)
	}
}

func TestConsumeJumpTokenConcurrent(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	plain, _, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://na.example.com", "admin")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	const racers = 8
	result := make(chan error, racers)
	for range racers {
		go func() {
			_, err := ConsumeJumpToken(conn, plain)
			result <- err
		}()
	}
	successes := 0
	for range racers {
		if err := <-result; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent successes = %d, want exactly 1", successes)
	}
}

func TestListJumpTokensBounds(t *testing.T) {
	conn := openJumpTokenTestDB(t)
	for range 3 {
		if _, _, err := NewJumpToken(conn, model.JumpTokenKindNA, "https://na.example.com", "admin"); err != nil {
			t.Fatalf("create: %v", err)
		}
	}
	tokens, total := ListJumpTokens(conn, 2, 0)
	if total != 3 || len(tokens) != 2 {
		t.Fatalf("page = %d/%d, want 2/3", len(tokens), total)
	}
	// ListJumpTokens 按 id DESC 倒序: 首页两条必须是新→旧, 否则前端审计列表顺序错乱。
	if tokens[0].ID <= tokens[1].ID {
		t.Fatalf("list must be id DESC, got %d before %d", tokens[0].ID, tokens[1].ID)
	}
	unbounded, _ := ListJumpTokens(conn, 0, 0)
	if len(unbounded) != 3 {
		t.Fatalf("default limit page = %d, want 3", len(unbounded))
	}
}
