package task

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-proxy-002 通知状态机：集合不变必须安静、集合变化必须发。
//
// 上面的纯函数测试只覆盖了指纹计算；真正会出 bug 的是"比对 + 落状态"这段状态机
// （例如先写状态再比对，就会永远认为没变化）。这里用真实库 + 捕获桩把它测住：
//   - 首次发现坏渠道 → 发一条；
//   - 同一集合再来一轮 → 不发（去重的核心）；
//   - 集合变化 → 发；
//   - 全部恢复 → 发一条恢复通知；
//   - 恢复后继续保持正常 → 不发。

type notifyCapture struct {
	mu       sync.Mutex
	payloads [][]byte
	server   *httptest.Server
}

func newNotifyCapture(t *testing.T) *notifyCapture {
	t.Helper()
	c := &notifyCapture{}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(raw)
		}
		c.mu.Lock()
		c.payloads = append(c.payloads, raw)
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(c.server.Close)
	return c
}

func (c *notifyCapture) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.payloads)
}

func (c *notifyCapture) types() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.payloads))
	for _, raw := range c.payloads {
		var event notify.Event
		if err := json.Unmarshal(raw, &event); err == nil {
			out = append(out, event.Type)
		}
	}
	return out
}

// openProxyNotifyTestDB 建隔离内存库，含渠道/节点/设置三张表并灌入设置缓存。
func openProxyNotifyTestDB(t *testing.T, webhookURL string) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:proxynotify-%s?mode=memory&cache=shared", t.Name())
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{},
		&model.Group{}, &model.GroupItem{}, &model.APIKey{}, &model.Setting{}, &model.LLMInfo{},
		&model.StatsTotal{}, &model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{},
		&model.QuotaAction{}, &model.ProxyNode{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))

	settings := []model.Setting{
		{Key: model.SettingKeyAlertChannels, Value: "webhook"},
		{Key: model.SettingKeyAlertWebhookURL, Value: webhookURL},
	}
	for _, s := range settings {
		if err := conn.Create(&s).Error; err != nil {
			t.Fatalf("seed setting %s: %v", s.Key, err)
		}
	}
	// 必须刷新设置缓存：op.SettingGetString 读的是缓存，只写库不刷缓存
	// 会让"渠道不生效"看起来像通知逻辑坏了（实测踩到）。
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	// notify 的设置来源是由装配层注入的函数指针（避免 notify→op 的导入环）。
	// 生产由 server 启动时注入；测试里不注入的话它会静默返回空设置，
	// 表现成"通知渠道明明配了却什么都没发"（实测踩到）。
	notify.SetSettingSource(op.SettingGetString)
	return conn
}

// seedBlockedChannel 落一条"启用渠道 + 启用但探活不通的节点"。
// 与其他装置一致用原生 INSERT：见下面 IgnoreDisabled 里对零值陷阱的说明。
func seedBlockedChannel(t *testing.T, conn *gorm.DB, channelID, nodeID int) {
	t.Helper()
	if err := conn.Exec(`INSERT INTO proxy_nodes (id, name, type, server, port, enabled, last_probe_ok, last_error)
		VALUES (?, ?, 'ss', '198.51.100.1', 443, 1, 0, 'EOF')`, nodeID, fmt.Sprintf("node-%d", nodeID)).Error; err != nil {
		t.Fatalf("seed node %d: %v", nodeID, err)
	}
	if err := conn.Exec(`INSERT INTO channels (id, name, enabled, proxy_node_id) VALUES (?, ?, 1, ?)`,
		channelID, fmt.Sprintf("ch-%d", channelID), nodeID).Error; err != nil {
		t.Fatalf("seed channel %d: %v", channelID, err)
	}
}

// resetProxyBlockState 让每个用例从"上一次是正常"开始（包级状态跨用例共享）。
func resetProxyBlockState() {
	proxyBlockMu.Lock()
	proxyBlockState = ""
	proxyBlockMu.Unlock()
}

// 首次发现坏渠道必须发一条；同一集合再来一轮必须安静。
//
// 这是去重的核心判据：不安静就会变成每 5 分钟一条骚扰。
func TestProxyBlockedNotifySendsOnceThenStaysQuiet(t *testing.T) {
	capture := newNotifyCapture(t)
	conn := openProxyNotifyTestDB(t, capture.server.URL)
	seedBlockedChannel(t, conn, 1, 10)
	resetProxyBlockState()

	proxyBlockedChannelNotify()
	if got := capture.count(); got != 1 {
		t.Fatalf("首次发现坏渠道应发 1 条，实得 %d 条 %v", got, capture.types())
	}

	// 同样的集合再来两轮：必须一条都不发。
	proxyBlockedChannelNotify()
	proxyBlockedChannelNotify()
	if got := capture.count(); got != 1 {
		t.Fatalf("集合未变化时不该重复通知，实得累计 %d 条 %v", got, capture.types())
	}
}

// 集合变化（多一个坏渠道）必须再发一条。
func TestProxyBlockedNotifySendsOnChange(t *testing.T) {
	capture := newNotifyCapture(t)
	conn := openProxyNotifyTestDB(t, capture.server.URL)
	seedBlockedChannel(t, conn, 1, 10)
	resetProxyBlockState()

	proxyBlockedChannelNotify()
	if capture.count() != 1 {
		t.Fatalf("前置：应已发 1 条")
	}

	seedBlockedChannel(t, conn, 2, 20)
	proxyBlockedChannelNotify()
	if got := capture.count(); got != 2 {
		t.Fatalf("集合变大应再发一条，实得累计 %d 条 %v", got, capture.types())
	}
}

// 全部恢复要发一条恢复通知；之后继续保持正常则安静。
func TestProxyBlockedNotifySendsRecoverySoUserKnowsItEnded(t *testing.T) {
	capture := newNotifyCapture(t)
	conn := openProxyNotifyTestDB(t, capture.server.URL)
	seedBlockedChannel(t, conn, 1, 10)
	resetProxyBlockState()

	proxyBlockedChannelNotify()
	if capture.count() != 1 {
		t.Fatalf("前置：应已发 1 条问题通知")
	}

	// 把节点改成探活通过 → 集合清空。
	if err := conn.Model(&model.ProxyNode{}).Where("id = ?", 10).
		Update("last_probe_ok", true).Error; err != nil {
		t.Fatalf("更新节点: %v", err)
	}
	proxyBlockedChannelNotify()
	if got := capture.count(); got != 2 {
		t.Fatalf("恢复应发一条，实得累计 %d 条 %v", got, capture.types())
	}
	types := capture.types()
	if types[len(types)-1] != "proxy_node_recovered" {
		t.Fatalf("最后一条应是恢复通知，实得 %v", types)
	}

	// 继续保持正常：不该再发。
	proxyBlockedChannelNotify()
	if got := capture.count(); got != 2 {
		t.Fatalf("保持正常时不该重复发恢复通知，实得累计 %d 条 %v", got, capture.types())
	}
}

// 一直正常时不该发任何通知（否则每轮都会报"恢复了"）。
func TestProxyBlockedNotifySilentWhenNeverBroken(t *testing.T) {
	capture := newNotifyCapture(t)
	_ = openProxyNotifyTestDB(t, capture.server.URL)
	resetProxyBlockState()

	for i := 0; i < 3; i++ {
		proxyBlockedChannelNotify()
	}
	if got := capture.count(); got != 0 {
		t.Fatalf("从未出问题时应保持安静，实得 %d 条 %v", got, capture.types())
	}
}

// 只报"启用渠道 + 启用节点"的组合：停用的渠道或节点不算受影响。
func TestProxyBlockedNotifyIgnoresDisabled(t *testing.T) {
	capture := newNotifyCapture(t)
	conn := openProxyNotifyTestDB(t, capture.server.URL)
	resetProxyBlockState()

	// 停用渠道 + 坏节点 → 不算。
	//
	// 装置一律用原生 INSERT：Channel.Enabled=false 与 ProxyNode.Enabled=false 都是零值，
	// GORM 的 Create 默认跳过零值字段（不写库 → 落成表默认值），而嵌进 ChannelConfig
	// 的字段用 Select 也摆不平。装置要表达的是"库里这一行长什么样"，
	// 用 SQL 直写最不容易失真（实测被这个坑绕了两轮）。
	if err := conn.Exec(`INSERT INTO proxy_nodes (id, name, type, server, port, enabled, last_probe_ok, last_error)
		VALUES (30, 'n30', 'ss', '198.51.100.3', 443, 1, 0, 'EOF')`).Error; err != nil {
		t.Fatalf("seed node 30: %v", err)
	}
	if err := conn.Exec(`INSERT INTO channels (id, name, enabled, proxy_node_id) VALUES (30, 'ch30', 0, 30)`).Error; err != nil {
		t.Fatalf("seed channel 30: %v", err)
	}
	// 启用渠道 + 停用节点 → 不算（用户已明确停用该节点）
	if err := conn.Exec(`INSERT INTO proxy_nodes (id, name, type, server, port, enabled, last_probe_ok, last_error)
		VALUES (31, 'n31', 'ss', '198.51.100.4', 443, 0, 0, 'EOF')`).Error; err != nil {
		t.Fatalf("seed node 31: %v", err)
	}
	if err := conn.Exec(`INSERT INTO channels (id, name, enabled, proxy_node_id) VALUES (31, 'ch31', 1, 31)`).Error; err != nil {
		t.Fatalf("seed channel 31: %v", err)
	}
	// 启用渠道但没有绑节点 → 不算（走直连）
	if err := conn.Exec(`INSERT INTO channels (id, name, enabled, proxy_node_id) VALUES (32, 'ch32', 1, 0)`).Error; err != nil {
		t.Fatalf("seed channel 32: %v", err)
	}

	proxyBlockedChannelNotify()
	if got := capture.count(); got != 0 {
		t.Fatalf("不该把停用渠道/停用节点/未绑节点算作受影响，实得 %d 条", got)
	}

	// 再加一个真正受影响的，确认筛选没把该报的也漏掉。
	seedBlockedChannel(t, conn, 33, 33)
	proxyBlockedChannelNotify()
	if got := capture.count(); got != 1 {
		t.Fatalf("真正受影响的应被报出，实得 %d 条", got)
	}
}
