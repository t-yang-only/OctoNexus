package op

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 需求9「价格对比统一标准」的判据。
//
// # 这个功能要防住的三件事
//
//  1. **按次与按量没法比** —— 一个报 $0.01/次、一个报 $2/1M token，
//     不折成同一把尺子，用户问的"哪个划算"永远答不了；
//  2. **汇率没配时假装折了** —— 除一个 0 会得到 +Inf，界面上显示成一串无穷大，
//     比不显示更糟；
//  3. **分母不可见** —— 按次折成每 token 价完全依赖"平均每次请求多少 token"，
//     不给出来用户就无法判断结论可不可信，也发现不了分母被极端值带偏。

var priceUnifyTestSeq int64

func openPriceUnifyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&priceUnifyTestSeq, 1)
	dsn := fmt.Sprintf("file:priceunify-%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.PriceSnapshot{}, &model.UsageSnapshot{}, &model.RelayLog{}, &model.Setting{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))
	return conn
}

func seedPriceRow(t *testing.T, conn *gorm.DB, row model.PriceSnapshot) {
	t.Helper()
	if err := conn.Create(&row).Error; err != nil {
		t.Fatalf("seed price row: %v", err)
	}
}

// setPriceRate 直接写设置缓存，绕过设置表：本组用例只关心折算结果，
// 不关心设置怎么读（后者由设置自己的用例覆盖）。
func setPriceRate(t *testing.T, rate string) {
	t.Helper()
	SettingSetStringForTest(model.SettingKeyPriceCnyPerUsd, rate, true)
	t.Cleanup(func() { SettingSetStringForTest(model.SettingKeyPriceCnyPerUsd, "", false) })
}

// seedAvgTokens 造若干条真实请求，把"平均每次 token 数"钉在一个已知值上。
func seedAvgTokens(t *testing.T, conn *gorm.DB, perRequest int) {
	t.Helper()
	for i := 0; i < 4; i++ {
		total := int64(perRequest)
		half := total / 2
		row := model.RelayLog{
			RequestID: uint64(9000 + i), Status: "success", Model: "m",
			PromptTokens: half, CompletionToks: total - half,
		}
		if err := conn.Create(&row).Error; err != nil {
			t.Fatalf("seed relay log: %v", err)
		}
	}
}

// 计费方式归并（T-price-003）：上游字面量与项目枚举不一致，必须按语义归并。
//
// 实测背景：线上 79 行价格里 76 行的 billing_mode 是 `token`，而 model.Channel 的
// 枚举写的是 `metered`。逐个字面量比会漏掉其中一种 —— 表现为"该折的没折"。
// 更危险的是 `subscription`（包月）：它没有每 token 单价，被当成按量折出一个数，
// 那个数看起来是个结论，实际随用量浮动。
func TestNormalizeBillingMode(t *testing.T) {
	cases := []struct {
		raw  string
		want billingMode
	}{
		// 上游实际用的写法
		{"token", billingMetered},
		// 项目枚举的写法
		{"metered", billingMetered},
		{"", billingUnknown},
		{"  ", billingUnknown},
		{"TOKEN", billingMetered}, // 大小写不敏感
		{"per_call", billingPerCall},
		{"percall", billingPerCall},
		{"per-request", billingPerCall},
		{"subscription", billingSubscription},
		{"monthly", billingSubscription},
		{"包月", billingSubscription},
		{"按量", billingMetered},
		// 认不出的值按未知，不明着当按量
		{"weird-mode", billingUnknown},
	}
	for _, tc := range cases {
		if got := normalizeBillingMode(tc.raw); got != tc.want {
			t.Errorf("normalizeBillingMode(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}

// 包月不能折出每 token 价：那会给出一个随用量浮动、却看起来像结论的数。
func TestPriceUnifySubscriptionIsNotConverted(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "s.example", Model: "monthly-model", Multiplier: 1,
		BillingMode: "subscription", InputPrice: 100, OutputPrice: 200,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	row := rows[0]
	if row.CnyInputPrice != 0 || row.CnyOutputPrice != 0 {
		t.Errorf("包月不应折出每 token 人民币价，实得 入%v 出%v", row.CnyInputPrice, row.CnyOutputPrice)
	}
	if row.Basis == "" {
		t.Error("必须说明为什么不折（否则用户以为功能坏了）")
	}
	// 原币种价必须仍在：折不了尺子也不能把原始信息弄丢。
	if row.InputPrice != 100 {
		t.Errorf("原币种价应保留，实得 %v", row.InputPrice)
	}
}

// 上游的 token 写法必须走到按量分支（这是线上 76/79 行的真实形态）。
func TestPriceUnifyAcceptsUpstreamTokenLiteral(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "u.example", Model: "token-model", Multiplier: 1,
		BillingMode: "token", InputPrice: 14, OutputPrice: 21, CacheReadPrice: 3.5,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	row := rows[0]
	if row.CnyInputPrice != 2 || row.CnyOutputPrice != 3 || row.CnyCacheReadPrice != 0.5 {
		t.Errorf("token 写法应按按量折算，实得 入%v 出%v 缓存%v",
			row.CnyInputPrice, row.CnyOutputPrice, row.CnyCacheReadPrice)
	}
}

// 认不出的计费方式不折算，也不明着当按量。
func TestPriceUnifyUnknownBillingModeIsNotConverted(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "w.example", Model: "weird-model", Multiplier: 1,
		BillingMode: "some-new-mode", InputPrice: 14, OutputPrice: 21,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	row := rows[0]
	if row.CnyInputPrice != 0 {
		t.Errorf("认不出的计费方式不应折出价格，实得 %v", row.CnyInputPrice)
	}
	if row.Basis == "" {
		t.Error("必须说明为什么不折")
	}
}

// 声明了按次却没给单价：数据不全，不猜。
func TestPriceUnifyPerCallWithoutPriceLeavesEmpty(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedAvgTokens(t, conn, 1000)
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "n.example", Model: "percall-noprice", Multiplier: 1,
		BillingMode: "per_call", // PerCallPrice 留 0
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	row := rows[0]
	if row.CnyInputPrice != 0 {
		t.Errorf("缺单价时不应折出价格，实得 %v", row.CnyInputPrice)
	}
	// 必须点明是"缺单价"而不是别的缘故。
	//
	// 这条是变异检查补出来的：只断言"价格为 0"抓不住"单价守卫被删"——
	// 删掉守卫后 0 除汇率还是 0，价格断言照样绿，而 Basis 会退化成
	// "样本不足"（分母明明够），把数据不全说成样本不足，指向错误的排查方向。
	if !strings.Contains(row.Basis, "单价") {
		t.Errorf("Basis 应说明是缺每次单价，实得 %q", row.Basis)
	}
}

// 按 cny_input 排序时，按次行必须按**它自己的人民币价**参与排序。
//
// 这是排序键与显示值一致性的判据。旧实现排序用 input_price / rate，
// 而按次行的 input_price 是 0（站点只给每次价）—— 于是按次行全部按 0 排到底，
// 可它们的人民币价明明有效。用户按这一列排序时按次模型不会出现在该在的位置，
// 而界面上"第 1 行比第 2 行便宜"看着就是错的。
func TestPriceSortCnyInputIncludesPerCallRows(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedAvgTokens(t, conn, 1000) // 平均 1000 token/次 = 0.001 M/次

	// 三个按量行，人民币输入价分别为 2 / 1 / 1.5（美元 14 / 7 / 10.5 ÷ 7）。
	for i, usd := range []float64{14, 7, 10.5} {
		seedPriceRow(t, conn, model.PriceSnapshot{
			Site: "m.example", Model: fmt.Sprintf("metered-%d", i), Multiplier: 1,
			BillingMode: "token", InputPrice: usd,
		})
	}
	// 一个按次行：$0.021/次 ÷ 7 = ¥0.003/次，再 ÷ 0.001 M/次 = ¥3/1M
	// 它比所有按量行都贵，应排第 0；旧实现按 input_price（按次行为 0）会把它排到最后。
	// 刻意避开与按量行平局：平局时两行顺序由数据库决定，判据会间歇性通过。
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "c.example", Model: "percall-mid", Multiplier: 1,
		BillingMode: "per_call", PerCallPrice: 0.021,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{Column: "cny_input", Descending: true})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("应返回 4 行，实得 %d", len(rows))
	}

	var perCallIndex = -1
	var perCallCny float64
	for i, row := range rows {
		if row.BillingMode == "per_call" {
			perCallIndex = i
			perCallCny = row.CnyInputPrice
		}
	}
	if perCallIndex < 0 {
		t.Fatal("没找到按次行")
	}
	if perCallCny != 3 {
		t.Fatalf("按次行的人民币输入价应为 3，实得 %v", perCallCny)
	}
	// 降序下 3 是最贵的，应排第 0；若排序键用了 input_price（按次行为 0），它会排到最后。
	if perCallIndex != 0 {
		t.Errorf("按次行人民币价 3 是全场最贵，降序应排第 0，实得第 %d", perCallIndex)
	}

	// 整体必须单调不增：排序键与显示值一致的最低要求。
	for i := 1; i < len(rows); i++ {
		if rows[i-1].CnyInputPrice < rows[i].CnyInputPrice-1e-9 {
			t.Errorf("降序不单调：第 %d 行 %v > 第 %d 行 %v",
				i-1, rows[i-1].CnyInputPrice, i, rows[i].CnyInputPrice)
		}
	}
}

// 样本不足时按次行折不出价，排序里按 0 处理（与界面显示「—」一致：没有值就不占位置）。
func TestPriceSortCnyInputPerCallWithoutSamplesSortsLast(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "m.example", Model: "metered-only", Multiplier: 1,
		BillingMode: "token", InputPrice: 14,
	})
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "c.example", Model: "percall-nosample", Multiplier: 1,
		BillingMode: "per_call", PerCallPrice: 0.021,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{Column: "cny_input", Descending: true})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	last := rows[len(rows)-1]
	if last.BillingMode != "per_call" {
		t.Errorf("样本不足的按次行应排最后（没有值就不占位置），实得 %s", last.Model)
	}
	if last.CnyInputPrice != 0 {
		t.Errorf("该行人民币价应为 0，实得 %v", last.CnyInputPrice)
	}
}

// cny_output 列**不**为按次行折算，排序键因此与显示值同为 0。
//
// 这条守着另一个方向的不一致：按次站点只给一个"每次多少钱"，没有输入/输出之分，
// unifyPriceRow 只填 CnyInputPrice。若排序却按 per_call_price 折算 output，
// 就会出现排序键有值、显示值是 0 —— 用户看到 0 排到有值行前面去。
// 两列的 CASE WHEN 各写一遍，所以要为两个方向各配一个用例（守卫互相掩盖）。
func TestPriceSortCnyOutputDoesNotFabricatePerCallValue(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedAvgTokens(t, conn, 1000)
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "m.example", Model: "metered-cheap", Multiplier: 1,
		BillingMode: "token", OutputPrice: 7, // ¥1
	})
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "c.example", Model: "percall-pricey", Multiplier: 1,
		BillingMode: "per_call", PerCallPrice: 0.07,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{Column: "cny_output", Descending: true})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	// 按次行的人民币输出价是 0（没有这个口径），降序应排最后。
	last := rows[len(rows)-1]
	if last.BillingMode != "per_call" {
		t.Errorf("按次行的人民币输出价为 0，应排最后，实得 %s", last.Model)
	}
	if last.CnyOutputPrice != 0 {
		t.Errorf("按次行的人民币输出价应为 0，实得 %v", last.CnyOutputPrice)
	}
	// 单调性：排序键与显示值一致的最低要求。
	for i := 1; i < len(rows); i++ {
		if rows[i-1].CnyOutputPrice < rows[i].CnyOutputPrice-1e-9 {
			t.Errorf("降序不单调：第 %d 行 %v > 第 %d 行 %v",
				i-1, rows[i-1].CnyOutputPrice, i, rows[i].CnyOutputPrice)
		}
	}
}

func TestPriceUnifyMeteredConvertsAllThreePrices(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "a.example", Model: "m1", Multiplier: 1, BillingMode: "metered",
		InputPrice: 14, OutputPrice: 21, CacheReadPrice: 3.5,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("应返回 1 行，实得 %d", len(rows))
	}
	row := rows[0]
	if row.CnyInputPrice != 2 || row.CnyOutputPrice != 3 || row.CnyCacheReadPrice != 0.5 {
		t.Errorf("人民币价 = 入%v/出%v/缓存%v，want 2/3/0.5（美元价 ÷ 7）",
			row.CnyInputPrice, row.CnyOutputPrice, row.CnyCacheReadPrice)
	}
	if row.Basis != "" {
		t.Errorf("按量计费不该有特殊说明，实得 %q", row.Basis)
	}
}

// 按次计费：必须用"平均每次请求 token 数"折成每 1M token 价。
// 这是整个需求的核心 —— 不折的话两类计费永远没法并排看。
func TestPriceUnifyPerCallUsesAverageTokens(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedAvgTokens(t, conn, 1000) // 平均 1000 token/次
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "b.example", Model: "m2", Multiplier: 1, BillingMode: "per_call",
		PerCallPrice: 0.07, // $0.07/次
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	row := rows[0]
	// $0.07 ÷ 7 = ¥0.01/次；1000 token/次 = 0.001 M/次
	// ⇒ ¥0.01 / 0.001 = ¥10/1M token
	if row.CnyInputPrice != 10 {
		t.Errorf("按次折算的人民币每 1M token 价 = %v，want 10", row.CnyInputPrice)
	}
	if row.PerCallPrice != 0.07 {
		t.Errorf("原始按次价应一并回带（供核对），实得 %v", row.PerCallPrice)
	}
	if row.AvgTokensPerRequest != 1000 {
		t.Errorf("分母必须随行下发，实得 %d want 1000", row.AvgTokensPerRequest)
	}
	if row.Basis == "" {
		t.Error("按次折算必须说明依据（ Basis 为空则用户无法核对）")
	}
}

// 汇率没配（=0）时一个数都不折：除 0 会得到 +Inf，
// 界面显示成一串无穷大比不显示更糟。
func TestPriceUnifySkippedWhenRateUnset(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "0")
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "c.example", Model: "m3", Multiplier: 1, BillingMode: "metered",
		InputPrice: 14, OutputPrice: 21,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	row := rows[0]
	if row.CnyInputPrice != 0 || row.CnyOutputPrice != 0 {
		t.Errorf("未配汇率时应全部留 0，实得 入%v 出%v", row.CnyInputPrice, row.CnyOutputPrice)
	}
	// 原币种价必须仍在：折不了尺子也不能把原始信息弄丢。
	if row.InputPrice != 14 {
		t.Errorf("原币种价应保留，实得 %v", row.InputPrice)
	}
}

// 分母不足（没有真实请求）时按次价折不出每 token 价：留 0 并说明原因，
// 而不是拿一个小样本硬算 —— 那看起来是个结论，实际不是。
func TestPriceUnifyPerCallWithoutSamplesLeavesEmpty(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	seedPriceRow(t, conn, model.PriceSnapshot{
		Site: "d.example", Model: "m4", Multiplier: 1, BillingMode: "per_call",
		PerCallPrice: 0.07,
	})

	rows, err := PriceModelList(t.Context(), "", PriceModelSort{})
	if err != nil {
		t.Fatalf("PriceModelList: %v", err)
	}
	row := rows[0]
	if row.CnyInputPrice != 0 {
		t.Errorf("样本不足时不应折出价格，实得 %v", row.CnyInputPrice)
	}
	if row.Basis == "" {
		t.Error("必须说明为什么没折（否则用户以为功能坏了）")
	}
}

// 排序：白名单内的列要真的排，且方向可控；白名单外回落默认序而不报错。
func TestPriceModelSortWhitelistAndFallback(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	setPriceRate(t, "7")
	for i, price := range []float64{5, 1, 9} {
		seedPriceRow(t, conn, model.PriceSnapshot{
			Site: "s.example", Model: fmt.Sprintf("m%d", i), Multiplier: 1,
			BillingMode: "metered", InputPrice: price,
		})
	}

	desc, err := PriceModelList(t.Context(), "", PriceModelSort{Column: "input", Descending: true})
	if err != nil {
		t.Fatalf("PriceModelList desc: %v", err)
	}
	if desc[0].InputPrice != 9 || desc[2].InputPrice != 1 {
		t.Errorf("降序应为 9,5,1，实得 %v,%v,%v", desc[0].InputPrice, desc[1].InputPrice, desc[2].InputPrice)
	}

	asc, err := PriceModelList(t.Context(), "", PriceModelSort{Column: "input", Descending: false})
	if err != nil {
		t.Fatalf("PriceModelList asc: %v", err)
	}
	if asc[0].InputPrice != 1 {
		t.Errorf("升序首个应为 1，实得 %v", asc[0].InputPrice)
	}

	// 非法列名不报错，回落到默认序（model asc）。
	fallback, err := PriceModelList(t.Context(), "", PriceModelSort{Column: "id; DROP TABLE price_snapshots", Descending: true})
	if err != nil {
		t.Fatalf("非法列名应回落而不报错: %v", err)
	}
	if len(fallback) != 3 {
		t.Errorf("回落后应仍返回 3 行，实得 %d", len(fallback))
	}
}

// 导入：按次价要落库，且上游没给（nil）时必须是 0 而不是 panic。
func TestPriceImportPerCallPriceAndNil(t *testing.T) {
	conn := openPriceUnifyTestDB(t)
	perCall := 0.05
	payload := []byte(`{"site":"e.example","plaza":{"groups":[
		{"name":"g1","rate_multiplier":2,"models":[
			{"name":"per-call-model","pricing":{"billing_mode":"per_call","per_request_price":0.05}},
			{"name":"metered-model","pricing":{"billing_mode":"metered","input_price":0.001}}
		]}
	]}}`)
	_ = perCall
	if _, _, err := PriceSnapshotImport(t.Context(), payload); err != nil {
		t.Fatalf("PriceSnapshotImport: %v", err)
	}

	var perCallRow model.PriceSnapshot
	if err := conn.Where("model = ?", "per-call-model").First(&perCallRow).Error; err != nil {
		t.Fatalf("load per-call row: %v", err)
	}
	// 0.05 × 倍率 2 = 0.1
	if perCallRow.PerCallPrice != 0.1 {
		t.Errorf("按次价应乘倍率落库 = %v，want 0.1", perCallRow.PerCallPrice)
	}

	var meteredRow model.PriceSnapshot
	if err := conn.Where("model = ?", "metered-model").First(&meteredRow).Error; err != nil {
		t.Fatalf("load metered row: %v", err)
	}
	if meteredRow.PerCallPrice != 0 {
		t.Errorf("按量模型的按次价应为 0（上游给 null），实得 %v", meteredRow.PerCallPrice)
	}
}
