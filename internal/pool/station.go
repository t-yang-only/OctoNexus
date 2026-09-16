package pool

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/health"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"gorm.io/gorm"
)

// stationKind 是「渠道凭据」这种号池后端的适配器标识。
//
// 它和 official 的区别是口径不同而不是数据不同：official 以「账号」为单位（一个 OAuth 账号 →
// 一条凭据），这里以「凭据」为单位（任意渠道下的每条 key）。两者都会出现在统一视图里，
// 所以 official 自动物化出来的那几个渠道要在这里排除（见 stationChannels）。
const stationKind = "channel"

func init() {
	// 与内置的 official 一样在 init 注册：号池开箱就有官方账号与渠道凭据两种后端。
	if err := Register(stationAdapter{}); err != nil {
		panic(fmt.Sprintf("register pool adapter %q: %v", stationKind, err))
	}
}

// stationAdapter 把 octopus 自己的渠道凭据投影成号池条目。
//
// 它只读库、不新增表、不改选路：
//   - list    ← channels + channel_keys（排除官方账号池渠道）
//   - get     ← 与列表同源
//   - probe   ← health.ProbeToken（按站点族选只读端点，一次真实请求，只由人触发）
//   - refresh ← health.FetchBalance（New API 系 /api/user/self），并把余额落进渠道余额快照
//   - toggle  ← op.SetChannelKeyEnabled（同时写「是否参与选路」与「人工停用」两位）
//
// 没有 sync/provision/revoke：这里没有任何东西需要被"物化"出来，凭据本来就是人配的。
type stationAdapter struct{}

func (stationAdapter) Info() AdapterInfo {
	return AdapterInfo{
		Kind:         stationKind,
		Title:        "渠道凭据（中转站/直连）",
		Capabilities: []Capability{CapList, CapGet, CapProbe, CapRefresh, CapToggle},
		Builtin:      true,
		Since:        "R-pool-ext-002",
		Fields: []FieldSpec{
			{Name: "channel", Type: "string", Label: "所属渠道", Required: true},
			{Name: "key_name", Type: "string", Label: "凭据名称", Required: true},
			// 字段名用 credential 而不是 key：secret 字段名不允许出现在条目的 Labels/Detail 键上
			//（契约自检会查这一条），用 key 会和 key_name/key_enabled 这类正常字段名混淆。
			{Name: "credential", Type: "string", Label: "上游凭据", Required: true, Secret: true},
			{Name: "base_url", Type: "string", Label: "上游地址", Required: true},
			{Name: "family", Type: "string", Label: "站点族（决定探活口径）"},
		},
	}
}

func (stationAdapter) Entries(ctx context.Context) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	channels, err := stationChannels()
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(channels))
	for _, channel := range channels {
		for _, key := range channel.Keys {
			entries = append(entries, stationEntry(channel, key))
		}
	}
	return entries, nil
}

// stationEntryID 是条目 ID：<channel_id>:<key_name>。
// 渠道 ID 是数字，所以按第一个冒号切分就能拆回来，即使凭据名里带冒号也不会歧义。
func stationEntryID(channelID int, keyName string) string {
	return strconv.Itoa(channelID) + ":" + keyName
}

func stationEntryKey(id string) (int, string, error) {
	rawID, keyName, found := strings.Cut(id, ":")
	if !found || keyName == "" {
		return 0, "", fmt.Errorf("%w: %s (want <channel_id>:<key_name>)", ErrEntryNotFound, id)
	}
	channelID, err := strconv.Atoi(rawID)
	if err != nil || channelID <= 0 {
		return 0, "", fmt.Errorf("%w: %s", ErrEntryNotFound, id)
	}
	return channelID, keyName, nil
}

// 站点族：决定拿哪种只读端点探活，也让人一眼看出这条凭据是按什么口径判的健康。
const (
	stationFamilyNewAPI           = "new-api"
	stationFamilyOpenAICompatible = "openai-compatible"
	stationFamilyAnthropic        = "anthropic"
)

func stationFamily(channel model.Channel) string {
	base := strings.ToLower(strings.TrimRight(channel.BaseURL, "/"))
	switch {
	case strings.Contains(base, "anthropic"):
		return stationFamilyAnthropic
	case strings.HasSuffix(base, "/v1"):
		return stationFamilyOpenAICompatible
	default:
		// 默认按 New API 系：这类中转站的用户侧自查端点是 /api/user/self，
		// 也是本仓库既有的默认探活口径（internal/health/probe_token_client.go）。
		return stationFamilyNewAPI
	}
}

// probeKindFor 把站点族映射成探活口径。
// 口径会随条目一起回给调用方（detail.probe_kind）：看到"不健康"时要能知道是按哪种口径判的。
func probeKindFor(family string) health.ProbeKind {
	switch family {
	case stationFamilyAnthropic:
		return health.ProbeKindAnthropicKey
	case stationFamilyOpenAICompatible:
		return health.ProbeKindOpenAIKey
	default:
		return health.ProbeKindNAToken
	}
}

// stationStatus 把「渠道是否启用」与「凭据是否参与选路」两件事摊平成一个人读得懂的状态。
func stationStatus(channel model.Channel, key model.ChannelKey) string {
	switch {
	case !channel.Enabled:
		return "channel-disabled"
	case key.OperatorDisabled:
		return "disabled-by-operator"
	case !key.Enabled:
		return "disabled"
	default:
		return "active"
	}
}

// stationEntryHealthy 用凭据自身的累计统计给一个"健康"判断：成功数不少于失败数即视为可用。
//
// 无样本（两者都是零）按乐观先验记健康——与选路侧"缺数据不惩罚"同一口径；
// 真正的实时结论要调 Probe，那是一次会花掉额度的真实请求，只能由人主动触发。
func stationEntryHealthy(channel model.Channel, key model.ChannelKey) bool {
	if !channel.Enabled || !key.Enabled {
		return false
	}
	return key.RequestSuccess >= key.RequestFailed
}

func stationEntry(channel model.Channel, key model.ChannelKey) Entry {
	family := stationFamily(channel)
	entry := Entry{
		Kind:     stationKind,
		ID:       stationEntryID(channel.ID, key.Name),
		Name:     fmt.Sprintf("%s / %s", channel.Name, key.Name),
		Provider: channel.Name,
		Status:   stationStatus(channel, key),
		Enabled:  key.Enabled,
		Healthy:  stationEntryHealthy(channel, key),
		Labels: map[string]string{
			"channel":  channel.Name,
			"family":   family,
			"base_url": channel.BaseURL,
		},
		Detail: map[string]any{
			"channel_id":        channel.ID,
			"channel_name":      channel.Name,
			"channel_enabled":   channel.Enabled,
			"base_url":          channel.BaseURL,
			"family":            family,
			"probe_kind":        string(probeKindFor(family)),
			"key_name":          key.Name,
			"key_enabled":       key.Enabled,
			"operator_disabled": key.OperatorDisabled,
			"models":            len(channel.Models),
			"request_success":   key.RequestSuccess,
			"request_failed":    key.RequestFailed,
			"wait_time_ms":      key.WaitTime,
			"input_token":       key.InputToken,
			"output_token":      key.OutputToken,
			"input_cost":        key.InputCost,
			"output_cost":       key.OutputCost,
			"healthy_basis":     "历史成功率（无样本按乐观先验视为可用）；要实时结论请探活",
		},
	}
	if remaining, ok := op.ChannelBalance(channel.ID); ok {
		entry.Detail["balance_remaining"] = remaining
	}
	return entry
}

// stationChannels 读全部渠道及其凭据与模型。
//
// 直接读库而不是走 op.ChannelList：那个接口是给界面分页用的，也没有"连凭据一起给"的形状，
// 而号池要的是一次把所有凭据投影出来。这条路径不在转发热路径上，不涉及缓存一致性。
func stationChannels() ([]model.Channel, error) {
	var channels []model.Channel
	if err := db.GetDB().Preload("Keys").Preload("Models").Order("id asc").Find(&channels).Error; err != nil {
		return nil, err
	}
	kept := make([]model.Channel, 0, len(channels))
	for _, channel := range channels {
		// 官方账号池渠道由 official 适配器以"账号"的口径列示，这里排除，免得同一条凭据两个身份。
		if op.IsOfficialPoolChannelName(channel.Name) {
			continue
		}
		kept = append(kept, channel)
	}
	return kept, nil
}

func stationChannel(channelID int) (model.Channel, error) {
	var channel model.Channel
	err := db.GetDB().Preload("Keys").Preload("Models").First(&channel, channelID).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return model.Channel{}, fmt.Errorf("%w: channel %d", ErrEntryNotFound, channelID)
	case err != nil:
		// 库出错不能让调用方以为是"这条不存在"：404 只代表查得到但没这条。
		return model.Channel{}, fmt.Errorf("read channel %d: %w", channelID, err)
	}
	if op.IsOfficialPoolChannelName(channel.Name) {
		return model.Channel{}, fmt.Errorf(
			"%w: channel %d（官方账号池渠道按账号口径列示，请用 official 后端）", ErrEntryNotFound, channelID)
	}
	return channel, nil
}

func stationKeyByName(channel model.Channel, keyName string) (model.ChannelKey, error) {
	for _, key := range channel.Keys {
		if key.Name == keyName {
			return key, nil
		}
	}
	return model.ChannelKey{}, fmt.Errorf("%w: %s", ErrEntryNotFound, keyName)
}

// Get 取单条：与统一视图同源，避免"列表与详情两套口径"。
func (a stationAdapter) Get(ctx context.Context, id string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	channel, key, err := a.locate(id)
	if err != nil {
		return Entry{}, err
	}
	return stationEntry(channel, key), nil
}

func (a stationAdapter) locate(id string) (model.Channel, model.ChannelKey, error) {
	channelID, keyName, err := stationEntryKey(id)
	if err != nil {
		return model.Channel{}, model.ChannelKey{}, err
	}
	channel, err := stationChannel(channelID)
	if err != nil {
		return model.Channel{}, model.ChannelKey{}, err
	}
	key, err := stationKeyByName(channel, keyName)
	if err != nil {
		return model.Channel{}, model.ChannelKey{}, err
	}
	return channel, key, nil
}

// Probe 用这条凭据打一次站点的只读端点。
//
// 与 official 同一约定：失败也把当前条目一起回——调用方按 error 判结论、按 Entry 展状态。
// 注意这是一次真实请求（会用掉一次上游调用），所以只由人主动触发，适配器不做任何自动探活。
func (a stationAdapter) Probe(ctx context.Context, id string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	channel, key, err := a.locate(id)
	if err != nil {
		return Entry{}, err
	}
	entry := stationEntry(channel, key)
	kind := probeKindFor(stationFamily(channel))
	result := health.ProbeToken(ctx, kind, channel.BaseURL, key.Key, channel.Proxy)

	entry.Detail["probe_kind"] = string(kind)
	entry.Detail["probe_status_code"] = result.StatusCode
	entry.Detail["probe_latency_ms"] = result.LatencyMs
	entry.Detail["healthy_basis"] = "本次探活结果（" + string(kind) + "）"
	entry.Healthy = result.Healthy
	if result.Healthy {
		return entry, nil
	}
	entry.LastError = result.Error
	if result.Error == "" {
		return entry, fmt.Errorf("probe %s: 站点判为不健康（HTTP %d）", id, result.StatusCode)
	}
	return entry, fmt.Errorf("probe %s: %s", id, result.Error)
}

// Refresh 重新读一次站点余额（New API 系的 /api/user/self），并把结果并进条目。
//
// 余额同时落进既有的渠道余额快照（op.RecordChannelBalance）：加权选路的 balance 维度用的就是
// 这份快照，所以在号池里刷一次余额，选路侧顺带也受益。
func (a stationAdapter) Refresh(ctx context.Context, id string) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	channel, key, err := a.locate(id)
	if err != nil {
		return Entry{}, err
	}
	entry := stationEntry(channel, key)
	quota, used, remaining, ok := health.FetchBalance(ctx, channel.BaseURL, key.Key, channel.Proxy)
	entry.Detail["balance_quota"] = quota
	entry.Detail["balance_used"] = used
	entry.Detail["balance_remaining"] = remaining
	if !ok {
		entry.LastError = "站点未回可解析的余额"
		return entry, fmt.Errorf("refresh %s: 站点未回可解析的余额（%s）", id, health.BalanceUserSelfPath)
	}
	op.RecordChannelBalance(channel.ID, remaining)
	return entry, nil
}

// SetEnabled 人工启停一条渠道凭据（toggle），再回读这一条。
//
// 走既有的 op.SetChannelKeyEnabled：它同时写「此刻是否参与选路」与「人工停用」两位，
// 所以任何周期性物化（例如官方账号池同步）都不会把这个决定悄悄改回去。
func (a stationAdapter) SetEnabled(ctx context.Context, id string, enabled bool) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if _, _, err := a.locate(id); err != nil {
		return Entry{}, err
	}
	channelID, keyName, err := stationEntryKey(id)
	if err != nil {
		return Entry{}, err
	}
	found, err := op.SetChannelKeyEnabled(nil, channelID, keyName, enabled)
	if err != nil {
		return Entry{}, fmt.Errorf("set enabled=%v for %s: %w", enabled, id, err)
	}
	if !found {
		return Entry{}, fmt.Errorf("%w: %s", ErrEntryNotFound, keyName)
	}
	return a.Get(ctx, id)
}
