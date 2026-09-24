package op

import (
	"context"
	"sort"
)

// T-insight-004 全局延迟分布。
//
// ## 补的是哪个盲区
//
// 项目已有的耗时统计都是**按维度切开**的：ChannelLatencyStats 按渠道分、
// GroupLatencyStats 按分组分。它们回答的是"谁快谁慢"，但回答不了另一类问题：
//
//	这次整体有多慢？—— p95 是 3 秒还是 30 秒？
//	慢是普遍的还是被少数拖累的？—— 界面上那条健康线现在到底在哪？
//
// 只有分维度、没有整体，等于每次都要人肉在脑子里把各渠道的数字合起来。
// 而这两类问题的判据完全不同：挑渠道要看**中位数**（典型体验），
// 看整体健康要看**尾部分位**（p95/p99）—— 一个 p50 很漂亮但 p95 巨大 的系统，
// 体验是"大多数时候快、偶尔卡死"，这与"一直不快"是两种病，处置也不同。
//
// ## 两个维度分开统计
//
// 首字节（TTFT）与总耗时量的是两件事：前者是"什么时候开始看到东西"，
// 后者是"全部看完花了多久"。同一个系统完全可能首字节很快而总耗时很慢
// （长输出），也可能首字节就慢（上游排队）。合成一个数字会同时丢掉两边信息。
//
// ## 为什么不给平均值
//
// 耗时的分布是长尾的，平均值由少数极端值决定：一次 60 秒的超时能把
// 100 次 1 秒的请求拉高到 1.6 秒。这个数字不能描述任何一次真实体验。
// 这里只给分位数与直方图。

// LatencyHistogramBucket 是直方图的一个区间。
type LatencyHistogramBucket struct {
	// UpperMs 是该区间的上界（毫秒，不含）。最后一个桶的上界为 0，表示"及以上"。
	UpperMs int64 `json:"upper_ms"`
	// Count 是落在该区间的样本数。
	Count int64 `json:"count"`
	// Ratio 是占比（百分比），分母为 0 时给 0。
	Ratio float64 `json:"ratio"`
}

// LatencyQuantiles 是一组固定分位数。
//
// 只给这五个而不是任意分位：界面上的位置有限，而 p50/p90/p95/p99
// 是运维上约定俗成的四个观察点（中位、常态最坏、健康线、极端情况）。
type LatencyQuantiles struct {
	Samples int64 `json:"samples"`
	// MinMs / MaxMs 让"分布有多宽"一眼可见 ——
	// p50 与 p99 接近但 Min 极小的分布，与 p50 与 p99 差三个数量级的分布，
	// 是两种不同的系统。
	MinMs int64 `json:"min_ms"`
	MaxMs int64 `json:"max_ms"`
	P50Ms int64 `json:"p50_ms"`
	P90Ms int64 `json:"p90_ms"`
	P95Ms int64 `json:"p95_ms"`
	P99Ms int64 `json:"p99_ms"`
}

// LatencyTail 刻画分布的尾部：慢到什么程度、有多少。
type LatencyTail struct {
	// ThresholdMs 是判定"慢"的阈值，随结果返回 ——
	// 界面上的"慢"字必须与这个数字对应，两处各写一个必然漂移。
	ThresholdMs int64 `json:"threshold_ms"`
	// SlowCount / SlowRatio 是超过阈值的样本数与占比。
	//
	// 这两个数是"p95 之外"的补充：p95 只说第 95 百分位在哪，
	// 说不出"超过某个绝对时间的有多少条"。而用户的判断往往是绝对的
	// （"超过 30 秒的都算我这次白等了"）。
	SlowCount int64   `json:"slow_count"`
	SlowRatio float64 `json:"slow_ratio"`
	// OverOneMinuteCount 是超过 60 秒的样本数。
	//
	// 单独给这一档是因为它跨越了一条心理线：一分钟以上的等待，用户
	// 大多已经切走或重发了 —— 这类请求即使最终成功，也已经不是一次
	// 可用的服务。它值得在界面上单独可见，而不是淹在"慢"里。
	OverOneMinuteCount int64 `json:"over_one_minute_count"`
}

// LatencyDistributionSummary 是全局延迟分布的结果。
type LatencyDistributionSummary struct {
	// Window 是扫描到的日志条数（含没走到渠道的），与实际参与统计的样本数
	// 故意分开给 —— 两者的落差本身就是信号（有多少请求根本没发出去）。
	Window int64 `json:"window"`
	// SlowThresholdMs 与尾部分位阈值同源。
	SlowThresholdMs int64 `json:"slow_threshold_ms"`
	// FirstByte 只统计**真的记到首字节**的请求（FirstByteMs >= 0）。
	//
	// 未提交的那些是因为失败或取消 —— 把它们按 0 或按总耗时算进来，
	// 会让"上游到底多快"这个数字失去意义。
	FirstByte LatencyQuantiles `json:"first_byte"`
	// Duration 统计所有耗时 > 0 的请求（含失败）。
	//
	// 失败的请求**必须计入**：一次 60 秒超时正是最该被看见的慢。
	// 只统计成功请求会让延迟画像系统性偏好，把故障期的慢全部抹掉。
	Duration LatencyQuantiles `json:"duration"`
	// FirstByteTail / DurationTail 是两个维度各自的尾部。
	FirstByteTail LatencyTail `json:"first_byte_tail"`
	DurationTail  LatencyTail `json:"duration_tail"`
	// DurationHistogram 是总耗时的直方图（固定区间，见 latencyHistogramBounds）。
	DurationHistogram []LatencyHistogramBucket `json:"duration_histogram"`
	// Truncated 表示日志条数达到了 window 上限，统计只覆盖了最近的一部分。
	//
	// 必须显式给出：否则"最近 500 条"与"全部 500 条"在界面上长得一样，
	// 而前者在流量大时只代表几十分钟。
	Truncated bool `json:"truncated"`
	// Sample 说明这批样本的来源（窗口原始条数、扣除的测试请求数、是否触限）。
	//
	// 与 Truncated 的分工：Truncated 只说"够不够"，Sample 说"从多少条里挑出了多少条"。
	// 画像默认排除测试请求，不把扣除量摆出来就没法与日志页核对。
	Sample RelayLogSampleInfo `json:"sample"`
}

// 延迟"慢"的阈值。
//
// 首字节取 3000ms，与 ChannelLatencyStats 的 slowFirstByteMs **同源**
// （那一处已经在界面上解释了"超过 3 秒用户已经明显感到卡了一下"）。
// 两处各写一个常量必然漂移，所以这里直接引用同一个值。
//
// 总耗时取 30000ms：一次完整的往返（含长输出）通常远慢于首字节，
// 沿用 3 秒会把绝大多数正常请求判成"慢"，那个标记就失去意义了。
const slowDurationMs = 30000

// latencyHistogramBounds 是直方图的固定区间上界（毫秒）。
//
// 用**固定区间**而不是等分或自适应分箱：固定区间让两张不同时间的直方图可以
// 直接对比（"上周这个时候还是 1-3 秒占多数"），而自适应分箱每次都换坐标轴，
// 变化被坐标轴吃掉了。
//
// 上界按实际观测取档：实测首字节中位 4515ms、慢渠道 12 秒级、超时 60 秒，
// 因此 30 秒以下细分，之上合并为"≥60s"。
var latencyHistogramBounds = []int64{500, 1000, 2000, 3000, 5000, 10000, 20000, 30000, 60000}

// LatencyDistributionStats 统计窗口内整体延迟分布（首字节 + 总耗时）。
//
// window 取最近 N 条日志（按 id 倒序）。与既有画像一致：样本不足不做拦截，
// 把 Samples 摆出来由人判断可信度。
func LatencyDistributionStats(ctx context.Context, window int) (LatencyDistributionSummary, error) {
	rows, sample, err := relayLogWindow(ctx, window)
	if err != nil {
		return LatencyDistributionSummary{}, err
	}

	var firstBytes []int64
	var durations []int64
	for _, row := range rows {
		// 首字节：只有真的记到（>= 0）才算样本。
		if row.FirstByteMs >= 0 {
			firstBytes = append(firstBytes, row.FirstByteMs)
		}
		// 总耗时：> 0 才算（0 表示没计时，不是"瞬间完成"）。
		if row.DurationMs > 0 {
			durations = append(durations, row.DurationMs)
		}
	}

	summary := LatencyDistributionSummary{
		Window:            sample.Window,
		Sample:            sample,
		SlowThresholdMs:   slowFirstByteMs,
		Truncated:         sample.Truncated,
		FirstByte:         quantilesOf(firstBytes),
		Duration:          quantilesOf(durations),
		FirstByteTail:     tailOf(firstBytes, slowFirstByteMs),
		DurationTail:      tailOf(durations, slowDurationMs),
		DurationHistogram: histogramOf(durations),
	}
	return summary, nil
}

// quantilesOf 返回一组样本的固定分位数。
//
// 空样本全部给 0（而不是 -1 之类哨兵）：0 在界面上由前端按 Samples==0
// 决定显示「—」，比一个需要特判的负数更难出错。这与 percentile 的既有约定一致。
func quantilesOf(values []int64) LatencyQuantiles {
	out := LatencyQuantiles{Samples: int64(len(values))}
	if len(values) == 0 {
		return out
	}
	out.MinMs = percentile(values, 0)
	out.P50Ms = percentile(values, 0.50)
	out.P90Ms = percentile(values, 0.90)
	out.P95Ms = percentile(values, 0.95)
	out.P99Ms = percentile(values, 0.99)
	// MaxMs 不能复用 percentile(values, 1.0)：最近秩法在 q=1 时会取到最后一个元素，
	// 但那依赖 ceil 的边界处理恰好不越界 —— 直接取最大值更直白，也不会随
	// percentile 的实现细节变化。
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	out.MaxMs = sorted[len(sorted)-1]
	return out
}

// tailOf 统计超过阈值的样本数与占比。
func tailOf(values []int64, threshold int64) LatencyTail {
	tail := LatencyTail{ThresholdMs: threshold}
	for _, value := range values {
		if value >= threshold {
			tail.SlowCount++
		}
		if value >= 60000 {
			tail.OverOneMinuteCount++
		}
	}
	tail.SlowRatio = ratio(tail.SlowCount, int64(len(values)))
	return tail
}

// histogramOf 把样本按固定区间分桶。
//
// 返回的桶**总是全部区间**（含计数为 0 的）：空桶在直方图上是一条零高度的柱子，
// 而缺桶会让相邻两段挤在一起，看起来像"这段没有区间"。
func histogramOf(values []int64) []LatencyHistogramBucket {
	buckets := make([]LatencyHistogramBucket, 0, len(latencyHistogramBounds)+1)
	for _, bound := range latencyHistogramBounds {
		buckets = append(buckets, LatencyHistogramBucket{UpperMs: bound})
	}
	// 最后一个桶 UpperMs=0，表示"及以上"（超过最大上界）。
	buckets = append(buckets, LatencyHistogramBucket{UpperMs: 0})

	for _, value := range values {
		placed := false
		for i, bound := range latencyHistogramBounds {
			if value < bound {
				buckets[i].Count++
				placed = true
				break
			}
		}
		if !placed {
			buckets[len(buckets)-1].Count++
		}
	}
	total := int64(len(values))
	for i := range buckets {
		buckets[i].Ratio = ratio(buckets[i].Count, total)
	}
	return buckets
}
