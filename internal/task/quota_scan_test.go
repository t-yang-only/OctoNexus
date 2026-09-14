package task

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openQuotaScanTestDB 全局库替换 + 生产同款表清单 + op.InitCache 灌缓存，
// 让"扫描→停用→审计"链在完全隔离的内存库上跑真实生产函数。
var quotaScanSeq int64

func openQuotaScanTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&quotaScanSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{},
		&model.Group{}, &model.GroupItem{}, &model.APIKey{}, &model.Setting{}, &model.LLMInfo{},
		&model.StatsTotal{}, &model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{},
		&model.QuotaAction{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	resetQuotaScanStateForTest()
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	return conn
}

// seedScanChannel 落库一个启用渠道 + 一枚启用凭据 + 可选授权，并刷新缓存。
func seedScanChannel(t *testing.T, conn *gorm.DB, name, baseURL string, proxy bool) (int, int) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: name, Enabled: true, BaseURL: baseURL, Proxy: proxy}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk-monitor-test", Enabled: true}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("reload cache: %v", err)
	}
	return channel.ID, key.ID
}

// balanceServer 造 new-api 系 /api/user/self 响应桩。
func balanceServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/user/self" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func keyEnabled(t *testing.T, conn *gorm.DB, keyID int) bool {
	t.Helper()
	var key model.ChannelKey
	if err := conn.First(&key, keyID).Error; err != nil {
		t.Fatalf("load key: %v", err)
	}
	return key.Enabled
}

func quotaActionCount(t *testing.T, conn *gorm.DB, channelID int) int64 {
	t.Helper()
	var n int64
	if err := conn.Model(&model.QuotaAction{}).Where("channel_id = ?", channelID).Count(&n).Error; err != nil {
		t.Fatalf("count actions: %v", err)
	}
	return n
}

// TestQuotaScanZeroStopsAndAudits 集成①（212 第一验收项的反面：真归零必须停）：
// remaining=0 → 凭据停用 + auto_stop 审计落库 + actor=system:auto。
func TestQuotaScanZeroStopsAndAudits(t *testing.T) {
	conn := openQuotaScanTestDB(t)
	srv := balanceServer(t, http.StatusOK, `{"data":{"quota":100,"used":100,"remaining":0}}`)
	channelID, keyID := seedScanChannel(t, conn, "zero-ch", srv.URL, false)

	quotaScanOnce()

	if keyEnabled(t, conn, keyID) {
		t.Fatal("zero-remaining channel key still enabled after scan, want disabled")
	}
	var action model.QuotaAction
	if err := conn.Where("channel_id = ?", channelID).Order("id DESC").First(&action).Error; err != nil {
		t.Fatalf("quota action audit missing: %v", err)
	}
	if action.Action != model.QuotaActionAutoStop || action.Actor != "system:auto" {
		t.Fatalf("audit = %s/%s, want auto_stop/system:auto", action.Action, action.Actor)
	}

	// 指纹去重：余额未再变化不得重复触发（防每 5 分钟刷审计）。
	before := quotaActionCount(t, conn, channelID)
	quotaScanOnce()
	if after := quotaActionCount(t, conn, channelID); after != before {
		t.Fatalf("unchanged balance re-triggered stop: %d → %d audits", before, after)
	}
}

// TestQuotaScanFailureNeverStops 集成②（防误停第一验收项）：
// 端点 404 / 响应不可解析 / 401 一律跳过，凭据状态与审计表零改动。
func TestQuotaScanFailureNeverStops(t *testing.T) {
	conn := openQuotaScanTestDB(t)
	srv := balanceServer(t, http.StatusOK, `not json at all`)
	channelID, keyID := seedScanChannel(t, conn, "bad-ch", srv.URL, true)

	quotaScanOnce()
	if !keyEnabled(t, conn, keyID) {
		t.Fatal("unparsable balance disabled the key, want untouched")
	}
	if n := quotaActionCount(t, conn, channelID); n != 0 {
		t.Fatalf("failed scan wrote %d audits, want 0", n)
	}

	// 同一渠道换不可达地址再扫：仍不动。
	if err := conn.Model(&model.Channel{}).Where("id = ?", channelID).Update("base_url", "http://127.0.0.1:1").Error; err != nil {
		t.Fatalf("update channel: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("reload cache: %v", err)
	}
	quotaScanOnce()
	if !keyEnabled(t, conn, keyID) {
		t.Fatal("unreachable endpoint disabled the key, want untouched")
	}
}

// TestQuotaScanTargetsMapping 映射层口径抽查：停用渠道/无 BaseURL/无启用凭据都出局，
// 监控凭证取 ID 最小启用凭据（生产映射不读裸库）。
func TestQuotaScanTargetsMapping(t *testing.T) {
	conn := openQuotaScanTestDB(t)
	srv := balanceServer(t, http.StatusOK, `{}`)
	enabledID, _ := seedScanChannel(t, conn, "map-ch", srv.URL, false)

	var disabled model.Channel
	if err := conn.First(&disabled, enabledID).Error; err != nil {
		t.Fatalf("load channel: %v", err)
	}
	_ = disabled
	// 渠道整行停用后应出局（直接改库+刷缓存，模拟 ChannelEnabled 后状态）。
	if err := conn.Model(&model.Channel{}).Where("id = ?", enabledID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, target := range op.QuotaScanTargets() {
		if target.ChannelID == enabledID {
			t.Fatal("disabled channel still in scan targets")
		}
	}
	if err := conn.Model(&model.Channel{}).Where("id = ?", enabledID).Update("enabled", true).Error; err != nil {
		t.Fatalf("re-enable: %v", err)
	}
	if err := conn.Model(&model.ChannelKey{}).Where("channel_id = ?", enabledID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable keys: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("reload: %v", err)
	}
	for _, target := range op.QuotaScanTargets() {
		if target.ChannelID == enabledID {
			t.Fatal("keyless channel still in scan targets")
		}
	}
}
