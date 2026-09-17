package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// 总余额聚合（T-balance-001）的纯聚合口径：只用既有事实算总额、剩余次数与 Key 额度，
// 未知余额不计入总额、Key 额度不为负。
//
// 用例全部在内存索引缓存上播种，不碰数据库：聚合读的就是这些缓存（与线上同源）。
func seedBalanceCaches(t *testing.T, channels []model.Channel, keys []model.ChannelKey, apiKeys []model.APIKey) {
	t.Helper()
	channelsBefore := channelCache.GetAll()
	keysBefore := channelKeyCache.GetAll()
	apiKeysBefore := apiKeyCache.GetAll()
	statsBefore := statsAPIKeyCache.GetAll()
	balanceBefore := balanceSnapshot()
	t.Cleanup(func() {
		restoreCache(channelCache, channelsBefore)
		restoreCache(channelKeyCache, keysBefore)
		restoreCache(apiKeyCache, apiKeysBefore)
		restoreCache(statsAPIKeyCache, statsBefore)
		restoreBalance(balanceBefore)
	})
	channelCache.Clear()
	channelKeyCache.Clear()
	apiKeyCache.Clear()
	statsAPIKeyCache.Clear()
	restoreBalance(map[int]float64{})
	for _, channel := range channels {
		channelCache.Set(channel.ID, channel)
	}
	for _, key := range keys {
		channelKeyCache.Set(key.ID, key)
	}
	for _, apiKey := range apiKeys {
		apiKeyCache.Set(apiKey.ID, apiKey)
	}
}

func restoreCache[K comparable, V any](target interface {
	Clear()
	Set(K, V)
}, snapshot map[K]V) {
	target.Clear()
	for key, value := range snapshot {
		target.Set(key, value)
	}
}

// balanceSnapshot 复制内存余额快照（含互斥锁下的读取）。
func balanceSnapshot() map[int]float64 {
	channelBalance.mu.RLock()
	defer channelBalance.mu.RUnlock()
	out := make(map[int]float64, len(channelBalance.values))
	for id, value := range channelBalance.values {
		out[id] = value
	}
	return out
}

// restoreBalance 用给定快照覆盖内存余额表。
func restoreBalance(snapshot map[int]float64) {
	channelBalance.mu.Lock()
	defer channelBalance.mu.Unlock()
	channelBalance.values = make(map[int]float64, len(snapshot))
	for id, value := range snapshot {
		channelBalance.values[id] = value
	}
}

// TestBalanceSummaryAggregatesKnownAndUnknown 已知余额计入总额、未知余额只计数不折算；
// 包月额度只对配置过的渠道算剩余次数；凭据数按渠道统计（含停用凭据）。
func TestBalanceSummaryAggregatesKnownAndUnknown(t *testing.T) {
	SettingSetStringForTest(model.SettingKeyBalancePointsPerUnit, "500000", true)
	SettingSetStringForTest(model.SettingKeyBalanceCurrency, "USD", true)
	t.Cleanup(func() {
		SettingSetStringForTest(model.SettingKeyBalancePointsPerUnit, "", false)
		SettingSetStringForTest(model.SettingKeyBalanceCurrency, "", false)
	})

	seedBalanceCaches(t,
		[]model.Channel{
			{ID: 9001, ChannelConfig: model.ChannelConfig{
				Name: "已知余额站", Enabled: true, BillingMode: "metered",
				MonthlyQuota: 100, MonthlyUsed: 40,
			}},
			{ID: 9002, ChannelConfig: model.ChannelConfig{Name: "还没扫到余额站", Enabled: true}},
		},
		[]model.ChannelKey{
			{ID: 9101, ChannelID: 9001, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk-1", Enabled: true}},
			{ID: 9102, ChannelID: 9001, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k2", Key: "sk-2", Enabled: false}},
			{ID: 9103, ChannelID: 9002, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k3", Key: "sk-3", Enabled: true}},
		},
		nil,
	)
	RecordChannelBalance(9001, 1000000) // 1,000,000 点 / 500000 = 2 个货币单位

	summary := BalanceSummaryGet()

	if summary.Total != 2 {
		t.Errorf("Total = %v, want 2（只有已知余额的渠道计入）", summary.Total)
	}
	if summary.KnownChannels != 1 || summary.UnknownChannels != 1 {
		t.Errorf("known/unknown = %d/%d, want 1/1", summary.KnownChannels, summary.UnknownChannels)
	}
	if summary.TotalMonthlyRemaining != 60 {
		t.Errorf("TotalMonthlyRemaining = %v, want 60", summary.TotalMonthlyRemaining)
	}
	if summary.Currency != "USD" || summary.PointsPerUnit != 500000 {
		t.Errorf("口径 = %v/%v, want USD/500000", summary.Currency, summary.PointsPerUnit)
	}
	if len(summary.Channels) != 2 || summary.Channels[0].ChannelID != 9001 {
		t.Fatalf("渠道明细 = %+v, want 按 ID 升序的两行", summary.Channels)
	}
	known := summary.Channels[0]
	if !known.Known || known.Balance != 2 || known.Remaining != 1000000 {
		t.Errorf("已知渠道 = %+v, want known/1000000 点/2 单位", known)
	}
	if known.KeyCount != 2 || known.KeyEnabled != 1 {
		t.Errorf("凭据数 = %d/%d, want 2/1（停用凭据仍计入总数）", known.KeyCount, known.KeyEnabled)
	}
	unknown := summary.Channels[1]
	if unknown.Known || unknown.Balance != 0 || unknown.MonthlyRemaining != 0 {
		t.Errorf("未知渠道 = %+v, want 未知且不计剩余次数", unknown)
	}
}

// TestBalanceSummaryUsesConfiguredUnit 换算口径按设置走：同一份点数在不同口径下折出不同额度。
func TestBalanceSummaryUsesConfiguredUnit(t *testing.T) {
	SettingSetStringForTest(model.SettingKeyBalancePointsPerUnit, "100000", true)
	SettingSetStringForTest(model.SettingKeyBalanceCurrency, "CNY", true)
	t.Cleanup(func() {
		SettingSetStringForTest(model.SettingKeyBalancePointsPerUnit, "", false)
		SettingSetStringForTest(model.SettingKeyBalanceCurrency, "", false)
	})

	seedBalanceCaches(t,
		[]model.Channel{{ID: 9011, ChannelConfig: model.ChannelConfig{Name: "口径站", Enabled: true}}},
		nil, nil,
	)
	RecordChannelBalance(9011, 1000000)

	summary := BalanceSummaryGet()
	if summary.Total != 10 {
		t.Errorf("Total = %v, want 10（1000000 点 / 100000 口径）", summary.Total)
	}
	if summary.Currency != "CNY" {
		t.Errorf("Currency = %v, want CNY", summary.Currency)
	}
}

// TestAPIKeyBalanceUnlimitedAndLimited Key 额度自查：不限额度用标志表达；
// 超额时剩余为 0 而不是负数（并发判额间隙不该显示成欠费）。
func TestAPIKeyBalanceUnlimitedAndLimited(t *testing.T) {
	SettingSetStringForTest(model.SettingKeyBalancePointsPerUnit, "", false)
	seedBalanceCaches(t, nil, nil, []model.APIKey{
		{ID: 9201, Name: "不限额度", MaxCost: 0, Enabled: true},
		{ID: 9202, Name: "限额 10", MaxCost: 10, Enabled: true},
		{ID: 9203, Name: "已超额", MaxCost: 5, Enabled: true},
	})
	statsAPIKeyCache.Set(9202, model.StatsAPIKey{APIKeyID: 9202, StatsMetrics: model.StatsMetrics{
		InputCost: 1, OutputCost: 2, InputToken: 100, OutputToken: 50, RequestSuccess: 3, RequestFailed: 1,
	}})
	statsAPIKeyCache.Set(9203, model.StatsAPIKey{APIKeyID: 9203, StatsMetrics: model.StatsMetrics{
		InputCost: 4, OutputCost: 4,
	}})

	summary := BalanceSummaryGet()
	byID := map[int]model.APIKeyBalanceRow{}
	for _, row := range summary.Keys {
		byID[row.ID] = row
	}

	if row := byID[9201]; !row.Unlimited || row.Remaining != 0 {
		t.Errorf("不限额度 = %+v, want Unlimited 且剩余 0", row)
	}
	if row := byID[9202]; row.Unlimited || row.Remaining != 7 || row.Used != 3 || row.Requests != 4 || row.Tokens != 150 {
		t.Errorf("限额 Key = %+v, want 已花 3 / 剩余 7 / 请求 4 / 词元 150", row)
	}
	if row := byID[9203]; row.Remaining != 0 {
		t.Errorf("超额 Key 剩余 = %v, want 0（不显示负数）", row.Remaining)
	}
}

// TestNormalizeBalanceUnitFallsBack 坏配置回落到默认口径，而不是让总额算不出来。
func TestNormalizeBalanceUnitFallsBack(t *testing.T) {
	if points, currency := model.NormalizeBalanceUnit(0, "  "); points != model.BalanceDefaultPointsPerUnit || currency != model.BalanceDefaultCurrency {
		t.Errorf("零口径回落 = %v/%v, want 默认值", points, currency)
	}
	if got := model.ConvertBalancePoints(1500000, 0); got != 3 {
		t.Errorf("非法口径下的折算 = %v, want 3（按默认 500000）", got)
	}
	if got := model.ConvertBalancePoints(0, 500000); got != 0 {
		t.Errorf("零点折算 = %v, want 0", got)
	}
}
