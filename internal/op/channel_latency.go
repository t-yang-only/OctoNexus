package op

import (
	"context"
	"sort"
)

// T-perf-001 渠道延迟画像。
//
// ## 解决什么问题
//
// 实测发现：17 个渠道的上游 TLS 握手从 1ms 到 1177ms，**差三个数量级**
// （api.x5m5x.com 1ms vs api.senseaudio.cn 1177ms）。
// 而慢的上游会拖慢每一次转发 —— 用户在客户端只感觉"这个模型怎么这么慢"，
// 完全看不出是渠道的问题，更不知道该换哪个。
//
// relay_logs 里早就有 FirstByteMs 与 DurationMs，但从没被按渠道聚合过。
// 这个函数把它变成可比较的画像。
//
// ## 为什么用中位数而不是平均值
//
// 首字节耗时的分布是**长尾**的：偶发的上游卡顿（几秒）会把平均值拉高，
// 让一个平时很快的渠道看起来很差。中位数描述的是"典型体验"，
// 这正是用户感知到的那个数字。p90 一并给出，用来看最坏情况的频率。
type ChannelLatency struct {
	Channel string `json:"channel"`
	// Samples 是本渠道在窗口内的样本数。**样本太少时下面的数字不可信** ——
	// 由调用方根据这个值决定是否展示。
	Samples int64 `json:"samples"`
	// FirstByteSamples 是**真的记到首字节**的样本数。
	//
	// 必须与 Samples 分开：某些路径（如非标准协议直通）不记录首字节，
	// 此时 FirstByteP50Ms 为 0 —— 而 0 在界面上会显示成「0ms」，
	// 让人以为「这个渠道快得不可思议」。有了这个计数，界面才能区分
	// 「真的 0ms」与「没有首字节数据」。
	FirstByteSamples int64 `json:"first_byte_samples"`
	// FirstByteP50Ms / P90Ms 是首字节耗时的中位数与 p90。
	//
	// **注意这不是纯网络延迟**：它从请求发出算到上游吐出第一个字节，
	// 因而包含上游自己的排队与推理时间。实测同一条链路（TLS 握手 1ms）
	// 下首字节中位数可达 4515ms —— 差的是上游处理，不是网络。
	// 只统计**已提交首字节**的请求（FirstByteMs >= 0）：
	// 未提交的那些是因为失败或取消，把它们算进来会污染"这个渠道有多快"。
	FirstByteP50Ms int64 `json:"first_byte_p50_ms"`
	FirstByteP90Ms int64 `json:"first_byte_p90_ms"`
	// DurationP50Ms 是总耗时的中位数（毫秒）。
	DurationP50Ms int64 `json:"duration_p50_ms"`
	// SlowCount 是首字节超过阈值的请求数，供用户判断"慢是常态还是偶发"。
	SlowCount int64 `json:"slow_count"`
}

// ChannelLatencySummary 是延迟画像的整体结果。
type ChannelLatencySummary struct {
	// SlowThresholdMs 是判定"慢"的阈值，随结果一起返回 ——
	// 界面上的"慢"字必须与这个数字对应，两处各写一个必然漂移。
	SlowThresholdMs int64 `json:"slow_threshold_ms"`
	// Window 是参与统计的样本总数。
	Window int64 `json:"window"`
	// Sample 说明这批样本的来源（窗口原始条数、剔除的测试请求数、是否触限）。
	Sample RelayLogSampleInfo `json:"sample"`
	// Channels 按首字节中位数排序：**快的在前** ——
	// 用户看这个表是为了挑快的用，不是挑慢的。
	Channels []ChannelLatency `json:"channels"`
}

// slowFirstByteMs 是判定"首字节慢"的阈值。
//
// 取 3000ms：正常上游首字节在几百毫秒内（实测 p50 大多 < 1500ms），
// 超过 3 秒用户已经明显感到"卡了一下" —— 那才是值得标记的。
const slowFirstByteMs = 3000

// ChannelLatencyStats 统计各渠道的首字节与总耗时分布。
//
// window 取最近 N 条日志（按 id 倒序）。样本太少时调用方会看到 Samples 很小，
// 这里不做"样本不足就不返回"的处理 —— 那会让用户以为接口坏了；
// 把事实（样本 3 条）摆出来，由人判断可信度。
func ChannelLatencyStats(ctx context.Context, window int) (ChannelLatencySummary, error) {
	rows, sample, err := relayLogWindow(ctx, window)
	if err != nil {
		return ChannelLatencySummary{}, err
	}

	type bucket struct {
		firstBytes []int64
		durations  []int64
		slow       int64
	}
	byChannel := make(map[string]*bucket)
	for _, row := range rows {
		// Window 统计**扫描到的全部日志**（含无渠道的），不是"参与渠道统计的条数"。
		//
		// 这样用户能看出落差：Window=100 而各渠道 Samples 加起来只有 95，
		// 说明有 5 条请求根本没走到渠道（分组不存在等）。
		// 若把无渠道的排除在外，这个落差就被藏起来了 —— 而那正是值得注意的信号。
		if row.TargetChannel == "" {
			continue
		}
		b, ok := byChannel[row.TargetChannel]
		if !ok {
			b = &bucket{}
			byChannel[row.TargetChannel] = b
		}
		// FirstByteMs < 0 表示首字节从未提交（失败/取消）：
		// 计入样本数，但不计入耗时分布 —— 否则一个全是失败的渠道会显示"很快"。
		if row.FirstByteMs >= 0 {
			b.firstBytes = append(b.firstBytes, row.FirstByteMs)
			if row.FirstByteMs >= slowFirstByteMs {
				b.slow++
			}
		}
		if row.DurationMs > 0 {
			b.durations = append(b.durations, row.DurationMs)
		}
	}

	summary := ChannelLatencySummary{
		SlowThresholdMs: slowFirstByteMs,
		Window:          sample.Samples,
		Sample:          sample,
		Channels:        make([]ChannelLatency, 0, len(byChannel)),
	}
	for name, b := range byChannel {
		c := ChannelLatency{
			Channel:          name,
			Samples:          int64(len(b.durations)),
			FirstByteSamples: int64(len(b.firstBytes)),
			SlowCount:        b.slow,
		}
		c.FirstByteP50Ms = percentile(b.firstBytes, 0.50)
		c.FirstByteP90Ms = percentile(b.firstBytes, 0.90)
		c.DurationP50Ms = percentile(b.durations, 0.50)
		summary.Channels = append(summary.Channels, c)
	}
	sort.Slice(summary.Channels, func(i, j int) bool {
		// 首字节中位数升序（快的在前）；相同则按样本数多的在前（更可信）。
		if summary.Channels[i].FirstByteP50Ms != summary.Channels[j].FirstByteP50Ms {
			return summary.Channels[i].FirstByteP50Ms < summary.Channels[j].FirstByteP50Ms
		}
		return summary.Channels[i].Samples > summary.Channels[j].Samples
	})
	return summary, nil
}

// percentile 返回已排序样本的指定分位值（毫秒，整数）。
//
// 空样本返回 0 —— 而不是 -1 之类的哨兵值：0 在界面上显示为「—」（由前端判断
// Samples==0 决定），比一个需要特判的负数更难出错。
func percentile(values []int64, q float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := make([]int64, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	// 最近秩法：取第 ceil(q*n)-1 个元素。样本少时比插值更稳
	// （插值会给出一个从没实际发生过的耗时）。
	idx := int(float64(len(sorted))*q+0.999999) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
