package op

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// 手动订阅（R-acct-004 / T-acct-005）的口径：
//   - 校验：名称必填/长度、点数与口径非负、渠道必须存在；
//   - 并账：绑定了"无自动读数的渠道"→ 该渠道由未知变已知（来源 manual），并计入总额；
//     该渠道已有自动读数 → 手动那条不计（同一笔钱算两遍会让总额虚高）；
//   - 过期与停用：都不计入总额，但明细照列（面板要显示"为什么没算"）。
func seedManualSubscriptionCache(t *testing.T, items []model.ManualSubscription) {
	t.Helper()
	before := ManualSubscriptionList()
	t.Cleanup(func() {
		manualSubscriptionCache.Lock()
		manualSubscriptionCache.items = before
		manualSubscriptionCache.Unlock()
	})
	manualSubscriptionCache.Lock()
	manualSubscriptionCache.items = items
	manualSubscriptionCache.Unlock()
}

func TestManualSubscriptionValidation(t *testing.T) {
	cases := []struct {
		name    string
		input   model.ManualSubscription
		wantErr string
	}{
		{name: "名称为空", input: model.ManualSubscription{Name: "  "}, wantErr: "名称"},
		{name: "名称过长", input: model.ManualSubscription{Name: strings.Repeat("长", 65)}, wantErr: "过长"},
		{name: "点数为负", input: model.ManualSubscription{Name: "套餐", BalancePoints: -1}, wantErr: "不能为负"},
		{name: "口径为负", input: model.ManualSubscription{Name: "套餐", PointsPerUnit: -5}, wantErr: "换算口径"},
		{name: "有效期为负", input: model.ManualSubscription{Name: "套餐", ExpireAt: -1}, wantErr: "有效期"},
		{name: "正常（名称去空白）", input: model.ManualSubscription{Name: "  Claude Pro  "}},
	}
	for _, tc := range cases {
		got, err := model.NormalizeManualSubscription(tc.input)
		if tc.wantErr == "" {
			if err != nil {
				t.Fatalf("%s: 不该报错: %v", tc.name, err)
			}
			if got.Name != "Claude Pro" {
				t.Fatalf("%s: 名称未去空白: %q", tc.name, got.Name)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: 期望包含 %q 的错误, 实际 %v", tc.name, tc.wantErr, err)
		}
	}
}

func TestManualSubscriptionDaysLeft(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if days, expired := model.ManualSubscriptionDaysLeft(0, now); days != -1 || expired {
		t.Fatalf("无期限应为 -1/false, 实际 %d/%v", days, expired)
	}
	if days, expired := model.ManualSubscriptionDaysLeft(now.Unix()-60, now); days != 0 || !expired {
		t.Fatalf("已过期应为 0/true, 实际 %d/%v", days, expired)
	}
	// 不足一天按一天算：续费提醒宁早不晚。
	if days, expired := model.ManualSubscriptionDaysLeft(now.Add(3*time.Hour).Unix(), now); days != 1 || expired {
		t.Fatalf("三小时后到期应为 1 天, 实际 %d/%v", days, expired)
	}
	if days, _ := model.ManualSubscriptionDaysLeft(now.Add(48*time.Hour).Unix(), now); days != 2 {
		t.Fatalf("48 小时应为 2 天, 实际 %d", days)
	}
}

func TestManualSubscriptionEntersBalanceSummary(t *testing.T) {
	seedBalanceCaches(t, []model.Channel{
		{ID: 1, ChannelConfig: model.ChannelConfig{Name: "无接口站点", Enabled: true}},
		{ID: 2, ChannelConfig: model.ChannelConfig{Name: "有接口站点", Enabled: true}},
	}, nil, nil)
	restoreBalance(map[int]float64{2: 500000}) // 渠道 2 有自动读数 = 1 个货币单位
	seedManualSubscriptionCache(t, []model.ManualSubscription{
		{ID: 1, ChannelID: 1, Name: "手录套餐", BalancePoints: 250000, Enabled: true},
		{ID: 2, ChannelID: 2, Name: "重复的手录", BalancePoints: 900000, Enabled: true},
		{ID: 3, ChannelID: 0, Name: "通用额度", BalancePoints: 100000, Enabled: true},
		{ID: 4, ChannelID: 0, Name: "停用的", BalancePoints: 100000, Enabled: false},
		{ID: 5, ChannelID: 0, Name: "过期的", BalancePoints: 100000, Enabled: true, ExpireAt: time.Now().Add(-time.Hour).Unix()},
	})

	summary := BalanceSummaryGet()
	// 总额 = 渠道2 自动读数 1.0 + 手录 0.5 + 通用 0.2 = 1.7（重复与停用/过期都不计）。
	if diff := summary.Total - 1.7; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("总额 = %v, 期望 1.7（明细: %+v）", summary.Total, summary.ManualSubscriptions)
	}
	if diff := summary.ManualTotal - 0.7; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("手录合计 = %v, 期望 0.7", summary.ManualTotal)
	}
	if summary.ManualExpired != 1 {
		t.Fatalf("过期条数 = %d, 期望 1", summary.ManualExpired)
	}
	// 渠道 1 由"未知"变成"已知（手动）"：这正是让无接口站点进总余额的路径。
	for _, row := range summary.Channels {
		switch row.ChannelID {
		case 1:
			if !row.Known || row.BalanceSource != "manual" || row.Remaining != 250000 {
				t.Fatalf("渠道 1 未按手动来源入账: %+v", row)
			}
		case 2:
			if !row.Known || row.BalanceSource != "api" {
				t.Fatalf("渠道 2 应保持自动来源: %+v", row)
			}
		}
	}
	if summary.UnknownChannels != 0 || summary.KnownChannels != 2 {
		t.Fatalf("渠道计数 = 已知 %d / 未知 %d, 期望 2/0", summary.KnownChannels, summary.UnknownChannels)
	}
	// 明细的可计标记要与总额一致：只有 1 与 3 计入了。
	counted := map[int]bool{}
	for _, row := range summary.ManualSubscriptions {
		counted[row.ID] = row.Counted
	}
	if !counted[1] || !counted[3] || counted[2] || counted[4] || counted[5] {
		t.Fatalf("counted 标记不符合口径: %+v", counted)
	}
}

func TestManualSubscriptionPointsPerUnitOverride(t *testing.T) {
	seedBalanceCaches(t, nil, nil, nil)
	restoreBalance(map[int]float64{})
	seedManualSubscriptionCache(t, []model.ManualSubscription{
		// 行级口径 100000：300000 点 = 3 个单位（全局口径是 500000，会被覆盖）。
		{ID: 1, Name: "不同基数", BalancePoints: 300000, PointsPerUnit: 100000, Enabled: true},
	})
	summary := BalanceSummaryGet()
	if diff := summary.Total - 3.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("行级口径未生效: 总额 = %v, 期望 3", summary.Total)
	}
	if summary.ManualSubscriptions[0].PointsPerUnit != 100000 {
		t.Fatalf("明细里的生效口径 = %v, 期望 100000", summary.ManualSubscriptions[0].PointsPerUnit)
	}
}

func TestManualSubscriptionCRUDRejectsUnknownChannel(t *testing.T) {
	useCredentialTestKey(t)
	conn := openAutoGroupTestDB(t)
	if err := conn.AutoMigrate(&model.ManualSubscription{}); err != nil {
		t.Fatalf("migrate manual subscription: %v", err)
	}
	// 渠道不存在必须被拒绝：录一条挂在空气上的套餐，用户只能在总额里看到一笔查无来源的钱。
	if _, err := ManualSubscriptionCreate(context.Background(), model.ManualSubscription{Name: "孤儿", ChannelID: 9999}); err == nil {
		t.Fatalf("绑定不存在的渠道应报错")
	}
}
