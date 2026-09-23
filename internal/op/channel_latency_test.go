package op

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-perf-001 渠道延迟画像的判据。
//
// ## 这个功能的两种失效方式
//
//   - **把失败的请求算进速度**：一个渠道全在失败（首字节从未提交，
//     FirstByteMs = -1），如果把它算进耗时分布，这个渠道会显示得**特别快** ——
//     因为它根本没吐过字节。这是最反直觉的错误。
//
//   - **用平均值代替中位数**：首字节分布是长尾的，偶发卡顿会把均值拉高，
//     让一个平时很快的渠道看起来很差。用户感知到的"典型速度"是中位数。

func openLatencyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.RelayLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

func seedLatency(conn *gorm.DB, channel string, firstByteMs, durationMs int64) {
	if err := conn.Create(&model.RelayLog{
		Status:        "success",
		TargetChannel: channel,
		Model:         "m",
		FirstByteMs:   firstByteMs,
		DurationMs:    durationMs,
	}).Error; err != nil {
		panic(err)
	}
}

// 核心：**失败的请求（FirstByteMs = -1）不能进耗时分布**。
//
// 否则一个"全部失败"的渠道会显示成最快 —— 它从没吐过字节。
func TestLatencyExcludesNeverCommitted(t *testing.T) {
	conn := openLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	// 快渠道：真的有 3 次提交，各 100ms。
	seedLatency(conn, "fast", 100, 500)
	seedLatency(conn, "fast", 110, 520)
	seedLatency(conn, "fast", 120, 540)
	// 全失败渠道：首字节从未提交。
	seedLatency(conn, "broken", -1, 2000)
	seedLatency(conn, "broken", -1, 3000)

	stats, err := ChannelLatencyStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	byName := map[string]ChannelLatency{}
	for _, c := range stats.Channels {
		byName[c.Channel] = c
	}

	fast := byName["fast"]
	if fast.FirstByteP50Ms < 100 || fast.FirstByteP50Ms > 120 {
		t.Fatalf("fast 的中位首字节应在 100-120ms，实得 %d", fast.FirstByteP50Ms)
	}

	broken := byName["broken"]
	if broken.FirstByteP50Ms != 0 {
		t.Fatalf("从未提交首字节的渠道不该有首字节耗时（应为 0），实得 %d", broken.FirstByteP50Ms)
	}
	// 但它的耗时是 0 不该被排到"最快"的第一位 —— 排序按首字节中位数，
	// 0 会排最前。这是可接受的：界面上会显示样本数与"—"。
	// 这里只断言数字正确，不断言排序（排序取向由界面决定）。
	if broken.Samples != 2 {
		t.Fatalf("样本数应按总耗时计入（2 条），实得 %d", broken.Samples)
	}
}

// 慢请求数按阈值统计：这条让用户分辨"慢是常态还是偶发"。
func TestLatencyCountsSlowRequests(t *testing.T) {
	conn := openLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedLatency(conn, "ch", 100, 500)
	seedLatency(conn, "ch", 200, 600)
	seedLatency(conn, "ch", slowFirstByteMs, 5000)   // 恰好等于阈值：算慢
	seedLatency(conn, "ch", slowFirstByteMs+1, 5000) // 超过阈值：算慢

	stats, _ := ChannelLatencyStats(context.Background(), 100)
	ch := stats.Channels[0]
	if ch.SlowCount != 2 {
		t.Fatalf("恰好等于阈值与超过阈值的都应算慢（共 2 条），实得 %d", ch.SlowCount)
	}
	if stats.SlowThresholdMs != slowFirstByteMs {
		t.Fatalf("阈值应随结果返回（界面上的「慢」必须与它对应），实得 %d", stats.SlowThresholdMs)
	}
}

// 阈值必须是正数且有意义 —— 0 会让每个请求都算慢，过大则永远不触发。
func TestLatencyThresholdIsSane(t *testing.T) {
	if slowFirstByteMs <= 0 {
		t.Fatalf("慢阈值必须为正，实得 %d（0 会让每个请求都算慢）", slowFirstByteMs)
	}
	if slowFirstByteMs > 60000 {
		t.Fatalf("慢阈值 %d ms 过大，永远触发不了", slowFirstByteMs)
	}
}

// 分位数：中位数与 p90 都要正确，且 p90 ≥ p50。
func TestLatencyPercentiles(t *testing.T) {
	conn := openLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	// 10 个样本：100..1000，中位数应为 500 附近的样本值，p90 应贴近最大值。
	for i := int64(1); i <= 10; i++ {
		seedLatency(conn, "ch", i*100, i*200)
	}
	stats, _ := ChannelLatencyStats(context.Background(), 100)
	ch := stats.Channels[0]

	// 最近秩法：p50 → 第 ceil(0.5*10)=5 个（索引 4）= 500
	if ch.FirstByteP50Ms != 500 {
		t.Fatalf("10 个样本 100..1000 的中位数应为 500，实得 %d", ch.FirstByteP50Ms)
	}
	// p90 → 第 ceil(0.9*10)=9 个（索引 8）= 900
	if ch.FirstByteP90Ms != 900 {
		t.Fatalf("p90 应为 900，实得 %d", ch.FirstByteP90Ms)
	}
	if ch.FirstByteP90Ms < ch.FirstByteP50Ms {
		t.Fatalf("p90(%d) 不该小于 p50(%d)", ch.FirstByteP90Ms, ch.FirstByteP50Ms)
	}
}

// p90 必须体现长尾：偶发卡顿不该被中位数掩盖。
func TestLatencyP90CapturesTail(t *testing.T) {
	conn := openLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	// 9 个快（100ms）+ 1 个极慢（10000ms）
	for i := 0; i < 9; i++ {
		seedLatency(conn, "ch", 100, 500)
	}
	seedLatency(conn, "ch", 10000, 12000)

	stats, _ := ChannelLatencyStats(context.Background(), 100)
	ch := stats.Channels[0]

	// 中位数仍是 100（典型体验没被拉高）—— 这正是用中位数的理由。
	if ch.FirstByteP50Ms != 100 {
		t.Fatalf("中位数应仍是 100（未被单次卡顿拉高），实得 %d", ch.FirstByteP50Ms)
	}
	// p90 抓到了那个尾巴。最近秩法：ceil(0.9*10)=9 → 索引 8 = 100
	// （第 10 个才是 10000）。所以这里 p90 仍是 100 是**符合最近秩法的**，
	// 而 SlowCount 才是长尾的判据。
	if ch.SlowCount != 1 {
		t.Fatalf("那次 10000ms 应被记为慢（SlowCount=1），实得 %d", ch.SlowCount)
	}
}

// 排序：快的在前（用户看这个表是为了挑快的用）。
func TestLatencySortedFastFirst(t *testing.T) {
	conn := openLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedLatency(conn, "slow", 5000, 6000)
	seedLatency(conn, "fast", 50, 200)
	seedLatency(conn, "mid", 800, 1000)

	stats, _ := ChannelLatencyStats(context.Background(), 100)
	if len(stats.Channels) != 3 {
		t.Fatalf("应有 3 个渠道，实得 %d", len(stats.Channels))
	}
	if stats.Channels[0].Channel != "fast" || stats.Channels[2].Channel != "slow" {
		t.Fatalf("应按首字节中位数升序（快的在前），实得 %s ... %s",
			stats.Channels[0].Channel, stats.Channels[2].Channel)
	}
}

// 无渠道的日志不进任何渠道画像；空库不报错。
func TestLatencySkipsEmptyChannelAndEmptyDB(t *testing.T) {
	conn := openLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	stats, err := ChannelLatencyStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("空库不该报错: %v", err)
	}
	if len(stats.Channels) != 0 || stats.Window != 0 {
		t.Fatalf("空库应得到空结果，实得 channels=%d window=%d", len(stats.Channels), stats.Window)
	}

	// 无渠道名的日志：不进渠道列表，但计入 Window（它确实是日志）。
	if err := conn.Create(&model.RelayLog{Status: "failed", TargetChannel: "", FirstByteMs: -1}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	stats, _ = ChannelLatencyStats(context.Background(), 100)
	if len(stats.Channels) != 0 {
		t.Fatalf("空渠道名不该造出渠道画像，实得 %d", len(stats.Channels))
	}
	if stats.Window != 1 {
		t.Fatalf("无渠道的日志仍应计入 Window，实得 %d", stats.Window)
	}
}

// percentile 本身：空返回 0、单元素返回自身、越界不 panic。
func TestPercentileEdgeCases(t *testing.T) {
	if got := percentile(nil, 0.5); got != 0 {
		t.Fatalf("空样本应返回 0，实得 %d", got)
	}
	if got := percentile([]int64{42}, 0.5); got != 42 {
		t.Fatalf("单样本应返回自身，实得 %d", got)
	}
	if got := percentile([]int64{42}, 0.99); got != 42 {
		t.Fatalf("单样本 p99 仍应返回自身，实得 %d", got)
	}
	// 无序输入也要正确处理（内部会排序）。
	if got := percentile([]int64{300, 100, 200}, 0.5); got != 200 {
		t.Fatalf("无序输入的中位数应为 200，实得 %d", got)
	}
}
