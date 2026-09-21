package op

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 渠道计费事实的落库判据（T-allocate-001 演练抓出的真缺陷）。
//
// 缺陷现场: ChannelUpdate 用「逐列点名」的 Select 更新, 那份名单里没有五个计费列
// （billing_mode / multiplier / per_call_price / monthly_quota / monthly_used）。
// 结果是: 面板上填了、接口回 200、进程缓存里也有值（所以选路当下是对的）, 但**库里那一行始终是 NULL**;
// 重启或换实例后, 分压（mode=allocate）与加权模式的余额/倍率维度就悄悄退回"未知"——
// 一个"看起来生效、实际上没落地"的静默降级。
//
// 因此这里直接断言**库内行**（不看缓存、不看接口回显）: 那才是重启之后还会存在的事实。
func TestChannelUpdatePersistsBillingFields(t *testing.T) {
	openChannelKeyTestDB(t)

	created, err := ChannelCreate(&model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-billing", BaseURL: "http://127.0.0.1:1", Enabled: true},
		Keys:          []model.ChannelKeyInput{{Name: "k1", Key: "sk-billing"}},
		Models:        []string{"mock-good"},
	}, context.Background())
	if err != nil {
		t.Fatalf("ChannelCreate: %v", err)
	}

	// 更新时带上全部计费事实（面板保存走的就是这条路径）。
	if _, err := ChannelUpdate(&model.ChannelDetail{
		ID: created.ID,
		ChannelConfig: model.ChannelConfig{
			Name: "DS-UNIT-billing", BaseURL: "http://127.0.0.1:1", Enabled: true,
			BillingMode: "subscription", Multiplier: 2.5, PerCallPrice: 0.02,
			MonthlyQuota: 1000, MonthlyUsed: 100,
		},
		Keys:   []model.ChannelKeyInput{{Name: "k1", Key: "sk-billing"}},
		Models: []string{"mock-good"},
	}, context.Background()); err != nil {
		t.Fatalf("ChannelUpdate: %v", err)
	}

	var row model.Channel
	if err := dbGetChannel(t, created.ID, &row); err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if row.BillingMode != "subscription" || row.Multiplier != 2.5 || row.PerCallPrice != 0.02 ||
		row.MonthlyQuota != 1000 || row.MonthlyUsed != 100 {
		t.Fatalf("库内计费列 = %q/%v/%v/%v/%v, want subscription/2.5/0.02/1000/100"+
			"（ChannelUpdate 的 Select 名单漏列会让这些值只在缓存里, 重启即丢）",
			row.BillingMode, row.Multiplier, row.PerCallPrice, row.MonthlyQuota, row.MonthlyUsed)
	}

	// 逆向对照: 把计费事实清零也必须真的写回去（清零是最容易被"按零值跳过"吞掉的一类写入）。
	if _, err := ChannelUpdate(&model.ChannelDetail{
		ID:            created.ID,
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-billing", BaseURL: "http://127.0.0.1:1", Enabled: true},
		Keys:          []model.ChannelKeyInput{{Name: "k1", Key: "sk-billing"}},
		Models:        []string{"mock-good"},
	}, context.Background()); err != nil {
		t.Fatalf("ChannelUpdate(清空): %v", err)
	}
	if err := dbGetChannel(t, created.ID, &row); err != nil {
		t.Fatalf("query channel: %v", err)
	}
	if row.BillingMode != "" || row.Multiplier != 0 || row.PerCallPrice != 0 ||
		row.MonthlyQuota != 0 || row.MonthlyUsed != 0 {
		t.Fatalf("清空后库内计费列 = %q/%v/%v/%v/%v, want 全零",
			row.BillingMode, row.Multiplier, row.PerCallPrice, row.MonthlyQuota, row.MonthlyUsed)
	}
}

// dbGetChannel 按主键读渠道行; 单独抽出来是为了让上面的断言一眼看出"读的是库, 不是缓存"。
func dbGetChannel(t *testing.T, id int, out *model.Channel) error {
	t.Helper()
	return db.GetDB().First(out, id).Error
}
