package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/health"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-pool-ext-009 渠道凭据适配器：把 octopus 自己的渠道凭据投影成号池条目。
//
// 这一层不引入新表、不碰选路，所以单测只需要一个内存库 + 一个站点桩：
// 库里的渠道/凭据是"生产同一套结构"，站点桩用来把 Probe/Refresh 的真实 HTTP 口径钉住。

var stationTestDBCounter int64

func openStationTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:station-%s-%d?mode=memory&cache=shared", t.Name(), atomic.AddInt64(&stationTestDBCounter, 1))
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open station test db: %v", err)
	}
	// ChannelGrant 也要建：toggle 走 op.SetChannelKeyEnabled，它会顺手刷新渠道缓存（缓存会读授权）。
	if err := conn.AutoMigrate(
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{},
	); err != nil {
		t.Fatalf("migrate station test db: %v", err)
	}
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	return conn
}

func seedStationChannel(t *testing.T, conn *gorm.DB, name, baseURL string, keys ...model.ChannelKey) model.Channel {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: name, BaseURL: baseURL, Enabled: true}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel %s: %v", name, err)
	}
	for index := range keys {
		keys[index].ChannelID = channel.ID
		if err := conn.Create(&keys[index]).Error; err != nil {
			t.Fatalf("create key %s: %v", keys[index].Name, err)
		}
	}
	channel.Keys = keys
	return channel
}

func stationKey(name, credential string, enabled bool) model.ChannelKey {
	return model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Name: name, Key: credential, Enabled: enabled}}
}

// serveUserSelf 起一个 New API 系桩站点：/api/user/self 回额度 JSON，其余路径 404。
func serveUserSelf(t *testing.T, body string, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		if r.URL.Path != health.BalanceUserSelfPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status == http.StatusOK {
			_, _ = w.Write([]byte(body))
		}
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

func TestStationAdapterListsChannelCredentials(t *testing.T) {
	conn := openStationTestDB(t)
	alpha := seedStationChannel(t, conn, "Alpha站", "https://alpha.example.com/v1",
		stationKey("k1", "sk-alpha-1", true),
		stationKey("k2", "sk-alpha-2", false),
	)
	// 官方账号池自动物化的渠道必须被排除：同一条凭据不能有两个身份。
	official := seedStationChannel(t, conn,
		op.OfficialPoolChannelName(model.OfficialAccountProviderOpenAI), "https://official.example.com",
		stationKey("acct-1", "sk-official", true))

	entries, err := stationAdapter{}.Entries(context.Background())
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d 条，期望 2 条（官方账号池渠道应被排除）: %+v", len(entries), entries)
	}
	byID := map[string]Entry{}
	for _, entry := range entries {
		byID[entry.ID] = entry
	}
	active, ok := byID[stationEntryID(alpha.ID, "k1")]
	if !ok {
		t.Fatalf("缺条目 %s：%+v", stationEntryID(alpha.ID, "k1"), entries)
	}
	if active.Provider != "Alpha站" || active.Name != "Alpha站 / k1" {
		t.Errorf("条目名称/提供方不对：%q / %q", active.Provider, active.Name)
	}
	if active.Status != "active" || !active.Enabled || !active.Healthy {
		t.Errorf("启用中的凭据应为 active/Enabled/Healthy：%+v", active)
	}
	if got := active.Detail["family"]; got != stationFamilyOpenAICompatible {
		t.Errorf("base_url 以 /v1 结尾应判为 openai-compatible，实际 %v", got)
	}
	if got := active.Detail["probe_kind"]; got != string(health.ProbeKindOpenAIKey) {
		t.Errorf("openai-compatible 的探活口径应为 openai_key，实际 %v", got)
	}
	if got := active.Labels["base_url"]; got != "https://alpha.example.com/v1" {
		t.Errorf("labels 缺 base_url：%v", active.Labels)
	}

	disabled := byID[stationEntryID(alpha.ID, "k2")]
	if disabled.Enabled || disabled.Healthy || disabled.Status != "disabled" {
		t.Errorf("停用凭据应为 disabled/Enabled=false/Healthy=false：%+v", disabled)
	}
	if _, leaked := byID[stationEntryID(official.ID, "acct-1")]; leaked {
		t.Errorf("官方账号池渠道不应出现在渠道凭据后端里：%+v", entries)
	}
}

func TestStationAdapterNeverExposesCredential(t *testing.T) {
	conn := openStationTestDB(t)
	secret := "sk-station-secret-7f3a"
	channel := seedStationChannel(t, conn, "Secret站", "https://secret.example.com", stationKey("k", secret, true))

	entries, err := stationAdapter{}.Entries(context.Background())
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	rendered := fmt.Sprintf("%v", entries)
	if strings.Contains(rendered, secret) {
		t.Fatalf("统一视图里出现了凭据明文：%s", rendered)
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal entries: %v", err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("条目 JSON 里出现了凭据明文：%s", encoded)
	}

	entry, err := stationAdapter{}.Get(context.Background(), stationEntryID(channel.ID, "k"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if single := fmt.Sprintf("%v", entry); strings.Contains(single, secret) {
		t.Fatalf("单条详情里出现了凭据明文：%s", single)
	}
}

func TestStationAdapterRejectsUnknownIDs(t *testing.T) {
	conn := openStationTestDB(t)
	channel := seedStationChannel(t, conn, "Alpha站", "https://alpha.example.com", stationKey("k1", "sk-1", true))
	official := seedStationChannel(t, conn,
		op.OfficialPoolChannelName(model.OfficialAccountProviderClaude), "https://official.example.com",
		stationKey("acct", "sk-official", true))

	adapter := stationAdapter{}
	for _, id := range []string{
		"", "no-colon", "abc:k1", "0:k1",
		"99999:k1",
		stationEntryID(channel.ID, "missing"),
		stationEntryID(official.ID, "acct"), // 官方号池渠道按账号口径列示
	} {
		if _, err := adapter.Get(context.Background(), id); !errors.Is(err, ErrEntryNotFound) {
			t.Errorf("Get(%q) 应为 ErrEntryNotFound，实际 %v", id, err)
		}
		if _, err := adapter.SetEnabled(context.Background(), id, false); !errors.Is(err, ErrEntryNotFound) {
			t.Errorf("SetEnabled(%q) 应为 ErrEntryNotFound，实际 %v", id, err)
		}
	}
}

func TestStationAdapterProbeUsesReadOnlySiteEndpoint(t *testing.T) {
	conn := openStationTestDB(t)
	server, seen := serveUserSelf(t, `{"success":true,"data":{"quota":1000,"used":250}}`, http.StatusOK)
	channel := seedStationChannel(t, conn, "Probe站", server.URL, stationKey("k", "sk-probe", true))
	adapter := stationAdapter{}
	id := stationEntryID(channel.ID, "k")

	entry, err := adapter.Probe(context.Background(), id)
	if err != nil {
		t.Fatalf("探活应成功：%v", err)
	}
	if !entry.Healthy {
		t.Errorf("桩站点回了 200 JSON，应判健康：%+v", entry)
	}
	if got := entry.Detail["probe_kind"]; got != string(health.ProbeKindNAToken) {
		t.Errorf("非 /v1、非 anthropic 的站点应按 new-api 口径探活，实际 %v", got)
	}
	if got := entry.Detail["probe_status_code"]; got != http.StatusOK {
		t.Errorf("应记录站点状态码 200，实际 %v", got)
	}
	if len(*seen) != 1 {
		t.Fatalf("桩站点应收到 1 次请求，实际 %d 次：%v", len(*seen), *seen)
	}
	wantCall := "GET " + health.BalanceUserSelfPath + " auth=Bearer sk-probe"
	if (*seen)[0] != wantCall {
		t.Errorf("探活请求口径不对：\n got %s\nwant %s", (*seen)[0], wantCall)
	}

	// 站点判不健康时：条目照回（带当前快照），错误单独给出来。
	badServer, badSeen := serveUserSelf(t, "", http.StatusUnauthorized)
	badChannel := seedStationChannel(t, conn, "Bad站", badServer.URL, stationKey("k", "sk-bad", true))
	badEntry, err := adapter.Probe(context.Background(), stationEntryID(badChannel.ID, "k"))
	if err == nil {
		t.Fatalf("401 应判不健康并给出错误")
	}
	if badEntry.Healthy {
		t.Errorf("401 的条目不应是健康的：%+v", badEntry)
	}
	if badEntry.LastError == "" {
		t.Errorf("不健康时应带上原因：%+v", badEntry)
	}
	if len(*badSeen) != 1 || (*badSeen)[0] != "GET "+health.BalanceUserSelfPath+" auth=Bearer sk-bad" {
		t.Errorf("失败路径也应打到同一个只读端点：%v", *badSeen)
	}
}

func TestStationAdapterRefreshRecordsBalance(t *testing.T) {
	conn := openStationTestDB(t)
	server, _ := serveUserSelf(t, `{"success":true,"data":{"quota":500,"used":120}}`, http.StatusOK)
	channel := seedStationChannel(t, conn, "Balance站", server.URL, stationKey("k", "sk-balance", true))
	adapter := stationAdapter{}
	id := stationEntryID(channel.ID, "k")

	entry, err := adapter.Refresh(context.Background(), id)
	if err != nil {
		t.Fatalf("刷余额应成功：%v", err)
	}
	if got := entry.Detail["balance_remaining"]; got != float64(380) {
		t.Errorf("balance_remaining 应为 380（500-120），实际 %v", got)
	}
	// 余额要落进既有的渠道余额快照：加权选路的 balance 维度读的就是它。
	if remaining, ok := op.ChannelBalance(channel.ID); !ok || remaining != 380 {
		t.Errorf("渠道余额快照应为 380，实际 %v / %v", remaining, ok)
	}

	// 站点不回可解析余额时：干净失败，条目照回。
	badServer, _ := serveUserSelf(t, "", http.StatusNotFound)
	badChannel := seedStationChannel(t, conn, "NoBalance站", badServer.URL, stationKey("k", "sk-nobalance", true))
	badEntry, err := adapter.Refresh(context.Background(), stationEntryID(badChannel.ID, "k"))
	if err == nil {
		t.Fatalf("站点 404 时应报错")
	}
	if badEntry.LastError == "" {
		t.Errorf("失败时应带上原因：%+v", badEntry)
	}
	if _, ok := op.ChannelBalance(badChannel.ID); ok {
		t.Errorf("失败的一次不应写入余额快照")
	}
}

func TestStationAdapterSetEnabledKeepsOperatorDecision(t *testing.T) {
	conn := openStationTestDB(t)
	channel := seedStationChannel(t, conn, "Toggle站", "https://toggle.example.com", stationKey("k", "sk-toggle", true))
	adapter := stationAdapter{}
	id := stationEntryID(channel.ID, "k")

	entry, err := adapter.SetEnabled(context.Background(), id, false)
	if err != nil {
		t.Fatalf("停用应成功：%v", err)
	}
	if entry.Enabled || entry.Status != "disabled-by-operator" {
		t.Errorf("停用后应显示人工停用：%+v", entry)
	}
	var key model.ChannelKey
	if err := conn.Where("channel_id = ? AND name = ?", channel.ID, "k").First(&key).Error; err != nil {
		t.Fatalf("读回凭据: %v", err)
	}
	if key.Enabled || !key.OperatorDisabled {
		t.Errorf("停用应同时写下人工决定：enabled=%v operator_disabled=%v", key.Enabled, key.OperatorDisabled)
	}

	entry, err = adapter.SetEnabled(context.Background(), id, true)
	if err != nil {
		t.Fatalf("启用应成功：%v", err)
	}
	if !entry.Enabled || entry.Status != "active" {
		t.Errorf("启用后应回到 active：%+v", entry)
	}
	if err := conn.Where("channel_id = ? AND name = ?", channel.ID, "k").First(&key).Error; err != nil {
		t.Fatalf("读回凭据: %v", err)
	}
	if !key.Enabled || key.OperatorDisabled {
		t.Errorf("启用应同时清掉人工停用：enabled=%v operator_disabled=%v", key.Enabled, key.OperatorDisabled)
	}
}

// 渠道整体停用时，凭据不该被显示成"可用"：状态要能区分这两种停用。
func TestStationAdapterDistinguishesChannelDisabled(t *testing.T) {
	conn := openStationTestDB(t)
	channel := seedStationChannel(t, conn, "Off站", "https://off.example.com", stationKey("k", "sk-off", true))
	if err := conn.Model(&model.Channel{}).Where("id = ?", channel.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("停用渠道: %v", err)
	}

	entry, err := stationAdapter{}.Get(context.Background(), stationEntryID(channel.ID, "k"))
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if entry.Status != "channel-disabled" {
		t.Errorf("渠道停用时状态应为 channel-disabled，实际 %q", entry.Status)
	}
	if entry.Healthy {
		t.Errorf("渠道停用时不应判为健康：%+v", entry)
	}
}
