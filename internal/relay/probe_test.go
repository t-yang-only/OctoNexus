package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// R-probe-001 主动探活的单测: 探测走的是 op 读取链 (GroupList / ChannelGrantGet / ChannelGet),
// 故这里必须落真实实体到隔离内存库, 不能用内存构造的分组 —— 那正是"探测走生产路径"的验收点。
// 上游用 httptest 桩: 既能断言探测请求打到了哪个路径与模型, 又能按用例切换成功/失败。

var probeTestSeq int64

// openProbeTestDB 建隔离内存库、灌生产同款表清单并初始化缓存。
func openProbeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&probeTestSeq, 1)
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
	t.Cleanup(db.SetDBForTest(conn))
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	return conn
}

// seedProbeChannel 落库一条可转发渠道 (渠道 + 凭据 + 渠道模型 + 授权), 返回授权与渠道主键。
func seedProbeChannel(t *testing.T, conn *gorm.DB, name, baseURL string, protocols model.Protocol) (int, int) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: name, Enabled: true, BaseURL: baseURL}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk-probe-test", Enabled: true}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	channelModel := model.ChannelModel{ChannelID: channel.ID, Name: "probe-model"}
	if err := conn.Create(&channelModel).Error; err != nil {
		t.Fatalf("create channel model: %v", err)
	}
	grant := model.ChannelGrant{ChannelModelID: channelModel.ID, ChannelKeyID: key.ID, Protocols: protocols}
	if err := conn.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return grant.ID, channel.ID
}

// seedProbeGroup 落库一个引用该授权的分组, 返回分组 (含 ID) 与成员行主键。
func seedProbeGroup(t *testing.T, conn *gorm.DB, mode model.GroupMode, grantID int) (model.Group, int) {
	t.Helper()
	seq := atomic.AddInt64(&probeTestSeq, 1)
	group := model.Group{Name: fmt.Sprintf("probe-%d", seq), Mode: mode, RelayConfig: model.DefaultGroupRelayConfig()}
	if err := conn.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	ref := grantID
	item := model.GroupItem{GroupID: group.ID, ChannelGrantID: &ref, Priority: 1}
	if err := conn.Create(&item).Error; err != nil {
		t.Fatalf("create group item: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("reload cache: %v", err)
	}
	return group, item.ID
}

// seedProbeCooldown 直接写分组路由状态: 把成员置为"仍在冷却期内", 探活任务才有目标。
func seedProbeCooldown(group model.Group, itemID int, deadlineMs int64) {
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	route.Cooldowns[itemID] = deadlineMs
}

// probeCooldownDeadline 读回成员的冷却截止时间, 0 表示当前无冷却。
func probeCooldownDeadline(groupID, itemID int) int64 {
	routeMu.Lock()
	defer routeMu.Unlock()
	if route := routes[groupID]; route != nil {
		return route.Cooldowns[itemID]
	}
	return 0
}

// probeUpstream 是记录探测请求的上游桩: 记命中次数与最后一次请求的路径/正文, 响应按用例给出。
type probeUpstream struct {
	baseURL   string
	hits      int32
	status    int
	body      string
	lastPath  atomic.Value
	lastModel atomic.Value
	lastBody  atomic.Value
}

// newProbeUpstream 启动桩上游; status/body 决定它给探测返回什么。
func newProbeUpstream(t *testing.T, status int, body string) *probeUpstream {
	t.Helper()
	upstream := &probeUpstream{status: status, body: body}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&upstream.hits, 1)
		upstream.lastPath.Store(r.URL.Path)
		raw, _ := io.ReadAll(r.Body)
		upstream.lastBody.Store(string(raw))
		var payload map[string]any
		if err := json.Unmarshal(raw, &payload); err == nil {
			upstream.lastModel.Store(payload["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(upstream.status)
		_, _ = w.Write([]byte(upstream.body))
	}))
	t.Cleanup(server.Close)
	upstream.baseURL = server.URL
	return upstream
}

const probeChatOKBody = `{"id":"probe","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

// TestProbeCoolingMemberRecoversAndClearsCooldown 核心不变式: 冷却中的成员被真实探测一次,
// 上游确认可用即立刻解除冷却 (不必等冷却到期), 且探测打的是成员自己的模型与协议路径。
func TestProbeCoolingMemberRecoversAndClearsCooldown(t *testing.T) {
	conn := openProbeTestDB(t)
	upstream := newProbeUpstream(t, http.StatusOK, probeChatOKBody)
	grantID, _ := seedProbeChannel(t, conn, "probe-ok", upstream.baseURL, model.ProtocolOpenAIChatCompletion)
	group, itemID := seedProbeGroup(t, conn, model.GroupModeFailover, grantID)
	defer ResetRouteState(group.ID)

	seedProbeCooldown(group, itemID, time.Now().Add(time.Minute).UnixMilli())

	probed, recovered := ProbeCoolingMembers(context.Background())
	if probed != 1 || recovered != 1 {
		t.Fatalf("probed=%d recovered=%d, want 1/1", probed, recovered)
	}
	if hits := atomic.LoadInt32(&upstream.hits); hits != 1 {
		t.Fatalf("upstream hits=%d, want 1", hits)
	}
	if path := upstream.lastPath.Load(); path != "/v1/chat/completions" {
		t.Fatalf("probe path=%v, want /v1/chat/completions", path)
	}
	if got := upstream.lastModel.Load(); got != "probe-model" {
		t.Fatalf("probe model=%v, want probe-model (成员绑定的上游模型名)", got)
	}
	if body, _ := upstream.lastBody.Load().(string); !strings.Contains(body, `"max_tokens":1`) {
		t.Fatalf("probe body=%q, want max_tokens=1 (探测成本压到最低)", body)
	}
	if got := probeCooldownDeadline(group.ID, itemID); got != 0 {
		t.Fatalf("cooldown=%d after recovery, want cleared", got)
	}
}

// TestProbeFailureKeepsCooldown 探测失败只记日志: 冷却时长不变 (探测不是惩罚), 也不重试。
func TestProbeFailureKeepsCooldown(t *testing.T) {
	conn := openProbeTestDB(t)
	upstream := newProbeUpstream(t, http.StatusInternalServerError, `{"error":{"message":"upstream boom"}}`)
	grantID, _ := seedProbeChannel(t, conn, "probe-bad", upstream.baseURL, model.ProtocolOpenAIChatCompletion)
	group, itemID := seedProbeGroup(t, conn, model.GroupModeFailover, grantID)
	defer ResetRouteState(group.ID)

	deadline := time.Now().Add(time.Minute).UnixMilli()
	seedProbeCooldown(group, itemID, deadline)

	probed, recovered := ProbeCoolingMembers(context.Background())
	if probed != 1 || recovered != 0 {
		t.Fatalf("probed=%d recovered=%d, want 1/0", probed, recovered)
	}
	if hits := atomic.LoadInt32(&upstream.hits); hits != 1 {
		t.Fatalf("upstream hits=%d, want 1 (失败不重试)", hits)
	}
	if got := probeCooldownDeadline(group.ID, itemID); got != deadline {
		t.Fatalf("cooldown=%d, want unchanged %d", got, deadline)
	}
}

// TestProbeSkipsExpiredCooldownAndManualMode 两种不该探的情形:
// 冷却已到期的成员交给下一个真实请求当探测; 手动模式没有冷却概念。
func TestProbeSkipsExpiredCooldownAndManualMode(t *testing.T) {
	conn := openProbeTestDB(t)
	upstream := newProbeUpstream(t, http.StatusOK, probeChatOKBody)
	grantID, _ := seedProbeChannel(t, conn, "probe-skip", upstream.baseURL, model.ProtocolOpenAIChatCompletion)

	expired, expiredItem := seedProbeGroup(t, conn, model.GroupModeFailover, grantID)
	defer ResetRouteState(expired.ID)
	seedProbeCooldown(expired, expiredItem, time.Now().Add(-time.Minute).UnixMilli())

	manual, manualItem := seedProbeGroup(t, conn, model.GroupModeManual, grantID)
	defer ResetRouteState(manual.ID)
	// 手动模式恒无冷却, 这里硬写一条以证明探活侧确实跳过它。
	seedProbeCooldown(manual, manualItem, time.Now().Add(time.Minute).UnixMilli())

	probed, recovered := ProbeCoolingMembers(context.Background())
	if probed != 0 || recovered != 0 {
		t.Fatalf("probed=%d recovered=%d, want 0/0", probed, recovered)
	}
	if hits := atomic.LoadInt32(&upstream.hits); hits != 0 {
		t.Fatalf("upstream hits=%d, want 0", hits)
	}
}

// TestProbeUsesGrantSupportedProtocol 授权不支持 OpenAI Chat 时按既有兜底顺序选协议:
// 只给 Anthropic Message 的成员必须探 /v1/messages, 否则协议不匹配会被误判成成员不可用。
func TestProbeUsesGrantSupportedProtocol(t *testing.T) {
	conn := openProbeTestDB(t)
	upstream := newProbeUpstream(t, http.StatusOK, `{"id":"probe","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`)
	grantID, _ := seedProbeChannel(t, conn, "probe-msg", upstream.baseURL, model.ProtocolAnthropicMessage)
	group, itemID := seedProbeGroup(t, conn, model.GroupModeFailover, grantID)
	defer ResetRouteState(group.ID)

	seedProbeCooldown(group, itemID, time.Now().Add(time.Minute).UnixMilli())
	probed, recovered := ProbeCoolingMembers(context.Background())
	if probed != 1 || recovered != 1 {
		t.Fatalf("probed=%d recovered=%d, want 1/1", probed, recovered)
	}
	if path := upstream.lastPath.Load(); path != "/v1/messages" {
		t.Fatalf("probe path=%v, want /v1/messages (授权只支持 Anthropic Message)", path)
	}
}

// TestProbeErrorBodyOn200IsNotRecovery 部分上游把错误包在 200 正文的 error 字段里下发:
// 与真实转发同口径, 这种响应不算恢复, 冷却保持。
func TestProbeErrorBodyOn200IsNotRecovery(t *testing.T) {
	conn := openProbeTestDB(t)
	upstream := newProbeUpstream(t, http.StatusOK, `{"error":{"message":"quota exhausted","type":"insufficient_quota"}}`)
	grantID, _ := seedProbeChannel(t, conn, "probe-errbody", upstream.baseURL, model.ProtocolOpenAIChatCompletion)
	group, itemID := seedProbeGroup(t, conn, model.GroupModeFailover, grantID)
	defer ResetRouteState(group.ID)

	deadline := time.Now().Add(time.Minute).UnixMilli()
	seedProbeCooldown(group, itemID, deadline)

	probed, recovered := ProbeCoolingMembers(context.Background())
	if probed != 1 || recovered != 0 {
		t.Fatalf("probed=%d recovered=%d, want 1/0", probed, recovered)
	}
	if got := probeCooldownDeadline(group.ID, itemID); got != deadline {
		t.Fatalf("cooldown=%d, want unchanged %d", got, deadline)
	}
}

// TestProbeSkipsUnavailableMember 渠道/凭据被停用的成员不发探测: 探了也转发不了, 只是白花钱。
func TestProbeSkipsUnavailableMember(t *testing.T) {
	conn := openProbeTestDB(t)
	upstream := newProbeUpstream(t, http.StatusOK, probeChatOKBody)
	grantID, channelID := seedProbeChannel(t, conn, "probe-unavailable", upstream.baseURL, model.ProtocolOpenAIChatCompletion)
	group, itemID := seedProbeGroup(t, conn, model.GroupModeFailover, grantID)
	defer ResetRouteState(group.ID)

	// 停用凭据后成员即不可用 (available=false), 冷却条目仍在路由状态里。
	if err := conn.Model(&model.ChannelKey{}).Where("channel_id = ?", channelID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable key: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("reload cache: %v", err)
	}
	seedProbeCooldown(group, itemID, time.Now().Add(time.Minute).UnixMilli())

	probed, recovered := ProbeCoolingMembers(context.Background())
	if probed != 0 || recovered != 0 {
		t.Fatalf("probed=%d recovered=%d, want 0/0", probed, recovered)
	}
	if hits := atomic.LoadInt32(&upstream.hits); hits != 0 {
		t.Fatalf("upstream hits=%d, want 0 (不可用成员不探)", hits)
	}
}
