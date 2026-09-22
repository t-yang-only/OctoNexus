package op

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 用量报告的组装口径（吸收上游 Usage Reports）。
//
// 这套用例里最重要的一条是 **日期格式**：stats_dailies.date 存的是 `20060102`（无分隔符），
// 而窗口边界算出来的是 time.Time。本轮实际踩过——用 `2006-01-02` 去比会让**每一行都被跳过**，
// 报告恒为空、不报错、面板上只是"没有数据"，非常难发现。
// 因此下面用"种一行已知数据 → 断言被算进报告"来锁死它：格式写错时这条必然变红。

func setupUsageReportDB(t *testing.T) {
	t.Helper()
	setupModelMappingDB(t) // 复用同一个共享库（含 sqlite 初始化）
	if err := db.GetDB().AutoMigrate(&model.StatsDaily{}, &model.UsageHourly{}, &model.UsageReportState{}); err != nil {
		t.Fatalf("migrate usage report tables: %v", err)
	}
}

func clearUsageReportData(t *testing.T) {
	t.Helper()
	for _, table := range []any{&model.StatsDaily{}, &model.UsageHourly{}, &model.UsageReportState{}} {
		if err := db.GetDB().Where("1 = 1").Delete(table).Error; err != nil {
			t.Fatalf("clear %T: %v", table, err)
		}
	}
}

// 种一行逐日统计并断言它被算进报告（同时锁死 20060102 的日期格式）。
func TestUsageReportSumsDailyStatsInWindow(t *testing.T) {
	setupUsageReportDB(t)
	clearUsageReportData(t)
	ctx := context.Background()

	at, err := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 09:00", time.Local)
	if err != nil {
		t.Fatalf("parse at: %v", err)
	}
	// 日报窗口 = [2026-09-21, 2026-09-22)，所以这一行必须落在 09-21。
	row := model.StatsDaily{Date: "20260921"}
	row.InputToken = 1_000_000
	row.OutputToken = 250_000
	row.InputCost = 1.5
	row.OutputCost = 0.5
	row.RequestSuccess = 90
	row.RequestFailed = 10
	row.WaitTime = 100_000
	if err := db.GetDB().WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed daily: %v", err)
	}

	report, err := UsageReportBuild(ctx, model.UsageReportDaily, at)
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if report.Requests != 100 {
		t.Fatalf("请求数应为 90+10=100，实得 %d（日期格式对不上会让它恒为 0）", report.Requests)
	}
	if report.Success != 90 || report.Failed != 10 {
		t.Fatalf("成功/失败应为 90/10，实得 %d/%d", report.Success, report.Failed)
	}
	if report.InputToken != 1_000_000 || report.OutputToken != 250_000 {
		t.Fatalf("词元应为 1M/250K，实得 %d/%d", report.InputToken, report.OutputToken)
	}
	if diff := report.Cost - 2.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("费用应为 2.0，实得 %v", report.Cost)
	}
	if diff := report.SuccessRate - 90.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("成功率应为 90%%，实得 %v", report.SuccessRate)
	}
	if report.Empty {
		t.Fatalf("有数据时 Empty 不该为 true")
	}
}

// 窗口外的行不得被算进来：否则周报会把上上周的数字一起加进去。
func TestUsageReportExcludesRowsOutsideWindow(t *testing.T) {
	setupUsageReportDB(t)
	clearUsageReportData(t)
	ctx := context.Background()

	at, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 09:00", time.Local)
	inside := model.StatsDaily{Date: "20260921"}
	inside.RequestSuccess = 5
	// 同一天但更早（09-20）与更晚（09-22，属于今天）都必须排除。
	before := model.StatsDaily{Date: "20260920"}
	before.RequestSuccess = 1000
	today := model.StatsDaily{Date: "20260922"}
	today.RequestSuccess = 2000
	for _, row := range []*model.StatsDaily{&inside, &before, &today} {
		if err := db.GetDB().WithContext(ctx).Create(row).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	report, err := UsageReportBuild(ctx, model.UsageReportDaily, at)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if report.Requests != 5 {
		t.Fatalf("只该统计窗口内的 5 次，实得 %d（窗口边界写错会把别的天算进来）", report.Requests)
	}
}

// 明细排行按请求数倒序，且最多 5 条。
func TestUsageReportTopListsAreSortedAndBounded(t *testing.T) {
	setupUsageReportDB(t)
	clearUsageReportData(t)
	ctx := context.Background()

	at, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 09:00", time.Local)
	// usage_hourlies.hour 格式 2006010215；窗口 [09-21 00:00, 09-22 00:00)。
	seed := func(modelName string, count int64) {
		row := model.UsageHourly{Hour: "2026092112", ModelName: modelName, ChannelName: "ch-" + modelName}
		row.RequestSuccess = count
		if err := db.GetDB().WithContext(ctx).Create(&row).Error; err != nil {
			t.Fatalf("seed %s: %v", modelName, err)
		}
	}
	seed("m-small", 1)
	seed("m-big", 500)
	seed("m-mid", 50)
	seed("m-x1", 40)
	seed("m-x2", 30)
	seed("m-x3", 20)
	seed("m-x4", 10)

	report, err := UsageReportBuild(ctx, model.UsageReportDaily, at)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(report.TopModels) != 5 {
		t.Fatalf("模型排行应最多 5 条，实得 %d", len(report.TopModels))
	}
	if report.TopModels[0].Name != "m-big" || report.TopModels[0].Count != 500 {
		t.Fatalf("排行第一应是 m-big/500，实得 %s/%d", report.TopModels[0].Name, report.TopModels[0].Count)
	}
	if report.TopModels[1].Name != "m-mid" {
		t.Fatalf("排行第二应是 m-mid，实得 %s", report.TopModels[1].Name)
	}
	if len(report.TopChannels) != 5 {
		t.Fatalf("渠道排行应最多 5 条，实得 %d", len(report.TopChannels))
	}
}

// 空周期必须给可读的一句话，而不是一堆 0；正文里必须带上余额与数据截止时间。
func TestUsageReportEmptyWindowRendersReadableText(t *testing.T) {
	setupUsageReportDB(t)
	clearUsageReportData(t)
	ctx := context.Background()

	at, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 09:00", time.Local)
	report, err := UsageReportBuild(ctx, model.UsageReportDaily, at)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !report.Empty {
		t.Fatalf("没有数据时 Empty 应为 true")
	}
	if !strings.Contains(report.Text, "没有请求记录") {
		t.Fatalf("空周期正文应说明没有请求，实得:\n%s", report.Text)
	}
	if !strings.Contains(report.Text, "余额") {
		t.Fatalf("正文应包含余额，实得:\n%s", report.Text)
	}
	if !strings.Contains(report.Text, "2026-09-21") {
		t.Fatalf("正文应标出周期标签，实得:\n%s", report.Text)
	}
	// 没扫到余额时必须说"未知"，绝不能印 0.00（那会被看成"没钱了"，T-balance-001 口径）。
	if report.BalanceKnown {
		t.Fatalf("本用例没有渠道余额，BalanceKnown 应为 false")
	}
	if strings.Contains(report.Text, "0.00") {
		t.Fatalf("余额未知时不该印 0.00:\n%s", report.Text)
	}
	if !strings.Contains(report.Text, "未知") {
		t.Fatalf("余额未知时正文应写明未知:\n%s", report.Text)
	}
}

// 有数据时正文要包含请求数、费用与排行（这是用户真正会读的部分）。
func TestUsageReportNonEmptyTextHasNumbers(t *testing.T) {
	setupUsageReportDB(t)
	clearUsageReportData(t)
	ctx := context.Background()

	at, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 09:00", time.Local)
	row := model.StatsDaily{Date: "20260921"}
	row.RequestSuccess = 42
	row.RequestFailed = 8
	row.InputCost = 1.25
	row.OutputCost = 0.25
	if err := db.GetDB().WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	detail := model.UsageHourly{Hour: "2026092112", ModelName: "gpt-test", ChannelName: "ch-a"}
	detail.RequestSuccess = 42
	if err := db.GetDB().WithContext(ctx).Create(&detail).Error; err != nil {
		t.Fatalf("seed detail: %v", err)
	}

	report, err := UsageReportBuild(ctx, model.UsageReportDaily, at)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	text := report.Text
	for _, want := range []string{"请求 50 次", "成功率 84.0%", "1.5000", "gpt-test", "ch-a"} {
		if !strings.Contains(text, want) {
			t.Fatalf("正文缺少 %q:\n%s", want, text)
		}
	}
}

// 明细覆盖范围小于总计时必须如实说明，否则"总数一万多、排行只有 3 次"会被当成 bug。
func TestUsageReportNotesPartialDetailCoverage(t *testing.T) {
	setupUsageReportDB(t)
	clearUsageReportData(t)
	ctx := context.Background()

	at, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 09:00", time.Local)
	// 总计 1000 次，但明细桶只覆盖 3 次。
	row := model.StatsDaily{Date: "20260921"}
	row.RequestSuccess = 1000
	if err := db.GetDB().WithContext(ctx).Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	detail := model.UsageHourly{Hour: "2026092112", ModelName: "m", ChannelName: "c"}
	detail.RequestSuccess = 3
	if err := db.GetDB().WithContext(ctx).Create(&detail).Error; err != nil {
		t.Fatalf("seed detail: %v", err)
	}

	report, err := UsageReportBuild(ctx, model.UsageReportDaily, at)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if report.DetailRequests != 3 {
		t.Fatalf("明细覆盖应为 3，实得 %d", report.DetailRequests)
	}
	if !strings.Contains(report.Text, "覆盖 3 次") {
		t.Fatalf("明细覆盖少于总数时正文应说明，实得:\n%s", report.Text)
	}

	// 明细覆盖完整时不该出现这句（避免无谓噪音）。
	detail2 := model.UsageHourly{Hour: "2026092113", ModelName: "m", ChannelName: "c"}
	detail2.RequestSuccess = 997
	if err := db.GetDB().WithContext(ctx).Create(&detail2).Error; err != nil {
		t.Fatalf("seed detail2: %v", err)
	}
	full, err := UsageReportBuild(ctx, model.UsageReportDaily, at)
	if err != nil {
		t.Fatalf("build full: %v", err)
	}
	if full.DetailRequests != 1000 {
		t.Fatalf("明细覆盖应为 1000，实得 %d", full.DetailRequests)
	}
	if strings.Contains(full.Text, "覆盖") && strings.Contains(full.Text, "小时明细桶") {
		t.Fatalf("明细完整时不该出现覆盖说明:\n%s", full.Text)
	}
}

// 大数字要缩写成 K/M/B（通知渠道里一眼可读，不必数零）。
func TestHumanCountShortensLargeNumbers(t *testing.T) {
	cases := map[int64]string{
		999:           "999",
		1_000:         "1.00K",
		1_500_000:     "1.50M",
		2_000_000_000: "2.00B",
	}
	for input, want := range cases {
		if got := humanCount(input); got != want {
			t.Fatalf("humanCount(%d) = %s, want %s", input, got, want)
		}
	}
}

// UsageReportSent：没记过 = 没发过；记过 = 发过。
// 查库出错时按"已发过"处理，避免重复打扰（宁可漏发，用户可手动补发）。
func TestUsageReportSentReflectsRecordedState(t *testing.T) {
	setupUsageReportDB(t)
	clearUsageReportData(t)
	ctx := context.Background()

	if UsageReportSent(ctx, "2026-09-22") {
		t.Fatalf("没记过时不该判为已发")
	}
	if UsageReportSent(ctx, "") {
		t.Fatalf("空键不该判为已发（否则会静默跳过所有发送）")
	}
	if err := UsageReportMarkSent(ctx, UsageReport{PeriodKey: "2026-09-22", Period: "daily", Text: "首行\n第二行"}, 1, 0, time.Now()); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if !UsageReportSent(ctx, "2026-09-22") {
		t.Fatalf("记过之后应判为已发")
	}
	// Summary 取首行，便于面板一行展示。
	history := UsageReportHistory(ctx, 5)
	if len(history) != 1 || history[0].Summary != "首行" {
		t.Fatalf("history 应含一条且摘要取首行: %+v", history)
	}
}

// UsageReportDue：开关、时刻、周期记账三个条件缺一不可。
func TestUsageReportDueRequiresAllConditions(t *testing.T) {
	setupUsageReportDB(t)
	clearUsageReportData(t)
	ctx := context.Background()

	at, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 09:00", time.Local)
	// 设置项直接写内存缓存（UsageReportDue 就是经 SettingGet* 读缓存的），
	// 收尾清掉，避免影响同包其它用例。
	set := func(key model.SettingKey, value string) {
		t.Helper()
		SettingSetStringForTest(key, value, true)
	}
	t.Cleanup(func() {
		for _, key := range []model.SettingKey{
			model.SettingKeyUsageReportEnabled,
			model.SettingKeyUsageReportPeriod,
			model.SettingKeyUsageReportHour,
		} {
			SettingSetStringForTest(key, "", false)
		}
	})

	set(model.SettingKeyUsageReportEnabled, "false")
	set(model.SettingKeyUsageReportPeriod, "daily")
	set(model.SettingKeyUsageReportHour, "9")
	if _, _, due := UsageReportDue(ctx, at); due {
		t.Fatalf("开关关闭时不该发")
	}

	set(model.SettingKeyUsageReportEnabled, "true")
	if _, _, due := UsageReportDue(ctx, at); !due {
		t.Fatalf("开关打开且整点吻合时应发")
	}

	// 时刻不吻合。
	other, _ := time.ParseInLocation("2006-01-02 15:04", "2026-09-22 10:00", time.Local)
	if _, _, due := UsageReportDue(ctx, other); due {
		t.Fatalf("非配置时刻不该发")
	}

	// 本周期已发过。
	if err := UsageReportMarkSent(ctx, UsageReport{PeriodKey: "2026-09-22", Period: "daily", Text: "x"}, 1, 0, at); err != nil {
		t.Fatalf("mark: %v", err)
	}
	if _, _, due := UsageReportDue(ctx, at); due {
		t.Fatalf("本周期已发过时不该再发")
	}

	// 非法周期值不得让任务误发。
	set(model.SettingKeyUsageReportPeriod, "yearly")
	if _, _, due := UsageReportDue(ctx, other); due {
		t.Fatalf("非法周期值不该触发发送")
	}
}
