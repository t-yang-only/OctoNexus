package op

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// 总余额聚合（T-balance-001）: 把进程内的既有事实折成一份"还能用多少钱、还剩多少次"的快照。
//
// 数据来源全是既有事实, 本文件只做聚合与折算, 不新增采集:
//   - 渠道余额 ← op.ChannelBalance（配额扫描 task/quota_scan 与号池站点刷新写下的内存快照,
//     加权选路的 balance 维度读的也是它）;
//   - 剩余次数 ← 渠道配置的包月额度 - 已用（BillingMode=subscription 才是次数口径, 但这里不按模式过滤:
//     用户填了包月额度就说明他按次数在算账）;
//   - 客户端 Key 额度 ← Key 自身的 MaxCost 与累计统计（middleware.APIKeyAuth 里同一份事实）。
//
// 快照只读内存, 因此没有 ctx、也不落库: 重启后余额在下一轮扫描前按"未知"处理（与选路同口径）。

// BalanceSummaryGet 聚合总余额快照。未知余额的渠道不计入 Total, 只计入 UnknownChannels。
func BalanceSummaryGet() model.BalanceSummary {
	pointsPerUnit, currency := balanceUnitSettings()
	summary := model.BalanceSummary{
		Currency:      currency,
		PointsPerUnit: pointsPerUnit,
		Channels:      make([]model.ChannelBalanceRow, 0, channelCache.Len()),
		Keys:          make([]model.APIKeyBalanceRow, 0, apiKeyCache.Len()),
		GeneratedAt:   time.Now().Unix(),
	}

	counts := channelKeyCounts()
	for _, channel := range channelCache.GetAll() {
		row := model.ChannelBalanceRow{
			ChannelID:        channel.ID,
			ChannelName:      channel.Name,
			Enabled:          channel.Enabled,
			BillingMode:      channel.BillingMode,
			Multiplier:       channel.Multiplier,
			PerCallPrice:     channel.PerCallPrice,
			MonthlyQuota:     channel.MonthlyQuota,
			MonthlyUsed:      channel.MonthlyUsed,
			MonthlyRemaining: monthlyRemaining(channel.MonthlyQuota, channel.MonthlyUsed),
			KeyCount:         counts[channel.ID].total,
			KeyEnabled:       counts[channel.ID].enabled,
		}
		if remaining, ok := ChannelBalance(channel.ID); ok {
			row.Known = true
			row.Remaining = remaining
			row.Balance = model.ConvertBalancePoints(remaining, pointsPerUnit)
			summary.Total += row.Balance
			summary.KnownChannels++
		} else {
			summary.UnknownChannels++
		}
		summary.TotalMonthlyRemaining += row.MonthlyRemaining
		summary.Channels = append(summary.Channels, row)
	}
	sort.Slice(summary.Channels, func(i, j int) bool {
		return summary.Channels[i].ChannelID < summary.Channels[j].ChannelID
	})

	keys := apiKeyCache.GetAll()
	for _, key := range keys {
		summary.Keys = append(summary.Keys, APIKeyBalanceGet(key))
	}
	sort.Slice(summary.Keys, func(i, j int) bool { return summary.Keys[i].ID < summary.Keys[j].ID })

	return summary
}

// APIKeyBalanceGet 算一个客户端 Key 的额度自查: 不限额度（MaxCost<=0）时以 Unlimited 表达,
// 剩余额度不会为负（超额只可能来自并发下的判额间隙, 显示成负值会让调用方以为欠费）。
func APIKeyBalanceGet(key model.APIKey) model.APIKeyBalanceRow {
	stats := StatsAPIKeyGet(key.ID)
	used := stats.InputCost + stats.OutputCost
	row := model.APIKeyBalanceRow{
		ID:       key.ID,
		Name:     key.Name,
		Enabled:  key.Enabled,
		Limit:    key.MaxCost,
		Used:     used,
		Requests: stats.RequestSuccess + stats.RequestFailed,
		Tokens:   stats.InputToken + stats.OutputToken,
	}
	if key.MaxCost <= 0 {
		row.Unlimited = true
		return row
	}
	remaining := key.MaxCost - used
	if remaining < 0 {
		remaining = 0
	}
	row.Remaining = remaining
	return row
}

// balanceUnitSettings 读换算口径配置; 读不到或解析失败都按默认口径（NormalizeBalanceUnit 兜底）。
func balanceUnitSettings() (float64, string) {
	pointsValue, _ := SettingGetString(model.SettingKeyBalancePointsPerUnit)
	currency, _ := SettingGetString(model.SettingKeyBalanceCurrency)
	points, err := strconv.ParseFloat(strings.TrimSpace(pointsValue), 64)
	if err != nil {
		points = 0
	}
	return model.NormalizeBalanceUnit(points, currency)
}

// channelKeyCount 是一条渠道的凭据计数: 总数与其中启用中的数量。
type channelKeyCount struct {
	total   int
	enabled int
}

// channelKeyCounts 按渠道统计凭据数; 停用凭据仍计入 total（用户要看到"这条渠道还剩几把能用的钥匙"）。
func channelKeyCounts() map[int]channelKeyCount {
	counts := make(map[int]channelKeyCount, channelKeyCache.Len())
	for _, key := range channelKeyCache.GetAll() {
		count := counts[key.ChannelID]
		count.total++
		if key.Enabled {
			count.enabled++
		}
		counts[key.ChannelID] = count
	}
	return counts
}

// monthlyRemaining 算包月剩余次数; 未配置包月额度（<=0）时记 0, 不参与总剩余次数。
func monthlyRemaining(quota, used float64) float64 {
	if quota <= 0 {
		return 0
	}
	remaining := quota - used
	if remaining < 0 {
		return 0
	}
	return remaining
}
