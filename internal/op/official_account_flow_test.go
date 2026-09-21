package op

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/secret"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 环境与桩隔离工具：保存并恢复 OCTOPUS_OFFICIAL_KEY，换码/读侧桩按需注入。
func withOfficialKey(t *testing.T, value string) {
	t.Helper()
	orig := officialHTTPClient
	t.Cleanup(func() { officialHTTPClient = orig })
	t.Setenv("OCTOPUS_OFFICIAL_KEY", value)
	// 密钥走 internal/secret 的统一口径（进程内缓存一次，与渠道凭据一致）：
	// 换环境变量后必须重置缓存，否则本轮换的密钥不生效（生产上等价于"改环境变量要重启"）。
	secret.ResetKey()
	t.Cleanup(secret.ResetKey)
	// 并行车道 (NM-CUR-231) 给 authorize/exchange 加了 client id 必填校验；
	// 流程测试走桩换码器，补齐环境变量让校验放行。
	for _, p := range []string{"OPENAI", "GEMINI", "CLAUDE"} {
		t.Setenv("OCTOPUS_OFFICIAL_CLIENT_ID_"+p, "test-client-id")
	}
}

var officialFlowDBSeq int64

// openOfficialFlowTestDB 独立内存库：全局 DB 未初始化，流程测试全部显式传 conn。
func openOfficialFlowTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&officialFlowDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open flow test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.OfficialAccount{}); err != nil {
		t.Fatalf("migrate flow test db: %v", err)
	}
	return conn
}

var officialTestSeq int64

func nextOfficialName(t *testing.T) string {
	t.Helper()
	n := atomic.AddInt64(&officialTestSeq, 1)
	return fmt.Sprintf("acct-%s-%d", t.Name(), n)
}

type stubExchanger struct {
	bundle OfficialTokenBundle
	calls  int
}

func (s *stubExchanger) Exchange(provider model.OfficialAccountProvider, code, verifier string) (OfficialTokenBundle, error) {
	s.calls++
	if s.calls > 1 {
		return OfficialTokenBundle{}, errStubReplay
	}
	return s.bundle, nil
}

var errStubReplay = &replayError{}

type replayError struct{}

func (*replayError) Error() string { return "stub replay: code already used" }

type stubReader struct{ snap UsageSnapshot }

func (s stubReader) Read(provider model.OfficialAccountProvider, accessToken string) (UsageSnapshot, error) {
	return s.snap, nil
}

// TestOfficialAccountAuthorizeRejectsWithoutKey 加密密钥真的不可得时发起接入必须拒绝，不落 pending 行。
//
// 「不可得」的定义必须是**通道级**不可得：环境变量没设 **且** 数据目录写不出 credential.key。
// 旧版本把「没设环境变量」直接当成不可得，于是正常安装上号池/官方账号整条功能一用就报
// official credential cipher key not configured —— 那是缺陷（同一实例的渠道凭据加密明明是好的），
// 所以这里改成用「数据目录被占住」制造真正的不可得，判据仍然是"拒绝且不落 pending"。
func TestOfficialAccountAuthorizeRejectsWithoutKey(t *testing.T) {
	blockCipherKeySource(t)
	if _, _, _, err := OfficialAccountAuthorize(nil, model.OfficialAccountProviderOpenAI); err == nil {
		t.Fatalf("authorize without key: want error")
	} else if !strings.Contains(err.Error(), "cipher key") {
		t.Fatalf("authorize without key: err = %v, want cipher key error", err)
	}
}

// blockCipherKeySource 制造"密钥真的拿不到"的环境：不设环境变量，且数据库所在目录的父路径是个文件，
// 于是 credential.key 既读不到也建不出来。测试结束自动还原。
func blockCipherKeySource(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	previous := conf.AppConfig.Database.Path
	conf.AppConfig.Database.Path = filepath.Join(blocker, "data.db")
	if err := os.Unsetenv("OCTOPUS_OFFICIAL_KEY"); err != nil {
		t.Fatalf("unset env: %v", err)
	}
	secret.ResetKey()
	t.Cleanup(func() {
		conf.AppConfig.Database.Path = previous
		secret.ResetKey()
	})
}

// TestOfficialAccountStateExpiry 过期 state 条件消费必须回 Expired 且被删除。
func TestOfficialAccountStateExpiry(t *testing.T) {
	state := "expire-state-" + nextOfficialName(t)
	putOfficialState(state, officialStateEntry{
		accountID: 1, provider: model.OfficialAccountProviderOpenAI, verifier: "v", expiresAt: time.Now().Add(-time.Second),
	})
	if _, err := consumeOfficialState(state); err != ErrOfficialStateExpired {
		t.Fatalf("consume expired state: err = %v, want ErrOfficialStateExpired", err)
	}
	// 过期项已删除：二次消费必须回 Unknown 而非再次 Expired。
	if _, err := consumeOfficialState(state); err != ErrOfficialStateUnknown {
		t.Fatalf("re-consume expired state: err = %v, want ErrOfficialStateUnknown", err)
	}
}

// TestOfficialAccountStateReplayConsumed 已消费 state 重放必须回 Unknown（取走即删语义）。
func TestOfficialAccountStateReplayConsumed(t *testing.T) {
	state := "replay-state-" + nextOfficialName(t)
	putOfficialState(state, officialStateEntry{
		accountID: 2, provider: model.OfficialAccountProviderGemini, verifier: "v", expiresAt: time.Now().Add(time.Minute),
	})
	if _, err := consumeOfficialState(state); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if _, err := consumeOfficialState(state); err != ErrOfficialStateUnknown {
		t.Fatalf("replay consume: err = %v, want ErrOfficialStateUnknown", err)
	}
}

// TestOfficialAccountCipherRoundTrip 密文 round-trip：加密后可解回明文，且错误 AAD（服务商）解不开。
func TestOfficialAccountCipherRoundTrip(t *testing.T) {
	withOfficialKey(t, "test-key-entropy-123")
	plain := "sk-official-abc-" + nextOfficialName(t)
	enc, err := officialEncrypt(model.OfficialAccountProviderClaude, plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if strings.Contains(enc, plain) {
		t.Fatalf("ciphertext leaks plaintext")
	}
	got, err := officialDecrypt(model.OfficialAccountProviderClaude, enc)
	if err != nil || got != plain {
		t.Fatalf("decrypt: got %q err %v, want %q", got, err, plain)
	}
	// AAD 绑定服务商：换服务商解密必须失败，杜绝跨账号挪用密文。
	if _, err := officialDecrypt(model.OfficialAccountProviderOpenAI, enc); err == nil {
		t.Fatalf("decrypt with wrong provider AAD: want error")
	}
}

// TestOfficialAccountCallbackFullLoop 全链路：authorize → callback（换码+密文落库）→ usage 回填。
func TestOfficialAccountCallbackFullLoop(t *testing.T) {
	withOfficialKey(t, "test-key-entropy-123")
	conn := openOfficialFlowTestDB(t)
	ex := &stubExchanger{bundle: OfficialTokenBundle{AccessToken: "AT-" + nextOfficialName(t), RefreshToken: "RT", ExpiresIn: 3600, ExternalName: "ops@x.io"}}
	SetOfficialOAuthClientsForTest(ex, stubReader{snap: UsageSnapshot{PlanTier: "Pro", Window5H: "62%", Window7D: "80%", Healthy: true}})
	t.Cleanup(func() { SetOfficialOAuthClientsForTest(&httpTokenExchanger{}, &httpUsageReader{}) })

	account, _, state, err := OfficialAccountAuthorize(conn, model.OfficialAccountProviderClaude)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if account.Status != model.OfficialAccountStatusPending {
		t.Fatalf("authorize status = %q, want pending", account.Status)
	}
	active, err := OfficialAccountCallback(conn, account.ID, "one-time-code", state)
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if active.Status != model.OfficialAccountStatusActive {
		t.Fatalf("callback status = %q, want active", active.Status)
	}
	if active.ExternalName != "ops@x.io" {
		t.Fatalf("external name = %q, want ops@x.io", active.ExternalName)
	}
	if active.AccessCipher == "" || strings.Contains(active.AccessCipher, "AT-") {
		t.Fatalf("access cipher must be encrypted ciphertext, got %q", active.AccessCipher)
	}
	// one_time_code 绝不落库：回调成功后账号行任何字段不得含明文 code。
	if strings.Contains(active.AccessCipher, "one-time-code") || strings.Contains(active.LastError, "one-time-code") {
		t.Fatalf("one-time code leaked into account row")
	}
	used, err := OfficialAccountReadUsage(conn, account.ID)
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if used.PlanTier != "Pro" || used.Window5H != "62%" || !used.Healthy {
		t.Fatalf("usage snapshot mismatch: %+v", used)
	}
}

// TestOfficialAccountCallbackReplayCode 同一 state 第二次提交（重复 code）必须被 state 一次性语义拒绝。
func TestOfficialAccountCallbackReplayCode(t *testing.T) {
	withOfficialKey(t, "test-key-entropy-123")
	conn := openOfficialFlowTestDB(t)
	ex := &stubExchanger{bundle: OfficialTokenBundle{AccessToken: "AT-REPLAY"}}
	SetOfficialOAuthClientsForTest(ex, stubReader{})
	t.Cleanup(func() { SetOfficialOAuthClientsForTest(&httpTokenExchanger{}, &httpUsageReader{}) })

	account, _, state, err := OfficialAccountAuthorize(conn, model.OfficialAccountProviderGemini)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if active, err := OfficialAccountCallback(conn, account.ID, "code-1", state); err != nil {
		t.Fatalf("first callback with bound account id: %v", err)
	} else if active.Status != model.OfficialAccountStatusActive {
		t.Fatalf("first callback status = %q, want active", active.Status)
	}
	// 同一 state 重放（第二次提交 code）必须被一次性消费语义拒绝。
	if _, err := OfficialAccountCallback(conn, account.ID, "code-2", state); err != ErrOfficialStateUnknown {
		t.Fatalf("replayed state: err = %v, want ErrOfficialStateUnknown", err)
	}
}
