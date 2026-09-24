package op

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-insight-004 全局延迟分布的判据。
//
// ## 这套判据要钉住的三件事
//
//  1. **失败请求必须计入总耗时**。这是最容易做错、也最贵的一处：
//     只统计成功请求会让延迟画像系统性偏好 —— 一次 60 秒超时正是最该被
//     看见的慢，把它排除掉，故障期的延迟数字反而变漂亮。所以必须有一条
//     用例专门验证"失败行进了总耗时样本"。
//
//  2. **首字节与总耗时是两个独立维度**。把它们合并或互相顶替（拿总耗时
//     填首字节）会同时丢掉两边信息：首字节快的系统总耗时可能很慢（长输出）。
//
//  3. **未提交首字节的请求不得按 0 计入首字节分布**。失败/取消的请求
//     FirstByteMs 是 -1，若直接 append 进去，p50 会被一群 0 拉低，
//     显示成"上游快得不可思议"。这是 ChannelLatencyStats 已经踩过的坑
//     （first_byte_samples 就是为它加的），这里必须同样守住。

// latencyDistEnv 建内存库并把它装成全局 DB，返回可直接 Create 的连接。
//
// 复用既有的 openLatencyTestDB 装置（同包，见 channel_latency_test.go），
// 不另造一套 —— 两套装置在同一个包里必然漂移。
func latencyDistEnv(t *testing.T) *gorm.DB {
	t.Helper()
	conn := openLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	return conn
}

// seedDistRow 写一行日志。便于每条用例只声明它关心的字段。
//
// 名字与既有 seedLatency 刻意区分：后者签名是 (conn, channel, firstByte, duration)
// 且固定写 success，本函数的用例需要控制 status 与 request_id。
func seedDistRow(t *testing.T, conn *gorm.DB, seed model.RelayLog) {
	t.Helper()
	if seed.Model == "" {
		seed.Model = "m"
	}
	if err := conn.Create(&seed).Error; err != nil {
		t.Fatalf("seed relay log: %v", err)
	}
}

// 判据一：分位数按最近秩法给全（min/p50/p90/p95/p99/max），样本数如实。
//
// 用 1..100 的等差样本，让每个分位的期望值可以手算出来核对：
// 最近秩法取第 ceil(q*n)-1 个元素（0-based），n=100 时即第 q*100 个元素。
func TestLatencyDistributionQuantiles(t *testing.T) {
	conn := latencyDistEnv(t)
	for i := 1; i <= 100; i++ {
		seedDistRow(t, conn, model.RelayLog{
			RequestID:   uint64(i),
			Status:      "success",
			Model:       "m",
			DurationMs:  int64(i) * 100, // 100..10000
			FirstByteMs: int64(i) * 10,  // 10..1000
		})
	}

	got, err := LatencyDistributionStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("LatencyDistributionStats: %v", err)
	}
	if got.Window != 100 {
		t.Fatalf("Window = %d, want 100", got.Window)
	}
	if got.Duration.Samples != 100 {
		t.Fatalf("Duration.Samples = %d, want 100", got.Duration.Samples)
	}
	if got.FirstByte.Samples != 100 {
		t.Fatalf("FirstByte.Samples = %d, want 100", got.FirstByte.Samples)
	}
	// 手算期望（最近秩法，n=100）：
	//   p50 → 第 50 个元素（100-based）= 5000；p90 → 9000
	//   p95 → 9500；p99 → 9900
	if got.Duration.P50Ms != 5000 {
		t.Errorf("Duration.P50Ms = %d, want 5000", got.Duration.P50Ms)
	}
	if got.Duration.P90Ms != 9000 {
		t.Errorf("Duration.P90Ms = %d, want 9000", got.Duration.P90Ms)
	}
	if got.Duration.P95Ms != 9500 {
		t.Errorf("Duration.P95Ms = %d, want 9500", got.Duration.P95Ms)
	}
	if got.Duration.P99Ms != 9900 {
		t.Errorf("Duration.P99Ms = %d, want 9900", got.Duration.P99Ms)
	}
	if got.Duration.MinMs != 100 {
		t.Errorf("Duration.MinMs = %d, want 100", got.Duration.MinMs)
	}
	if got.Duration.MaxMs != 10000 {
		t.Errorf("Duration.MaxMs = %d, want 10000", got.Duration.MaxMs)
	}
	// 首字节是独立维度：不能被总耗时顶替。
	if got.FirstByte.P50Ms != 500 {
		t.Errorf("FirstByte.P50Ms = %d, want 500（首字节与总耗时必须是两个独立的分布）",
			got.FirstByte.P50Ms)
	}
	if got.FirstByte.MaxMs != 1000 {
		t.Errorf("FirstByte.MaxMs = %d, want 1000", got.FirstByte.MaxMs)
	}
}

// 判据二（核心）：**失败请求必须计入总耗时**，但不得计入首字节。
//
// 反向对照就在同一条用例里：若实现改成只在 Status=="success" 时采样，
// 下面的 10 秒失败行会消失，P99/Max 立刻掉到 1 秒级 —— 用例变红。
func TestLatencyDistributionCountsFailedRequestsInDuration(t *testing.T) {
	conn := latencyDistEnv(t)
	// 99 条 1 秒的成功请求 + 1 条 60 秒的失败请求。
	for i := 0; i < 99; i++ {
		seedDistRow(t, conn, model.RelayLog{
			RequestID: uint64(i + 1), Status: "success", Model: "m",
			DurationMs: 1000, FirstByteMs: 100,
		})
	}
	seedDistRow(t, conn, model.RelayLog{
		RequestID: 100, Status: "failed", Model: "m",
		DurationMs: 60000, FirstByteMs: -1, // 从未提交首字节（超时/取消的典型形状）
	})

	got, err := LatencyDistributionStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("LatencyDistributionStats: %v", err)
	}
	if got.Duration.Samples != 100 {
		t.Fatalf("Duration.Samples = %d, want 100（失败请求必须计入总耗时：一次 60 秒超时"+
			"正是最该被看见的慢，排除它会让故障期的延迟数字反而变漂亮）",
			got.Duration.Samples)
	}
	if got.Duration.MaxMs != 60000 {
		t.Errorf("Duration.MaxMs = %d, want 60000（失败行的耗时应进入样本）", got.Duration.MaxMs)
	}
	// 反向对照：首字节样本必须是 99 —— 那一行 -1 不得被算进来。
	if got.FirstByte.Samples != 99 {
		t.Errorf("FirstByte.Samples = %d, want 99（FirstByteMs=-1 表示从未提交首字节，"+
			"按 0 计入会把 p50 拉低，显示成「上游快得不可思议」）", got.FirstByte.Samples)
	}
}

// 判据三：尾部分档与阈值随结果返回，且 60 秒以上单独可见。
//
// 阈值必须**随结果返回**：界面上那个"慢"字要与它对应，两处各写一个数字必然漂移。
func TestLatencyDistributionTail(t *testing.T) {
	conn := latencyDistEnv(t)
	// 6 条 100ms 快、3 条 5 秒慢（超过首字节阈值 3000ms）、1 条 70 秒超时。
	for i := 0; i < 6; i++ {
		seedDistRow(t, conn, model.RelayLog{
			RequestID: uint64(i + 1), Status: "success", Model: "m",
			DurationMs: 100, FirstByteMs: 100,
		})
	}
	for i := 0; i < 3; i++ {
		seedDistRow(t, conn, model.RelayLog{
			RequestID: uint64(i + 10), Status: "success", Model: "m",
			DurationMs: 5000, FirstByteMs: 5000,
		})
	}
	seedDistRow(t, conn, model.RelayLog{
		RequestID: 20, Status: "failed", Model: "m",
		DurationMs: 70000, FirstByteMs: -1,
	})

	got, err := LatencyDistributionStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("LatencyDistributionStats: %v", err)
	}
	if got.FirstByteTail.ThresholdMs != slowFirstByteMs {
		t.Errorf("FirstByteTail.ThresholdMs = %d, want %d（与 ChannelLatencyStats 同源，"+
			"两处各写一个常量必然漂移）", got.FirstByteTail.ThresholdMs, slowFirstByteMs)
	}
	if got.FirstByteTail.SlowCount != 3 {
		t.Errorf("FirstByteTail.SlowCount = %d, want 3（10 条里有 3 条首字节 ≥3000ms）",
			got.FirstByteTail.SlowCount)
	}
	// 3/9 = 33.33%：分母是 **9** 而不是 10 —— 那条 FirstByteMs=-1 的失败行
	// 被排除在首字节样本之外。这个数字恰好是"排除行为生效"的证据：
	// 若把它算进分母，得到的是 30.0，本断言立刻变红。
	if got.FirstByteTail.SlowRatio < 33.2 || got.FirstByteTail.SlowRatio > 33.4 {
		t.Errorf("FirstByteTail.SlowRatio = %.2f, want ≈33.33（= 3 条慢 / 9 条有首字节的样本；"+
			"若分母混进那条 -1 的失败行会得到 30.00）", got.FirstByteTail.SlowRatio)
	}
	// 总耗时阈值是 30 秒，只有那条 70 秒的超过。
	if got.DurationTail.ThresholdMs != slowDurationMs {
		t.Errorf("DurationTail.ThresholdMs = %d, want %d", got.DurationTail.ThresholdMs, slowDurationMs)
	}
	if got.DurationTail.SlowCount != 1 {
		t.Errorf("DurationTail.SlowCount = %d, want 1（只有 70 秒那条超过 30 秒阈值）",
			got.DurationTail.SlowCount)
	}
	if got.DurationTail.OverOneMinuteCount != 1 {
		t.Errorf("DurationTail.OverOneMinuteCount = %d, want 1", got.DurationTail.OverOneMinuteCount)
	}
	// 60 秒那条不得被算进首字节的慢计数（它根本没有首字节）。
	if got.FirstByteTail.OverOneMinuteCount != 0 {
		t.Errorf("FirstByteTail.OverOneMinuteCount = %d, want 0（首字节 100/5000 都没到 60 秒）",
			got.FirstByteTail.OverOneMinuteCount)
	}
}

// 判据四：直方图桶**总是全量区间**，且计数与样本数守恒。
//
// 两个不变量各自防一类错：
//   - 桶数固定 → 缺桶会让相邻两段在图上挤在一起，看起来像"这段没有区间"；
//   - 计数守恒 → 落在所有区间之外的样本必须进最后一个"及以上"桶，
//     否则直方图会静默丢样本（图上看起来总量对不上，但没人会去加）。
func TestLatencyDistributionHistogram(t *testing.T) {
	conn := latencyDistEnv(t)
	durations := []int64{100, 600, 1500, 2500, 4000, 8000, 15000, 25000, 45000, 90000}
	for i, d := range durations {
		seedDistRow(t, conn, model.RelayLog{
			RequestID: uint64(i + 1), Status: "success", Model: "m",
			DurationMs: d, FirstByteMs: 10,
		})
	}

	got, err := LatencyDistributionStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("LatencyDistributionStats: %v", err)
	}
	wantBuckets := len(latencyHistogramBounds) + 1
	if len(got.DurationHistogram) != wantBuckets {
		t.Fatalf("直方图桶数 = %d, want %d（空桶也要给：缺桶会让相邻区间在图上挤在一起）",
			len(got.DurationHistogram), wantBuckets)
	}
	var sum int64
	for _, bucket := range got.DurationHistogram {
		sum += bucket.Count
	}
	if sum != int64(len(durations)) {
		t.Fatalf("直方图计数合计 = %d, want %d（落在所有区间之外的样本必须进最后那个"+
			"「及以上」桶，否则会静默丢样本）", sum, len(durations))
	}
	// 最后一个桶是"及以上"：90 秒超过最大上界 60000，必须落在那里。
	last := got.DurationHistogram[len(got.DurationHistogram)-1]
	if last.UpperMs != 0 {
		t.Errorf("最后一个桶的 UpperMs = %d, want 0（0 表示「及以上」）", last.UpperMs)
	}
	if last.Count != 1 {
		t.Errorf("最后一个桶计数 = %d, want 1（只有 90000ms 超过 60000 上界）", last.Count)
	}
	// 每个桶的占比分母是总样本数。
	var ratioSum float64
	for _, bucket := range got.DurationHistogram {
		ratioSum += bucket.Ratio
	}
	if ratioSum < 99.9 || ratioSum > 100.1 {
		t.Errorf("直方图占比合计 = %.2f, want ≈100", ratioSum)
	}
}

// 判据五：空窗口不报错、不给 NaN，且 Truncated 如实反映是否触到上限。
func TestLatencyDistributionEmptyAndTruncated(t *testing.T) {
	conn := latencyDistEnv(t)

	got, err := LatencyDistributionStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("空窗口应正常返回而不是报错: %v", err)
	}
	if got.Window != 0 || got.Duration.Samples != 0 {
		t.Fatalf("空窗口 Window=%d Samples=%d, want 0/0", got.Window, got.Duration.Samples)
	}
	if got.Duration.P50Ms != 0 || got.Duration.MaxMs != 0 {
		t.Errorf("空窗口分位数应为 0（前端按 Samples==0 显示「—」），实得 p50=%d max=%d",
			got.Duration.P50Ms, got.Duration.MaxMs)
	}
	if got.DurationTail.SlowRatio != 0 {
		t.Errorf("空窗口 SlowRatio = %v, want 0（分母为 0 不许给 NaN）", got.DurationTail.SlowRatio)
	}
	if got.Truncated {
		t.Error("空窗口不该标记 Truncated")
	}
	// 空窗口的直方图仍应给出全部区间（形状固定），计数全 0。
	if len(got.DurationHistogram) != len(latencyHistogramBounds)+1 {
		t.Errorf("空窗口直方图桶数 = %d, want %d",
			len(got.DurationHistogram), len(latencyHistogramBounds)+1)
	}

	// 触到上限时 Truncated 必须为真：否则"最近 3 条"与"全部 3 条"在界面上长得一样。
	for i := 0; i < 5; i++ {
		seedDistRow(t, conn, model.RelayLog{
			RequestID: uint64(i + 1), Status: "success", Model: "m",
			DurationMs: 1000, FirstByteMs: 100,
		})
	}
	got, err = LatencyDistributionStats(context.Background(), 3)
	if err != nil {
		t.Fatalf("LatencyDistributionStats(3): %v", err)
	}
	if got.Window != 3 {
		t.Fatalf("Window = %d, want 3（受 window 上限约束）", got.Window)
	}
	if !got.Truncated {
		t.Error("扫描到上限时必须标记 Truncated —— 否则「最近 3 条」与「全部 3 条」在界面上长得一样")
	}
	// 未触到时不得误标。
	got, err = LatencyDistributionStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("LatencyDistributionStats(500): %v", err)
	}
	if got.Truncated {
		t.Error("没触到上限时不得标记 Truncated（误标会让人以为数据不全）")
	}
}

// 判据六：window 越界要夹回而不是报错，且首字节与服务端既有约定一致。
func TestLatencyDistributionWindowClamp(t *testing.T) {
	conn := latencyDistEnv(t)
	for i := 0; i < 3; i++ {
		seedDistRow(t, conn, model.RelayLog{
			RequestID: uint64(i + 1), Status: "success", Model: "m",
			DurationMs: 1000, FirstByteMs: 100,
		})
	}
	// window=0 → 取默认 500（而不是返回空结果）。
	got, err := LatencyDistributionStats(context.Background(), 0)
	if err != nil {
		t.Fatalf("window=0 应夹回默认值: %v", err)
	}
	if got.Window != 3 {
		t.Errorf("window=0 时 Window = %d, want 3（默认 500 足够覆盖全部样本）", got.Window)
	}
	if got.Truncated {
		t.Error("window=0（默认 500）不该标记 Truncated")
	}
}
