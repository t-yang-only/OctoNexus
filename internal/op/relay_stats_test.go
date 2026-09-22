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

// T-usability-008 真实通过率统计的判据。
//
// ## 这个功能解决的是「一个数回答两个问题」的困境
//
// senseaudio 的 success_rate 只有 31.6%，但它 channel_rate 接近 100% ——
// 那 25 次失败全是「用 chat 接口调 TTS/图像模型」造成的请求非法。
//
// 所以判据的核心是：**请求非法必须从渠道健康度里被排除出去**，
// 同时不能把未分类的历史数据猜成某一类。

func openFaultStatsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.RelayLog{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn
}

func seedFaultLog(conn *gorm.DB, status, channel, faultKind string) {
	if err := conn.Create(&model.RelayLog{
		Status:        status,
		TargetChannel: channel,
		FaultKind:     faultKind,
	}).Error; err != nil {
		panic(err)
	}
}

// 核心判据：请求非法**不能**拉低渠道健康度。
//
// 这是本功能存在的全部理由：那类失败换任何成员都会同样发生，
// 算进渠道通过率会让用户去修一个没坏的东西。
func TestFaultStatsExcludesRequestFaultFromChannelRate(t *testing.T) {
	conn := openFaultStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	// 模拟 senseaudio 的真实构成：1 个成功 + 3 个请求非法。
	seedFaultLog(conn, "success", "senseaudio", "")
	seedFaultLog(conn, "failed", "senseaudio", "request")
	seedFaultLog(conn, "failed", "senseaudio", "request")
	seedFaultLog(conn, "failed", "senseaudio", "request")

	stats, err := RelayLogFaultsStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats.Channels) != 1 {
		t.Fatalf("应有 1 个渠道，实得 %d", len(stats.Channels))
	}
	ch := stats.Channels[0]

	// success_rate = 1/4 = 25% —— 用户视角确实低。
	if ch.SuccessRate < 24.9 || ch.SuccessRate > 25.1 {
		t.Fatalf("success_rate 应为 25%%，实得 %.1f", ch.SuccessRate)
	}
	// channel_rate = 1/(1+0+0) = 100% —— 渠道本身没有故障。
	if ch.ChannelRate < 99.9 {
		t.Fatalf("请求非法不该计入渠道健康度：channel_rate 应为 100%%，实得 %.1f"+
			"（这正是本功能要修的错误）", ch.ChannelRate)
	}
}

// 成员故障与可恢复故障**必须**拉低渠道健康度。
func TestFaultStatsIncludesRealChannelFaults(t *testing.T) {
	conn := openFaultStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedFaultLog(conn, "success", "ch", "")
	seedFaultLog(conn, "failed", "ch", "member")
	seedFaultLog(conn, "failed", "ch", "transient")

	stats, _ := RelayLogFaultsStats(context.Background(), 100)
	ch := stats.Channels[0]
	// channel_rate = 1/(1+1+1) ≈ 33.3%
	if ch.ChannelRate > 34 || ch.ChannelRate < 33 {
		t.Fatalf("成员/可恢复故障应计入渠道健康度：channel_rate 应约 33.3%%，实得 %.1f", ch.ChannelRate)
	}
	if ch.MemberFault != 1 || ch.TransientFault != 1 {
		t.Fatalf("两类故障应各记 1，实得 member=%d transient=%d", ch.MemberFault, ch.TransientFault)
	}
}

// **未分类的失败不能猜** —— 单独计数，不进任何一个故障桶。
//
// 升级前的存量行没有 fault_kind。把它们猜成 transient 会把历史账算到渠道头上，
// 而原因根本不在它身上。
func TestFaultStatsKeepsUnclassifiedSeparate(t *testing.T) {
	conn := openFaultStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedFaultLog(conn, "success", "ch", "")
	seedFaultLog(conn, "failed", "ch", "") // 升级前的存量行

	stats, _ := RelayLogFaultsStats(context.Background(), 100)
	ch := stats.Channels[0]
	if ch.Unclassified != 1 {
		t.Fatalf("未分类失败应单独计数，实得 %d", ch.Unclassified)
	}
	if ch.MemberFault != 0 || ch.TransientFault != 0 {
		t.Fatalf("未分类不该被猜进故障桶，实得 member=%d transient=%d", ch.MemberFault, ch.TransientFault)
	}
	// 渠道健康度：分母只有成功 → 100%（未分类不参与）。
	if ch.ChannelRate < 99.9 {
		t.Fatalf("未分类不该影响渠道健康度，实得 %.1f", ch.ChannelRate)
	}
}

// 取消既不算成功也不算失败 —— 那是客户端主动断开，与渠道无关。
func TestFaultStatsCanceledIsNeither(t *testing.T) {
	conn := openFaultStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedFaultLog(conn, "success", "ch", "")
	seedFaultLog(conn, "canceled", "ch", "")

	stats, _ := RelayLogFaultsStats(context.Background(), 100)
	ch := stats.Channels[0]
	if ch.Canceled != 1 {
		t.Fatalf("取消应单独计数，实得 %d", ch.Canceled)
	}
	if ch.ChannelRate < 99.9 {
		t.Fatalf("取消不该影响渠道健康度，实得 %.1f", ch.ChannelRate)
	}
	// success_rate = 1/2 = 50%（取消计入分母，因为它是"没成功"的请求）
	if ch.SuccessRate < 49.9 || ch.SuccessRate > 50.1 {
		t.Fatalf("success_rate 应为 50%%，实得 %.1f", ch.SuccessRate)
	}
}

// 无渠道的日志（分组不存在等）只进全局，不该凭空造出一个空名渠道。
func TestFaultStatsSkipsEmptyChannel(t *testing.T) {
	conn := openFaultStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedFaultLog(conn, "failed", "", "transient")
	seedFaultLog(conn, "success", "ch", "")

	stats, _ := RelayLogFaultsStats(context.Background(), 100)
	if len(stats.Channels) != 1 {
		t.Fatalf("空渠道名不该进渠道列表，实得 %d 个", len(stats.Channels))
	}
	if stats.Channels[0].Channel != "ch" {
		t.Fatalf("渠道名应为 ch，实得 %q", stats.Channels[0].Channel)
	}
	if stats.TransientFault != 1 {
		t.Fatalf("无渠道的失败仍应计入全局，实得 transient=%d", stats.TransientFault)
	}
}

// window 生效：只统计最近 N 条。
func TestFaultStatsRespectsWindow(t *testing.T) {
	conn := openFaultStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	for i := 0; i < 5; i++ {
		seedFaultLog(conn, "success", "ch", "")
	}
	seedFaultLog(conn, "failed", "ch", "transient") // 最新一条

	stats, _ := RelayLogFaultsStats(context.Background(), 2)
	if stats.Window != 2 {
		t.Fatalf("window=2 时窗口应为 2 条，实得 %d", stats.Window)
	}
	if stats.TransientFault != 1 || stats.Success != 1 {
		t.Fatalf("最近 2 条应是「1 失败 + 1 成功」，实得 transient=%d success=%d",
			stats.TransientFault, stats.Success)
	}
}

// 分母为 0 时返回 0 而不是 NaN —— NaN 会让 JSON 序列化失败或前端显示成 "NaN%"。
func TestFaultStatsNoNaNWhenEmpty(t *testing.T) {
	conn := openFaultStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	stats, err := RelayLogFaultsStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("空表不该报错: %v", err)
	}
	if stats.Window != 0 || len(stats.Channels) != 0 {
		t.Fatalf("空表应得到空结果，实得 window=%d channels=%d", stats.Window, len(stats.Channels))
	}
}
