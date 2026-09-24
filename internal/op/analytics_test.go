package op

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-insight-002「模型调用分析」的判据。
//
// ## 这一页与既有统计的关系
//
// 故障率按渠道分、耗时按分组分、尝试链按轮次聚合 —— 都是单一维度。
// 这一页回答的是"整体现在怎么样"：发了多少、花了多少、健康线在哪、时间线上谁在吃 token。
//
// ## 每一条判据都对着一个具体的错误写法
//
//  1. RPM/TPM 的分母是**窗口跨度**（首尾请求真实时间差），不是固定 1 小时 ——
//     写死 3600 会让"5 分钟发满 500 条"和"3 天发满 500 条"得到同一个数；
//  2. 吞吐 TPS 的分母是**耗时之和**，不是窗口跨度 —— 两者在本实现里被刻意分开，
//     混用会让吞吐被用户不发请求的空档稀释成无意义的低值；
//  3. 堆叠图尾部模型必须**合并且不丢失**，否则图上总量对不上；
//  4. 空 model 单独归属，绝不并进某个真实模型（否则"模型 X 的用量"凭空多出别人的量）；
//  5. 空窗口返回空切片而不是 nil（nil 序列化成 null，前端遍历会崩）。

type analyticsLogSeed struct {
	status     string
	modelName  string
	startedAt  time.Time
	durationMs int64
	prompt     int64
	completion int64
	cached     int64
	cost       float64
	faultKind  string
}

func seedAnalyticsLog(t *testing.T, conn *gorm.DB, s analyticsLogSeed) {
	t.Helper()
	row := model.RelayLog{
		Status:         s.status,
		Model:          s.modelName,
		StartedAt:      s.startedAt,
		DurationMs:     s.durationMs,
		PromptTokens:   s.prompt,
		CompletionToks: s.completion,
		CachedTokens:   s.cached,
		Cost:           s.cost,
		FaultKind:      s.faultKind,
	}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatalf("seed analytics log: %v", err)
	}
}

// withAnalyticsDB 建一份内存库并挂到全局，测试结束自动还原。
func withAnalyticsDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn := openFaultStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	return conn
}

// 基准时刻固定在整点前 30 分，便于验证"按整点切桶"。
func analyticsBase() time.Time {
	return time.Date(2026, 9, 24, 10, 30, 0, 0, time.Local)
}

// 判据 1：RPM 的分母是首尾请求的真实时间差，不是写死的 1 小时。
//
// 若分母被写成固定 3600s，两条相隔 2 小时的请求会得出 0.0333 RPM，
// 比真实值大 3 倍 —— 这个偏差足以让"要不要扩容"的结论反过来。
func TestAnalyticsRpmUsesActualSpan(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	seedAnalyticsLog(t, conn, analyticsLogSeed{status: "success", modelName: "m", startedAt: base})
	seedAnalyticsLog(t, conn, analyticsLogSeed{status: "success", modelName: "m", startedAt: base.Add(2 * time.Hour)})

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	// 跨度 = 首尾时间差(2h) + 最后一小时的兜底 = 3h
	if out.SpanSeconds < 10790 || out.SpanSeconds > 10810 {
		t.Fatalf("跨度应为首尾时间差加一小时兜底（10800s），实得 %.0f", out.SpanSeconds)
	}
	wantRpm := 2 / (out.SpanSeconds / 60)
	if out.AvgRpm < wantRpm-0.001 || out.AvgRpm > wantRpm+0.001 {
		t.Fatalf("RPM 必须与跨度自洽：期望 %.5f，实得 %.5f", wantRpm, out.AvgRpm)
	}
	// 负向对照：固定 1 小时分母得到 0.0333，这里必须明显更小。
	if out.AvgRpm > 0.02 {
		t.Fatalf("RPM 看起来用了固定分母（%.4f 偏大），真实跨度下应为 %.4f",
			out.AvgRpm, wantRpm)
	}
}

// 判据 2：吞吐按"耗时之和"算，不按窗口跨度算。
//
// 这是本实现里两个分母被刻意分开的地方。用户一天只发两条请求、
// 每条跑 1 秒输出 100 token，真实吞吐是 100 t/s；若拿 25 小时的窗口跨度当分母，
// 会得到 0.002 t/s —— 一个会让用户以为服务不可用的数。
func TestAnalyticsThroughputUsesBusyTimeNotSpan(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "m", startedAt: base,
		durationMs: 1000, completion: 100,
	})
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "m", startedAt: base.Add(24 * time.Hour),
		durationMs: 1000, completion: 100,
	})

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	// 耗时之和 = 2 秒 → 200 / 2 = 100 t/s
	if out.ThroughputTps < 99 || out.ThroughputTps > 101 {
		t.Fatalf("吞吐应按耗时之和计算（期望 100 t/s），实得 %.2f", out.ThroughputTps)
	}
	if out.AvgDurationMs < 999 || out.AvgDurationMs > 1001 {
		t.Fatalf("平均耗时应为 1000ms，实得 %.0f", out.AvgDurationMs)
	}
}

// 判据 3：堆叠图尾部模型合并且不丢失。
//
// 本项目实测 358 个模型，允许全部上色会让图例不可读。
// 但"只保留前 8 个"若写成丢弃，柱状图的总高度会比真实用量矮一截，
// 用户对不上账 —— 所以尾部必须并进 __other__。
func TestAnalyticsSeriesMergesTailModels(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	const n = 12
	for i := 0; i < n; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status:    "success",
			modelName: fmt.Sprintf("model-%02d", i),
			startedAt: base.Add(time.Duration(i) * time.Second),
			prompt:    10,
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Models) != n {
		t.Fatalf("模型明细不该被截断：期望 %d 项，实得 %d", n, len(out.Models))
	}
	if len(out.Series) != 1 {
		t.Fatalf("12 条都在同一小时，应只有 1 个桶，实得 %d", len(out.Series))
	}
	b := out.Series[0]
	if b.Tokens != n*10 {
		t.Fatalf("桶 token 应为 %d，实得 %d", n*10, b.Tokens)
	}
	if len(b.ByModel) > relayAnalyticsTopModels+1 {
		t.Fatalf("堆叠图的模型数应收敛到 %d+1，实得 %d", relayAnalyticsTopModels, len(b.ByModel))
	}
	var sum int64
	for _, v := range b.ByModel {
		sum += v
	}
	if sum != b.Tokens {
		t.Fatalf("堆叠图总量必须守恒：by_model 合计 %d ≠ 桶 token %d（尾部模型被丢弃了）", sum, b.Tokens)
	}
	if b.ByModel[otherModelKey] == 0 {
		t.Fatalf("%d 个模型只保留 %d 个，其余应并进 %s", n, relayAnalyticsTopModels, otherModelKey)
	}
}

// 判据 4：空模型名单独归属，绝不并进某个真实模型。
func TestAnalyticsEmptyModelGetsOwnEntry(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	seedAnalyticsLog(t, conn, analyticsLogSeed{status: "success", modelName: "", startedAt: base, prompt: 100})
	seedAnalyticsLog(t, conn, analyticsLogSeed{status: "success", modelName: "X", startedAt: base, prompt: 5})

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Models) != 2 {
		t.Fatalf("空模型名必须单独成一项，实得 %d 项", len(out.Models))
	}
	byName := make(map[string]ModelUsageStat, len(out.Models))
	for _, stat := range out.Models {
		if stat.Model == "" {
			t.Fatalf("空模型名不该原样出现在明细里（会与真实模型混淆）")
		}
		byName[stat.Model] = stat
	}
	if byName["(未指定)"].PromptTokens != 100 {
		t.Fatalf("空模型名的输入 token 应全额归入 (未指定)，实得 %d", byName["(未指定)"].PromptTokens)
	}
	if byName["X"].PromptTokens != 5 {
		t.Fatalf("真实模型 X 不该被摊到别人的量，实得 %d", byName["X"].PromptTokens)
	}
}

// 判据 5：空窗口返回空切片而不是 nil。
//
// 全新实例一条日志都没有时，报错会让首页显示成"出问题了"；
// 而 nil 切片序列化成 JSON null，前端 .map 会直接崩。
func TestAnalyticsEmptyWindowReturnsEmptySlices(t *testing.T) {
	_ = withAnalyticsDB(t)

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("空表不该报错: %v", err)
	}
	if out.Models == nil || out.Series == nil {
		t.Fatalf("空结果必须是空切片而非 nil（nil 会序列化成 null，前端遍历会崩）")
	}
	if out.Window != 0 || out.RequestCount != 0 || out.Truncated {
		t.Fatalf("空表应得到全 0 结果，实得 window=%d requests=%d truncated=%v",
			out.Window, out.RequestCount, out.Truncated)
	}
	if out.SuccessRate != 0 || out.ChannelRate != 0 {
		t.Fatalf("无数据时通过率必须是 0 而不是 NaN，实得 %.2f/%.2f", out.SuccessRate, out.ChannelRate)
	}
}

// 判据 6：Truncated 只在取满 window 条时置位。
//
// 取满说明"最近 N 条"之外可能还有更多 —— 此时界面必须说明，
// 否则用户会把"最近 500 条"读成"历史全部"。
func TestAnalyticsTruncatedOnlyWhenWindowFilled(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	for i := 0; i < 5; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "m", startedAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	full, err := AnalyticsOverviewStats(context.Background(), 5)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if !full.Truncated {
		t.Fatalf("取满 window 条必须标记 truncated（此时总数只是切片不是全量）")
	}

	roomy, _ := AnalyticsOverviewStats(context.Background(), 10)
	if roomy.Truncated {
		t.Fatalf("窗口未取满时不该标记 truncated")
	}
}

// 判据 7：窗口取"最近 N 条"，不是"最早 N 条"。
func TestAnalyticsWindowTakesLatest(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	for i, name := range []string{"A", "B", "C"} {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: name,
			startedAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 1)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Models) != 1 || out.Models[0].Model != "C" {
		t.Fatalf("window=1 应取最新一条 C，实得 %+v", out.Models)
	}
}

// 判据 8：缓存命中率的分母是输入 token，不是总 token。
//
// 用总 token 当分母会把输出也算进"本可命中的量"，
// 得到的命中率系统性偏低（本用例 25% 会变成 16.7%）。
func TestAnalyticsCacheHitRateUsesPromptTokens(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "m", startedAt: analyticsBase(),
		prompt: 1000, cached: 250, completion: 500,
	})

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if out.CacheHitRate < 24.9 || out.CacheHitRate > 25.1 {
		t.Fatalf("命中率应为 cached/prompt = 25%%，实得 %.1f（用 total_tokens 会算成 16.7%%）",
			out.CacheHitRate)
	}
	if out.TotalTokens != 1500 {
		t.Fatalf("总 token 应为 1500，实得 %d", out.TotalTokens)
	}
}

// 判据 9：请求非法只拉低成功率，不拉低渠道健康度（与 fault-stats 同源）。
//
// 这条不改写新口径，而是验证这一页复用了既有判断 —— 口径一旦分叉，
// 用户在两个页面会看到两个互相矛盾的"渠道健康"。
func TestAnalyticsChannelRateExcludesRequestFault(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	seedAnalyticsLog(t, conn, analyticsLogSeed{status: "success", modelName: "m", startedAt: base})
	for i := 0; i < 3; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "failed", modelName: "m", faultKind: "request",
			startedAt: base.Add(time.Duration(i) * time.Second),
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if out.SuccessRate < 24.9 || out.SuccessRate > 25.1 {
		t.Fatalf("成功率应为 25%%，实得 %.1f", out.SuccessRate)
	}
	if out.ChannelRate < 99.9 {
		t.Fatalf("请求非法不该计入渠道健康度：channel_rate 应为 100%%，实得 %.1f", out.ChannelRate)
	}
	if out.RequestFault != 3 || out.RequestCount != 4 {
		t.Fatalf("请求非法应记 3 条、总数 4 条，实得 %d/%d", out.RequestFault, out.RequestCount)
	}
	// 均值口径：总花费 / 总数，而不是 / 成功数。
	if out.TotalCost != 0 {
		t.Fatalf("未设置花费时应为 0，实得 %v", out.TotalCost)
	}
}

// 判据 10：桶标签随跨度切换粒度 —— 同一天用 HH:00，跨天用 MM/DD。
//
// 只用一种会让跨天图上出现重复刻度（三个"10:00"），
// 用户分不清哪个是哪天。
func TestAnalyticsBucketLabelsSameDayUseHour(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	seedAnalyticsLog(t, conn, analyticsLogSeed{status: "success", modelName: "m", startedAt: base})
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "m", startedAt: base.Add(time.Hour),
	})

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Series) != 2 {
		t.Fatalf("两条相隔 1 小时应产生 2 个桶，实得 %d", len(out.Series))
	}
	if out.Series[0].Bucket != "10:00" || out.Series[1].Bucket != "11:00" {
		t.Fatalf("同一天的桶标签应为 10:00 / 11:00，实得 %q / %q",
			out.Series[0].Bucket, out.Series[1].Bucket)
	}
	if out.Series[0].BucketAt >= out.Series[1].BucketAt {
		t.Fatalf("桶必须按时间升序，实得 %d >= %d",
			out.Series[0].BucketAt, out.Series[1].BucketAt)
	}
}

func TestAnalyticsBucketLabelsCrossDayUseDate(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	seedAnalyticsLog(t, conn, analyticsLogSeed{status: "success", modelName: "m", startedAt: base})
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "m", startedAt: base.Add(24 * time.Hour),
	})

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Series) != 2 {
		t.Fatalf("跨天两条应产生 2 个桶，实得 %d", len(out.Series))
	}
	if out.Series[0].Bucket != "09/24" || out.Series[1].Bucket != "09/25" {
		t.Fatalf("跨天的桶标签应为 09/24 / 09/25，实得 %q / %q",
			out.Series[0].Bucket, out.Series[1].Bucket)
	}
}

func bucketLabels(series []AnalyticsBucket) []string {
	labels := make([]string, 0, len(series))
	for _, bucket := range series {
		labels = append(labels, bucket.Bucket)
	}
	return labels
}

// 判据 11：桶标签必须互不重复 —— 这是时间轴能读的最低要求。
//
// 实测踩过：跨天窗口下按 "MM/DD" 命名，同一天的多个整点桶会**全部同名**，
// 横轴上连续出现几个 "09/22"，用户分不清先后（本机真实数据上就是这样，
// 4 个桶里有 2 个叫 09/22）。修法是让粒度随跨度自适应：
// 24 小时以内按整点（一个整点不会在 24 小时内出现两次，天然唯一）。
func TestAnalyticsBucketLabelsAreUnique(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase() // 09/24 10:30
	// 跨天、但总跨度只有 4 小时：整点桶必须仍然唯一。
	for _, offset := range []time.Duration{12 * time.Hour, 13 * time.Hour, 15 * time.Hour, 16 * time.Hour} {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "m", startedAt: base.Add(offset), prompt: 10,
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Series) != 4 {
		t.Fatalf("4 条请求落在 4 个整点，应得 4 个桶，实得 %d（标签 %v）",
			len(out.Series), bucketLabels(out.Series))
	}
	seen := make(map[string]bool, len(out.Series))
	for _, bucket := range out.Series {
		if seen[bucket.Bucket] {
			t.Fatalf("桶标签重复 %q —— 横轴上分不清先后（序列 %v）",
				bucket.Bucket, bucketLabels(out.Series))
		}
		seen[bucket.Bucket] = true
	}
}

// 判据 12：跨度达到一天后按天聚合，合并后总量必须守恒。
//
// 合并而不是只保留每天第一条：堆叠图的总高度要等于真实用量，
// 否则用户对不上账（同判据 3 的取舍，只是换了个粒度）。
func TestAnalyticsRollsUpToDailyBuckets(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase() // 09/24 10:30
	// 09/24 22:30 与 23:30 **同一天两个整点桶**，再加 09/25、09/26 各一条：
	// 桶键跨度 37 小时。只有真的按天合并才会得到 3 个桶；按整点会得到 4 个。
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "A", startedAt: base.Add(12 * time.Hour), prompt: 10,
	})
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "A", startedAt: base.Add(13 * time.Hour), prompt: 10,
	})
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "B", startedAt: base.Add(24 * time.Hour), prompt: 20,
	})
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "success", modelName: "A", startedAt: base.Add(49 * time.Hour), prompt: 30,
	})

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Series) != 3 {
		t.Fatalf("4 条请求落在 3 天，应聚合成 3 个日桶（按整点会得 4 个），实得 %d（标签 %v）",
			len(out.Series), bucketLabels(out.Series))
	}
	if out.Series[0].Requests != 2 {
		t.Fatalf("同一天的两个整点桶必须合并：首桶应有 2 条，实得 %d（标签 %v）",
			out.Series[0].Requests, bucketLabels(out.Series))
	}
	seen := make(map[string]bool, len(out.Series))
	var tokens int64
	for _, bucket := range out.Series {
		if seen[bucket.Bucket] {
			t.Fatalf("日桶标签重复 %q（序列 %v）", bucket.Bucket, bucketLabels(out.Series))
		}
		seen[bucket.Bucket] = true
		var bucketSum int64
		for _, value := range bucket.ByModel {
			bucketSum += value
		}
		if bucketSum != bucket.Tokens {
			t.Fatalf("日桶 %q 内 by_model 合计 %d ≠ 桶 token %d（合并时必须累加而不是覆盖）",
				bucket.Bucket, bucketSum, bucket.Tokens)
		}
		tokens += bucket.Tokens
	}
	if tokens != out.TotalTokens {
		t.Fatalf("日桶 token 合计 %d ≠ 全局 %d", tokens, out.TotalTokens)
	}

	// 跨度必须基于**真实首尾请求时间**（22:30 → 11:30 = 37h，加一小时兜底），
	// 不是聚合后的日桶键（00:00 → 00:00 = 48h）——否则 RPM 会被系统性算小。
	wantSpan := (37*time.Hour + time.Hour).Seconds()
	if out.SpanSeconds < wantSpan-1 || out.SpanSeconds > wantSpan+1 {
		t.Fatalf("跨度应取真实首尾请求时间差（%.0fs），实得 %.0fs（用日桶键会得到 176400s）",
			wantSpan, out.SpanSeconds)
	}
}

// TestAnalyticsByModelCostConservesTotal 桶内按模型拆分的成本合计必须等于该桶的总成本。
//
// 守恒是这一维度的生命线：堆叠图把每个色块加起来的和，必须与"这个小时花了多少钱"
// 是同一个数。少了任何一段（比如尾部模型被丢掉而不是并进 __other__），
// 图上看不出异常，但"钱花在哪"的答案就永久缺了一块。
func TestAnalyticsByModelCostConservesTotal(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	costs := []float64{0.5, 1.25, 2.0}
	for i, cost := range costs {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status:    "success",
			modelName: fmt.Sprintf("m-%d", i),
			startedAt: base.Add(time.Duration(i) * time.Minute),
			prompt:    100,
			cost:      cost,
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Series) != 1 {
		t.Fatalf("三条请求在同一小时内，应只有 1 个桶，实得 %d", len(out.Series))
	}
	bucket := out.Series[0]
	if math.Abs(bucket.Cost-3.75) > 1e-9 {
		t.Fatalf("桶总成本应为 3.75，实得 %v", bucket.Cost)
	}
	var sum float64
	for _, cost := range bucket.ByModelCost {
		sum += cost
	}
	if math.Abs(sum-bucket.Cost) > 1e-9 {
		t.Fatalf("按模型拆分的成本合计 %v 不等于桶总成本 %v（守恒被破坏）", sum, bucket.Cost)
	}
	if math.Abs(out.TotalCost-3.75) > 1e-9 {
		t.Fatalf("窗口总成本应为 3.75，实得 %v", out.TotalCost)
	}
	// 均值跟着总量走：三个请求 3.75，均值必须是 1.25。
	if math.Abs(out.AvgCostPerRequest-1.25) > 1e-9 {
		t.Fatalf("平均每请求成本应为 1.25，实得 %v", out.AvgCostPerRequest)
	}
}

// TestAnalyticsByModelCostMergesTailModels 尾部模型的成本必须并进 __other__ 而不是丢弃。
//
// 与 token 维度的收敛是两个独立的实现点：只给 token 做收敛、成本那边直接丢掉尾部，
// 会让成本柱子在模型多的时候凭空矮一截 —— 而代码看起来"两边都处理了"。
func TestAnalyticsByModelCostMergesTailModels(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	const n = 12
	for i := 0; i < n; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status:    "success",
			modelName: fmt.Sprintf("model-%02d", i),
			startedAt: base.Add(time.Duration(i) * time.Second),
			prompt:    10,
			cost:      0.1,
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	bucket := out.Series[0]
	if len(bucket.ByModelCost) > relayAnalyticsTopModels+1 {
		t.Fatalf("堆叠图成本键应收敛到 %d+1，实得 %d", relayAnalyticsTopModels, len(bucket.ByModelCost))
	}
	var sum float64
	for _, cost := range bucket.ByModelCost {
		sum += cost
	}
	want := float64(n) * 0.1
	if math.Abs(sum-want) > 1e-9 {
		t.Fatalf("成本收敛后合计 %v，应为 %v（尾部模型的成本必须并进 %s，不能丢）",
			sum, want, otherModelKey)
	}
	if bucket.ByModelCost[otherModelKey] <= 0 {
		t.Fatalf("%d 个模型只保留 %d 个，成本里应出现 %s", n, relayAnalyticsTopModels, otherModelKey)
	}
}

// TestAnalyticsByModelCostRollsUpDaily 按天聚合时成本拆分同样必须累加。
//
// 跨天窗口会走 rollUpAnalyticsBuckets。那里如果只合并 token 不合并成本，
// 结果是"按天看时成本柱子全空、按小时看时有值"——取决于窗口大小的随机故障。
func TestAnalyticsByModelCostRollsUpDaily(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	// 两个不同自然日，各两条，全部在同一个模型上。
	for day := 0; day < 2; day++ {
		for i := 0; i < 2; i++ {
			seedAnalyticsLog(t, conn, analyticsLogSeed{
				status:    "success",
				modelName: "m-daily",
				startedAt: base.AddDate(0, 0, day).Add(time.Duration(i) * time.Minute),
				prompt:    50,
				cost:      0.25,
			})
		}
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(out.Series) != 2 {
		t.Fatalf("跨两天应聚合出 2 个日桶，实得 %d", len(out.Series))
	}
	for _, bucket := range out.Series {
		if math.Abs(bucket.Cost-0.5) > 1e-9 {
			t.Fatalf("日桶总成本应为 0.5（两条 0.25），实得 %v", bucket.Cost)
		}
		var sum float64
		for _, cost := range bucket.ByModelCost {
			sum += cost
		}
		if math.Abs(sum-bucket.Cost) > 1e-9 {
			t.Fatalf("日桶按模型成本合计 %v 不等于总成本 %v（按天聚合时丢了成本）", sum, bucket.Cost)
		}
	}
}

// TestAnalyticsByModelCostSameModelSetAsTokens token 与成本两张拆分表必须覆盖同一批模型。
//
// 为什么这条必须单独钉：两张表各有一套 keep 判断时（比如成本那边按成本排序取前 N），
// 同一根柱子在两个口径下的色块构成会不一样，而两条口径还都声称合计等于桶总量 ——
// 这种不一致在界面上表现为"切一下口径，图例就变了"，极难定位到根因。
//
// __other__ 允许不对称：某个尾部模型 token 为正但成本恰为 0 时，
// token 那边会合并出 __other__ 而成本那边不会（0 不值得建键）。
func TestAnalyticsByModelCostSameModelSetAsTokens(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	const n = 12
	for i := 0; i < n; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status:    "success",
			modelName: fmt.Sprintf("model-%02d", i),
			startedAt: base.Add(time.Duration(i) * time.Second),
			prompt:    10,
			cost:      float64(i+1) * 0.01,
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	for _, bucket := range out.Series {
		onlyTokens := map[string]bool{}
		for name := range bucket.ByModel {
			if name != otherModelKey {
				onlyTokens[name] = true
			}
		}
		for name := range bucket.ByModelCost {
			if name != otherModelKey {
				delete(onlyTokens, name)
			}
		}
		if len(onlyTokens) > 0 {
			t.Fatalf("以下模型只在 token 拆分里、不在成本拆分里：%v（两张表用了不同的保留集合）", onlyTokens)
		}
	}
}
