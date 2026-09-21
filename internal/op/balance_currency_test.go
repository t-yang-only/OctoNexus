package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// 余额读数的**单位口径**判据（R-balance-002 实测抓出的真缺陷）。
//
// 缺陷现场：上游余额有三种接口，两种报美元（/user/balance、/usage），一种报 new-api 的"点"
// （/api/user/self 的 quota）。聚合层一律按 balance_points_per_unit 折算，于是 apikey.fan 报的
// 32.53 美元被再除一次 500000，面板显示 6.5e-05 —— "读到了余额，但读成了一个没有任何意义的数"。
//
// 判据取"聚合结果的货币值"而不是"有没有读数"：这类错误的表现是数值很小但非零，
// 只看非零/非空断言永远抓不到。
func TestBalanceSummaryRespectsReadUnit(t *testing.T) {
	SettingSetStringForTest(model.SettingKeyBalancePointsPerUnit, "500000", true)
	t.Cleanup(func() {
		SettingSetStringForTest(model.SettingKeyBalancePointsPerUnit, "", false)
	})

	currencyChannel := model.Channel{ID: 9101}
	currencyChannel.Name = "报美元的上游"
	pointsChannel := model.Channel{ID: 9102}
	pointsChannel.Name = "报点的上游"
	seedBalanceCaches(t, []model.Channel{currencyChannel, pointsChannel}, nil, nil)

	RecordChannelBalanceWithUnit(9101, 32.53, true)     // /user/balance 口径
	RecordChannelBalanceWithUnit(9102, 16265000, false) // new-api quota 口径：16265000 点

	summary := BalanceSummaryGet()
	byID := map[int]model.ChannelBalanceRow{}
	for _, row := range summary.Channels {
		byID[row.ChannelID] = row
	}

	if got := byID[9101].Balance; got < 32.52 || got > 32.54 {
		t.Fatalf("货币口径读数 = %v, want 32.53（再按点折算会得到 6.5e-05）", got)
	}
	if got := byID[9102].Balance; got < 32.52 || got > 32.54 {
		t.Fatalf("点口径读数 = %v, want 32.53（16265000 ÷ 500000）", got)
	}
	if summary.Total < 65.05 || summary.Total > 65.07 {
		t.Fatalf("总额 = %v, want ≈65.06", summary.Total)
	}
	if summary.KnownChannels != 2 {
		t.Fatalf("已知渠道 = %d, want 2", summary.KnownChannels)
	}
}
