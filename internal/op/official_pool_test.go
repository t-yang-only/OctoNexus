package op

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-pool-002 号池同步测试：走生产同一套加解密与库表，只把"没有的东西"桩化——
// 官方 OAuth 换码/刷新端点用桩注入，账号行按密文口径直接种进内存库。

var officialPoolDBSeq int64

// openOfficialPoolTestDB 打开独立内存库并建出号池依赖的全部表（账号 + 渠道四件套）。
// 同步会刷渠道缓存，而缓存读的是全局库，故一并把全局库指到这块内存库上，测试收尾还原。
func openOfficialPoolTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&officialPoolDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open pool test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.OfficialAccount{},
		&model.Channel{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
	); err != nil {
		t.Fatalf("migrate pool test db: %v", err)
	}
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	return conn
}

func atomicAdd(counter *int64) int64 {
	return atomic.AddInt64(counter, 1)
}

// poolAccountSeed 是一条待种入的官方账号。
type poolAccountSeed struct {
	name    string
	status  model.OfficialAccountStatus
	access  string
	refresh string
	expires *time.Time
}

// seedPoolAccount 按生产密文口径种一条账号行；不解密的测试读不到明文，故返回原始明文供断言比对。
func seedPoolAccount(t *testing.T, conn *gorm.DB, provider model.OfficialAccountProvider, seed poolAccountSeed) model.OfficialAccount {
	t.Helper()
	accessCipher, err := officialEncrypt(provider, seed.access)
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	refreshCipher, err := officialEncrypt(provider, seed.refresh)
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	account := model.OfficialAccount{
		Provider:      provider,
		ExternalName:  seed.name,
		Status:        seed.status,
		AccessCipher:  accessCipher,
		RefreshCipher: refreshCipher,
		ExpiresAt:     seed.expires,
	}
	if err := conn.Create(&account).Error; err != nil {
		t.Fatalf("create official account: %v", err)
	}
	return account
}

// stubRefresher 记录调用参数的刷新桩。
type stubRefresher struct {
	bundle OfficialTokenBundle
	err    error
	calls  int
	seen   []string
}

func (s *stubRefresher) Refresh(provider model.OfficialAccountProvider, refreshToken string) (OfficialTokenBundle, error) {
	s.calls++
	s.seen = append(s.seen, refreshToken)
	if s.err != nil {
		return OfficialTokenBundle{}, s.err
	}
	return s.bundle, nil
}

// withPoolRefresher 注入刷新桩并在收尾还原原实现。
func withPoolRefresher(t *testing.T, stub *stubRefresher) *stubRefresher {
	t.Helper()
	original := officialRefresher
	SetOfficialTokenRefresherForTest(stub)
	t.Cleanup(func() { officialRefresher = original })
	return stub
}

func poolChannelOf(t *testing.T, conn *gorm.DB, provider model.OfficialAccountProvider) (model.Channel, bool) {
	t.Helper()
	var channel model.Channel
	err := conn.Where("name = ?", OfficialPoolChannelName(provider)).First(&channel).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return model.Channel{}, false
	}
	if err != nil {
		t.Fatalf("lookup pool channel: %v", err)
	}
	return channel, true
}

func poolKeysOf(t *testing.T, conn *gorm.DB, channelID int) map[string]model.ChannelKey {
	t.Helper()
	keys, err := officialPoolKeys(conn, channelID)
	if err != nil {
		t.Fatalf("list pool keys: %v", err)
	}
	byName := make(map[string]model.ChannelKey, len(keys))
	for _, key := range keys {
		byName[key.Name] = key
	}
	return byName
}

// TestOfficialPoolSyncCreatesChannelAndKeys 两账号 active：建号池渠道 + 一账号一条启用凭据，凭据即解密的 access token。
func TestOfficialPoolSyncCreatesChannelAndKeys(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-a")
	conn := openOfficialPoolTestDB(t)
	provider := model.OfficialAccountProviderOpenAI
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "a@example.com", status: model.OfficialAccountStatusActive, access: "tok-a"})
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "b@example.com", status: model.OfficialAccountStatusActive, access: "tok-b"})

	result, err := OfficialPoolSync(conn, provider)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.ChannelID == 0 {
		t.Fatalf("expected pool channel created, got result %+v", result)
	}
	if result.Keys != 2 {
		t.Fatalf("expected 2 enabled keys, got %d (notes=%v)", result.Keys, result.Notes)
	}
	channel, ok := poolChannelOf(t, conn, provider)
	if !ok {
		t.Fatal("pool channel not found by name")
	}
	if channel.BaseURL == "" || channel.OpenAIChatCompletionPath == "" {
		t.Fatalf("pool channel config incomplete: %+v", channel.ChannelConfig)
	}
	keys := poolKeysOf(t, conn, channel.ID)
	if got := keys["a@example.com"].Key; got != "tok-a" {
		t.Fatalf("key a material = %q, want decrypted token", got)
	}
	if got := keys["b@example.com"].Key; got != "tok-b" {
		t.Fatalf("key b material = %q, want decrypted token", got)
	}
	if !keys["a@example.com"].Enabled || !keys["b@example.com"].Enabled {
		t.Fatal("active accounts must be enabled")
	}
}

// TestOfficialPoolSyncIsIdempotent 重复同步不产生重复凭据，也不改动已是最新的凭据行。
func TestOfficialPoolSyncIsIdempotent(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-a")
	conn := openOfficialPoolTestDB(t)
	provider := model.OfficialAccountProviderGemini
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "g@example.com", status: model.OfficialAccountStatusActive, access: "tok-g"})

	if _, err := OfficialPoolSync(conn, provider); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	channel, _ := poolChannelOf(t, conn, provider)
	first := poolKeysOf(t, conn, channel.ID)["g@example.com"]

	second, err := OfficialPoolSync(conn, provider)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if second.Keys != 1 {
		t.Fatalf("expected 1 key after re-sync, got %d", second.Keys)
	}
	again := poolKeysOf(t, conn, channel.ID)["g@example.com"]
	if again.ID != first.ID || again.Key != first.Key {
		t.Fatalf("re-sync must keep the same key row: first=%+v again=%+v", first, again)
	}
}

// TestOfficialPoolSyncTracksAccountStatus 账号失活停用凭据、重新授权后同一条凭据被重新启用并换成新 token。
func TestOfficialPoolSyncTracksAccountStatus(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-a")
	conn := openOfficialPoolTestDB(t)
	provider := model.OfficialAccountProviderOpenAI
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "live@example.com", status: model.OfficialAccountStatusActive, access: "tok-live"})
	dead := seedPoolAccount(t, conn, provider, poolAccountSeed{name: "dead@example.com", status: model.OfficialAccountStatusActive, access: "tok-dead"})
	if _, err := OfficialPoolSync(conn, provider); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	channel, _ := poolChannelOf(t, conn, provider)

	if err := conn.Model(&model.OfficialAccount{}).Where("id = ?", dead.ID).
		Update("status", model.OfficialAccountStatusRevoked).Error; err != nil {
		t.Fatalf("revoke account: %v", err)
	}
	result, err := OfficialPoolSync(conn, provider)
	if err != nil {
		t.Fatalf("sync after revoke: %v", err)
	}
	if result.Disabled != 1 || result.Keys != 1 {
		t.Fatalf("expected 1 disabled and 1 enabled key, got %+v", result)
	}
	keys := poolKeysOf(t, conn, channel.ID)
	if keys["dead@example.com"].Enabled {
		t.Fatal("revoked account key must be disabled")
	}
	if !keys["live@example.com"].Enabled {
		t.Fatal("active account key must stay enabled")
	}

	// 重新授权：账号回 active 且 token 换了，同一条凭据行应被重新启用并写入新 token。
	if err := conn.Model(&model.OfficialAccount{}).Where("id = ?", dead.ID).
		Updates(map[string]any{"status": model.OfficialAccountStatusActive, "external_name": "dead@example.com"}).Error; err != nil {
		t.Fatalf("reactivate account: %v", err)
	}
	newCipher, err := officialEncrypt(provider, "tok-dead-new")
	if err != nil {
		t.Fatalf("encrypt new token: %v", err)
	}
	if err := conn.Model(&model.OfficialAccount{}).Where("id = ?", dead.ID).
		Update("access_cipher", newCipher).Error; err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	if _, err := OfficialPoolSync(conn, provider); err != nil {
		t.Fatalf("sync after reauthorize: %v", err)
	}
	keys = poolKeysOf(t, conn, channel.ID)
	if !keys["dead@example.com"].Enabled {
		t.Fatal("reauthorized account key must be enabled again")
	}
	if keys["dead@example.com"].Key != "tok-dead-new" {
		t.Fatalf("key material must follow the new token, got %q", keys["dead@example.com"].Key)
	}
	if keys["dead@example.com"].ID == 0 || keys["live@example.com"].ID == 0 || keys["dead@example.com"].ID == keys["live@example.com"].ID {
		t.Fatal("each account keeps its own key row")
	}
}

// TestOfficialPoolSyncRefreshesExpiringToken 临期凭据用刷新凭据换新 token，并把新凭据写进号池。
func TestOfficialPoolSyncRefreshesExpiringToken(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-a")
	conn := openOfficialPoolTestDB(t)
	provider := model.OfficialAccountProviderOpenAI
	expired := time.Now().Add(-time.Minute)
	account := seedPoolAccount(t, conn, provider, poolAccountSeed{
		name: "expiring@example.com", status: model.OfficialAccountStatusActive,
		access: "tok-old", refresh: "refresh-old", expires: &expired,
	})
	stub := withPoolRefresher(t, &stubRefresher{bundle: OfficialTokenBundle{
		AccessToken: "tok-new", RefreshToken: "refresh-new", ExpiresIn: 3600,
	}})

	result, err := OfficialPoolSync(conn, provider)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected exactly 1 refresh call, got %d", stub.calls)
	}
	if len(stub.seen) != 1 || stub.seen[0] != "refresh-old" {
		t.Fatalf("refresher must receive the decrypted refresh token, got %v", stub.seen)
	}
	if result.Refreshed != 1 {
		t.Fatalf("expected 1 refreshed account, got %+v", result)
	}
	channel, _ := poolChannelOf(t, conn, provider)
	if got := poolKeysOf(t, conn, channel.ID)["expiring@example.com"].Key; got != "tok-new" {
		t.Fatalf("pool key must carry refreshed token, got %q", got)
	}
	updated, err := officialAccountGetOn(conn, account.ID)
	if err != nil {
		t.Fatalf("reload account: %v", err)
	}
	if updated.ExpiresAt == nil || !updated.ExpiresAt.After(time.Now()) {
		t.Fatalf("refreshed account must carry a future expiry, got %v", updated.ExpiresAt)
	}
	if refreshed, err := officialDecrypt(provider, updated.AccessCipher); err != nil || refreshed != "tok-new" {
		t.Fatalf("persisted access cipher must decrypt to the refreshed token, got %q err=%v", refreshed, err)
	}

	// 余量充足时不再刷新：官方刷新本身会轮换凭据，能不换就不换。
	if _, err := OfficialPoolSync(conn, provider); err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("in-window account must not refresh again, calls=%d", stub.calls)
	}
}

// TestOfficialPoolSyncWithoutAccountsLeavesChannelAbsent 无账号时不建空号池渠道，只给结论。
func TestOfficialPoolSyncWithoutAccountsLeavesChannelAbsent(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-a")
	conn := openOfficialPoolTestDB(t)
	provider := model.OfficialAccountProviderClaude

	result, err := OfficialPoolSync(conn, provider)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if result.ChannelID != 0 {
		t.Fatalf("no account must not create a pool channel, got %+v", result)
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected an explanation note when nothing was synced")
	}
	if _, ok := poolChannelOf(t, conn, provider); ok {
		t.Fatal("pool channel must not exist")
	}
}

// TestOfficialPoolSyncKeepsExistingKeyWhenCipherKeyChanged 密文密钥对不上时只登记原因，绝不把既有可用凭据擦掉。
func TestOfficialPoolSyncKeepsExistingKeyWhenCipherKeyChanged(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-a")
	conn := openOfficialPoolTestDB(t)
	provider := model.OfficialAccountProviderOpenAI
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "ro@example.com", status: model.OfficialAccountStatusActive, access: "tok-ro"})
	if _, err := OfficialPoolSync(conn, provider); err != nil {
		t.Fatalf("initial sync: %v", err)
	}
	channel, _ := poolChannelOf(t, conn, provider)
	before := poolKeysOf(t, conn, channel.ID)["ro@example.com"]

	// 换掉加密密钥：账号密文解不开，同步应降级为"记原因 + 保留原凭据"。
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "pool-cipher-key-b")
	result, err := OfficialPoolSync(conn, provider)
	if err != nil {
		t.Fatalf("sync with rotated cipher key must not fail hard: %v", err)
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected a note explaining the undecryptable account")
	}
	after := poolKeysOf(t, conn, channel.ID)["ro@example.com"]
	if after.ID != before.ID || after.Key != before.Key || !after.Enabled {
		t.Fatalf("existing key must stay untouched: before=%+v after=%+v", before, after)
	}
}

// TestOfficialPoolStatusListReportsMapping 状态快照逐服务商给出账号数、启用凭据数与模型/授权数。
func TestOfficialPoolStatusListReportsMapping(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-a")
	conn := openOfficialPoolTestDB(t)
	provider := model.OfficialAccountProviderOpenAI
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "s1@example.com", status: model.OfficialAccountStatusActive, access: "tok-1"})
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "s2@example.com", status: model.OfficialAccountStatusExpired, access: "tok-2"})
	if _, err := OfficialPoolSync(conn, provider); err != nil {
		t.Fatalf("sync: %v", err)
	}

	statuses, err := OfficialPoolStatusList(conn)
	if err != nil {
		t.Fatalf("status list: %v", err)
	}
	if len(statuses) != 3 {
		t.Fatalf("expected one status per provider, got %d", len(statuses))
	}
	byProvider := map[model.OfficialAccountProvider]model.OfficialPoolStatus{}
	for _, status := range statuses {
		byProvider[status.Provider] = status
	}
	openai := byProvider[model.OfficialAccountProviderOpenAI]
	if openai.ChannelID == 0 || openai.Accounts != 2 || openai.ActiveKeys != 1 {
		t.Fatalf("unexpected openai pool status: %+v", openai)
	}
	if gemini := byProvider[model.OfficialAccountProviderGemini]; gemini.ChannelID != 0 || gemini.ActiveKeys != 0 {
		t.Fatalf("unused provider must report an empty pool: %+v", gemini)
	}
}

// TestOfficialPoolStatusListIncludesMemberDetail 统一号池视图逐账号给出映射明细：
// active 账号有启用凭据；曾 active 后失活的账号凭据行保留但停用；从未 active 的账号根本没有凭据行；
// 未建号池渠道的服务商照旧出行（凭据标记为不存在），界面据此区分"有账号没同步"与"没账号"。
func TestOfficialPoolStatusListIncludesMemberDetail(t *testing.T) {
	withOfficialKey(t, "pool-cipher-key-a")
	conn := openOfficialPoolTestDB(t)
	provider := model.OfficialAccountProviderGemini
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "g1@example.com", status: model.OfficialAccountStatusActive, access: "tok-1"})
	seedPoolAccount(t, conn, provider, poolAccountSeed{name: "g2@example.com", status: model.OfficialAccountStatusActive, access: "tok-2"})
	if _, err := OfficialPoolSync(conn, provider); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// g2 在官方侧被撤销后重新同步：凭据行必须保留（只是停用），历史映射不丢。
	if err := conn.Model(&model.OfficialAccount{}).
		Where("provider = ? AND external_name = ?", provider, "g2@example.com").
		Update("status", model.OfficialAccountStatusRevoked).Error; err != nil {
		t.Fatalf("revoke g2: %v", err)
	}
	if _, err := OfficialPoolSync(conn, provider); err != nil {
		t.Fatalf("resync: %v", err)
	}
	// 另一个服务商：只有账号、从未同步过（pending）。
	seedPoolAccount(t, conn, model.OfficialAccountProviderOpenAI, poolAccountSeed{name: "o1@example.com", status: model.OfficialAccountStatusPending, access: "tok-3"})

	statuses, err := OfficialPoolStatusList(conn)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	byProvider := make(map[model.OfficialAccountProvider]model.OfficialPoolStatus, len(statuses))
	for _, status := range statuses {
		byProvider[status.Provider] = status
	}

	gemini := byProvider[model.OfficialAccountProviderGemini]
	if gemini.ChannelID == 0 || len(gemini.Members) != 2 {
		t.Fatalf("gemini status = %+v, want a channel with 2 members", gemini)
	}
	first, second := gemini.Members[0], gemini.Members[1]
	if first.ExternalName != "g1@example.com" || !first.KeyExists || !first.KeyEnabled {
		t.Fatalf("active member = %+v, want g1 mapped to an enabled key", first)
	}
	if first.KeyName != "g1@example.com" {
		t.Fatalf("key name = %q, want account external name", first.KeyName)
	}
	if second.ExternalName != "g2@example.com" || !second.KeyExists || second.KeyEnabled {
		t.Fatalf("revoked member = %+v, want g2 mapped to a disabled-but-kept key", second)
	}
	if second.Status != model.OfficialAccountStatusRevoked || second.KeyName != "g2@example.com" {
		t.Fatalf("revoked member = %+v, want status revoked with key name kept", second)
	}
	if gemini.ActiveKeys != 1 {
		t.Fatalf("gemini active keys = %d, want 1", gemini.ActiveKeys)
	}

	openai := byProvider[model.OfficialAccountProviderOpenAI]
	if openai.ChannelID != 0 {
		t.Fatalf("openai channel id = %d, want 0 (never synced)", openai.ChannelID)
	}
	if len(openai.Members) != 1 || openai.Members[0].KeyExists {
		t.Fatalf("openai members = %+v, want one member with no key yet", openai.Members)
	}
	if openai.Members[0].Status != model.OfficialAccountStatusPending {
		t.Fatalf("openai member status = %q", openai.Members[0].Status)
	}
}
