package op

import (
	"bytes"
	"context"
	"encoding/csv"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-trace-006 测试请求标记：让验证流量不进画像，同时**不改变窗口边界**。

// seedWindowLog 播一条日志，isTest 由调用方显式给定（被测维度）。
//
// 不往既有 seed 装置上加字段：那些装置的形状对应各自主题，共用一个"万能播种器"
// 会让每个用例都带着一堆与它无关的默认值，改动时互相牵连。
func seedWindowLog(t *testing.T, conn *gorm.DB, requestID int, isTest bool) {
	t.Helper()
	row := model.RelayLog{
		RequestID:     uint64(requestID),
		Status:        "success",
		Model:         "probe-group",
		TargetChannel: "probe-channel",
		IsTest:        isTest,
		StartedAt:     time.Now(),
		DurationMs:    120,
		FirstByteMs:   40,
	}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatalf("seed window log %d: %v", requestID, err)
	}
}

// windowSeedDB 建库并把 RelayLog / Group / GroupItem 都迁出来（group_latency 要查后两张）。
func windowSeedDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn := withAnalyticsDB(t)
	if err := conn.AutoMigrate(&model.Group{}, &model.GroupItem{}); err != nil {
		t.Fatalf("migrate group tables: %v", err)
	}
	return conn
}

// TestRelayWindowKeepsWindowBoundaryWhenSkippingTests 是这一族的**核心判据**。
//
// 剔除测试请求时，窗口边界必须保持"最近 N 条日志"，而不是"一路往下取满 N 条非测试日志"。
// 两者在实现上只差一个 WHERE，后果却完全不同：后者会让窗口随测试请求的比例悄悄变长，
// 于是"排除测试请求前后"的两次统计样本范围根本不可比 —— 而这个功能存在的全部意义
// 恰恰是让两次统计可比。用 SQL WHERE 的实现（参考项目的做法）会在这里变红。
func TestRelayWindowKeepsWindowBoundaryWhenSkippingTests(t *testing.T) {
	conn := windowSeedDB(t)
	// 最近三条都是测试请求：窗口=5 应当只落到 id 6..10，剔除后剩 id 6、7 两条。
	for id := 1; id <= 7; id++ {
		seedWindowLog(t, conn, id, false)
	}
	for id := 8; id <= 10; id++ {
		seedWindowLog(t, conn, id, true)
	}

	rows, sample, err := relayLogWindow(context.Background(), 5)
	if err != nil {
		t.Fatalf("relayLogWindow: %v", err)
	}

	if sample.Window != 5 {
		t.Fatalf("窗口原始条数应为 5（最近 5 条日志），实得 %d —— 剔除测试请求不得改变窗口边界", sample.Window)
	}
	if sample.TestSkipped != 3 {
		t.Fatalf("应剔除 3 条测试请求，实得 %d", sample.TestSkipped)
	}
	if sample.Samples != 2 || int64(len(rows)) != 2 {
		t.Fatalf("样本应为 2 条，实得 Samples=%d len=%d", sample.Samples, len(rows))
	}
	for _, row := range rows {
		if row.IsTest {
			t.Fatalf("测试请求 id=%d 不应出现在样本里", row.RequestID)
		}
		// 窗口边界：window=5 只该看到 id 6..10。改用 SQL WHERE 剔除的实现会一路
		// 往下取到 5 条非测试日志（把 id 3、4、5 也拉进样本），这条立刻报出来 ——
		// 它是本用例的核心断言，比"剔除了几条"更早暴露问题。
		if row.RequestID < 6 {
			t.Fatalf("样本落到了窗口之外（request_id=%d）—— 剔除测试请求时把窗口撑大了", row.RequestID)
		}
	}
}

// TestRelayWindowReportsSampleAccounting 钉住三态数字彼此自洽（Samples = Window - TestSkipped）。
func TestRelayWindowReportsSampleAccounting(t *testing.T) {
	conn := windowSeedDB(t)
	for id := 1; id <= 6; id++ {
		seedWindowLog(t, conn, id, id%2 == 0) // 偶数 id 是测试请求：3 条
	}

	rows, sample, err := relayLogWindow(context.Background(), 100)
	if err != nil {
		t.Fatalf("relayLogWindow: %v", err)
	}
	if sample.Window != 6 || sample.TestSkipped != 3 || sample.Samples != 3 {
		t.Fatalf("记账应为 Window=6 TestSkipped=3 Samples=3，实得 %+v", sample)
	}
	if sample.Window != sample.Samples+sample.TestSkipped {
		t.Fatalf("自洽式不成立：%+v", sample)
	}
	if sample.Truncated {
		t.Fatalf("窗口远大于行数时不应报触限：%+v", sample)
	}
	if int64(len(rows)) != sample.Samples {
		t.Fatalf("len(rows)=%d 与 Samples=%d 不一致", len(rows), sample.Samples)
	}
}

// TestRelayWindowClampsAndKeepsEmptySlice 覆盖边界：默认值、上限夹回、空窗口仍然是非 nil。
//
// 空窗口返回非 nil 空切片，是为了让 JSON 序列化成 [] 而不是 null —— 后者在前端
// 会逼出额外的判空分支，且"没有数据"与"字段缺失"看起来一样。
func TestRelayWindowClampsAndKeepsEmptySlice(t *testing.T) {
	conn := windowSeedDB(t)

	rows, sample, err := relayLogWindow(context.Background(), 0)
	if err != nil {
		t.Fatalf("relayLogWindow(0): %v", err)
	}
	if rows == nil {
		t.Fatalf("空窗口必须返回非 nil 空切片")
	}
	if sample.Window != 0 {
		t.Fatalf("空库窗口应为 0，实得 %d", sample.Window)
	}

	// 夹回上限：播少量行，请求一个远超上限的窗口，Truncated 判定不能因此说谎。
	seedWindowLog(t, conn, 1, false)
	_, sample, err = relayLogWindow(context.Background(), 99999)
	if err != nil {
		t.Fatalf("relayLogWindow(99999): %v", err)
	}
	if sample.Window != 1 || sample.Truncated {
		t.Fatalf("1 行数据对上限窗口不应触限：%+v", sample)
	}
}

// TestAllProfilesShareOneSampleWindow 钉住"七个画像都经收口取数"。
//
// 这条判据防的是**将来**：新增画像若自己写一遍查询、或某个画像在改动中被漏掉，
// 它的 TestSkipped 会是 0 而别的都是播种数 —— 口径分叉在界面上只表现为"某个面板
// 的数字和别的对不上"，没有这条判据就只能靠肉眼比对。
func TestAllProfilesShareOneSampleWindow(t *testing.T) {
	conn := windowSeedDB(t)
	const testRows = 4
	for id := 1; id <= 20; id++ {
		seedWindowLog(t, conn, id, id <= testRows)
	}
	group := model.Group{Name: "probe-group"}
	if err := conn.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	ctx := context.Background()
	const window = 100

	overview, err := AnalyticsOverviewStats(ctx, window)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	latency, err := LatencyDistributionStats(ctx, window)
	if err != nil {
		t.Fatalf("LatencyDistributionStats: %v", err)
	}
	routing, err := RoutingProfileStats(ctx, window)
	if err != nil {
		t.Fatalf("RoutingProfileStats: %v", err)
	}
	attempts, err := AttemptChainStats(ctx, window)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	channels, err := ChannelLatencyStats(ctx, window)
	if err != nil {
		t.Fatalf("ChannelLatencyStats: %v", err)
	}
	groups, err := GroupLatencyStats(ctx, window)
	if err != nil {
		t.Fatalf("GroupLatencyStats: %v", err)
	}
	faults, err := RelayLogFaultsStats(ctx, window)
	if err != nil {
		t.Fatalf("RelayLogFaultsStats: %v", err)
	}

	cases := []struct {
		name   string
		sample RelayLogSampleInfo
	}{
		{"AnalyticsOverview", overview.Sample},
		{"LatencyDistribution", latency.Sample},
		{"RoutingProfile", routing.Sample},
		{"AttemptChain", attempts.Sample},
		{"ChannelLatency", channels.Sample},
		{"GroupLatency", groups.Sample},
		{"RelayLogFaults", faults.Sample},
	}
	for _, item := range cases {
		if item.sample.TestSkipped != testRows {
			t.Fatalf("%s 的 TestSkipped=%d，应为 %d —— 该画像没有经 relayLogWindow 取数",
				item.name, item.sample.TestSkipped, testRows)
		}
		if item.sample.Samples != 20-testRows {
			t.Fatalf("%s 的 Samples=%d，应为 %d", item.name, item.sample.Samples, 20-testRows)
		}
	}
}

// TestRelayLogFilterIsTestThreeStates 覆盖日志页过滤的三态，重点是 false 分支要连 NULL 一起命中。
//
// 存量行在库里是 NULL（这一列是升级时才加的）。若 false 分支写成 "is_test = ?"，
// 那些行两个分支都不匹配 —— 表现为"一筛选就凭空少了几百条"，而且不报任何错。
func TestRelayLogFilterIsTestThreeStates(t *testing.T) {
	conn := windowSeedDB(t)
	seedWindowLog(t, conn, 1, false)
	seedWindowLog(t, conn, 2, true)
	seedWindowLog(t, conn, 3, false)
	if err := conn.Exec("UPDATE relay_logs SET is_test = NULL WHERE request_id = ?", 3).Error; err != nil {
		t.Fatalf("simulate legacy row: %v", err)
	}

	if _, total := RelayLogList(model.RelayLogFilter{Limit: 50}); total != 3 {
		t.Fatalf("不筛该维度时应看到全部 3 条，实得 %d", total)
	}

	onlyTest := true
	if _, total := RelayLogList(model.RelayLogFilter{Limit: 50, IsTest: &onlyTest}); total != 1 {
		t.Fatalf("只看测试请求应为 1 条，实得 %d", total)
	}

	onlyReal := false
	onlyRealRows, onlyRealTotal := RelayLogList(model.RelayLogFilter{Limit: 50, IsTest: &onlyReal})
	if onlyRealTotal != 2 {
		t.Fatalf("只看非测试请求应为 2 条（含 is_test 为 NULL 的存量行），实得 %d", onlyRealTotal)
	}
	for _, row := range onlyRealRows {
		if row.IsTest {
			t.Fatalf("非测试分支里出现了测试请求 request_id=%d", row.RequestID)
		}
	}
}

// TestRelayLogExportMarksTestRequests 覆盖导出列：两态文本 + 列位置在最后（不移动既有列）。
func TestRelayLogExportMarksTestRequests(t *testing.T) {
	conn := windowSeedDB(t)
	seedWindowLog(t, conn, 1, false)
	seedWindowLog(t, conn, 2, true)

	var buf bytes.Buffer
	if _, err := relayLogExportCSVOn(conn, &buf, model.RelayLogFilter{Limit: 50}); err != nil {
		t.Fatalf("export: %v", err)
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(buf.String(), "\uFEFF"))).ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(records) != 3 {
		t.Fatalf("应有表头 + 2 行，实得 %d", len(records))
	}

	header := records[0]
	if header[len(header)-1] != "测试请求" {
		t.Fatalf("测试请求列必须在最后一列（中间插列会让下游按列号解析的脚本静默错位），实得末列 %q", header[len(header)-1])
	}
	// 导出按 id 正序：第 1 条非测试、第 2 条测试。
	if got := records[1][len(header)-1]; got != "否" {
		t.Fatalf("非测试请求导出应为「否」，实得 %q", got)
	}
	if got := records[2][len(header)-1]; got != "是" {
		t.Fatalf("测试请求导出应为「是」，实得 %q", got)
	}
	// 列数与表头一致（列对不上时 CSV 会静默错位到下一行）。
	for index, row := range records[1:] {
		if len(row) != len(header) {
			t.Fatalf("第 %d 行有 %d 列，表头 %d 列", index+1, len(row), len(header))
		}
	}
}

// TestRelayWindowIgnoresStatusAndFaultKind 反向对照：收口函数**不按状态或归因筛样本**。
//
// 失败请求必须留在样本里（一次 60 秒超时正是最该被看见的慢），这条判据钉住
// "收口函数只剔除测试请求这一件事"，防止有人顺手加上"只统计成功"。
func TestRelayWindowIgnoresStatusAndFaultKind(t *testing.T) {
	conn := windowSeedDB(t)
	rows := []model.RelayLog{
		{RequestID: 1, Status: "success", TargetChannel: "ch"},
		{RequestID: 2, Status: "failed", TargetChannel: "ch", FaultKind: "transient", Error: "boom"},
		{RequestID: 3, Status: "canceled", TargetChannel: "ch", FirstByteMs: -1},
		{RequestID: 4, Status: "failed", TargetChannel: "ch", IsTest: true},
	}
	for _, row := range rows {
		entry := row
		if err := conn.Create(&entry).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	samples, sample, err := relayLogWindow(context.Background(), 100)
	if err != nil {
		t.Fatalf("relayLogWindow: %v", err)
	}
	if sample.Samples != 3 {
		t.Fatalf("失败与取消的请求必须留在样本里（只该剔除测试请求），Samples=%d", sample.Samples)
	}
	statuses := make([]string, 0, len(samples))
	for _, row := range samples {
		statuses = append(statuses, strconv.Itoa(int(row.RequestID))+":"+row.Status)
	}
	joined := strings.Join(statuses, ",")
	for _, want := range []string{"1:success", "2:failed", "3:canceled"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("样本缺 %s（实得 %s）", want, joined)
		}
	}
}
