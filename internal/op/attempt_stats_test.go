package op

import (
	"context"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-trace-002 尝试链聚合的判据。
//
// ## 这个功能存在的唯一理由
//
// 一次**成功**的请求里，成员 A 失败、成员 B 接手成功了 —— A 的那次失败
// 在 fault-stats 里不存在（那条日志 status=success），在渠道画像里也不存在
// （画像记的是 B）。所以本组判据的核心是：
//
//	**成功的请求，它的失败轮必须被统计到。**
//
// 如果实现只遍历 `status != success` 的行，下面第一条用例必须变红。

func openAttemptStatsTestDB(t *testing.T) *gorm.DB {
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

// seedAttemptLog 落一条带尝试链的日志。
// 用 Create 而不是 raw SQL：这里要顺带验证 serializer:json 的往返。
func seedAttemptLog(t *testing.T, conn *gorm.DB, status string, chain []model.RelayAttemptDetail, truncated bool) model.RelayLog {
	t.Helper()
	row := model.RelayLog{
		Status:            status,
		AttemptDetail:     chain,
		AttemptsTruncated: truncated,
		Attempts:          len(chain),
	}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatalf("seed attempt log: %v", err)
	}
	return row
}

// 核心判据：**成功的请求里，失败的轮次必须进入统计**。
//
// 这是本功能区别于 fault-stats 的地方：那条日志 status=success，
// fault-stats 完全看不见它，而这里必须看见 A 的失败。
func TestAttemptStatsCountsFailuresInsideSuccessfulRequest(t *testing.T) {
	conn := openAttemptStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	// 一次成功的请求：A 失败（可恢复），B 接手成功。
	seedAttemptLog(t, conn, "success", []model.RelayAttemptDetail{
		{Round: 1, Channel: "channelA", Model: "m", WaitMs: 3000, FaultKind: "transient", Error: "upstream timeout"},
		{Round: 2, Channel: "channelB", Model: "m", WaitMs: 1500},
	}, false)

	got, err := AttemptChainStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}

	if got.Scanned != 1 {
		t.Fatalf("Scanned = %d, want 1", got.Scanned)
	}
	if got.AffectedRequests != 1 {
		t.Errorf("AffectedRequests = %d, want 1 —— 成功请求里的失败轮也必须算「受影响的请求」", got.AffectedRequests)
	}
	if got.MultiRoundRequests != 1 {
		t.Errorf("MultiRoundRequests = %d, want 1", got.MultiRoundRequests)
	}

	byName := map[string]AttemptChannelStat{}
	for _, ch := range got.Channels {
		byName[ch.Channel] = ch
	}
	a, ok := byName["channelA"]
	if !ok {
		t.Fatalf("channelA 没进统计 —— 这正是本功能要补的盲区，got %+v", got.Channels)
	}
	if a.Attempts != 1 || a.Failures != 1 || a.TransientFault != 1 {
		t.Errorf("channelA = %+v, want Attempts=1 Failures=1 TransientFault=1", a)
	}
	if a.Successes != 0 {
		t.Errorf("channelA.Successes = %d, want 0（它失败了）", a.Successes)
	}
	// 成功的轮次也要计入 Attempts：否则「成功率 100%」与「从没被试过」长得一样。
	b, ok := byName["channelB"]
	if !ok {
		t.Fatalf("channelB 没进统计")
	}
	if b.Attempts != 1 || b.Successes != 1 || b.Failures != 0 {
		t.Errorf("channelB = %+v, want Attempts=1 Successes=1 Failures=0", b)
	}
}

// 负向对照：没有失败轮的请求不该被算成「受影响」。
//
// 没有这条，上面那条用例只要把 AffectedRequests 恒等于 Scanned 就能过。
func TestAttemptStatsDoesNotFlagCleanRequests(t *testing.T) {
	conn := openAttemptStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedAttemptLog(t, conn, "success", []model.RelayAttemptDetail{
		{Round: 1, Channel: "channelA", Model: "m", WaitMs: 900},
	}, false)

	got, err := AttemptChainStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	if got.Scanned != 1 {
		t.Fatalf("Scanned = %d, want 1", got.Scanned)
	}
	if got.AffectedRequests != 0 {
		t.Errorf("AffectedRequests = %d, want 0 —— 一次干净的请求不该被算成受影响", got.AffectedRequests)
	}
	if got.MultiRoundRequests != 0 {
		t.Errorf("MultiRoundRequests = %d, want 0", got.MultiRoundRequests)
	}
}

// 三类失败归因必须各归各的桶，且未分类单独计 —— 不知道的不能猜。
func TestAttemptStatsSeparatesFaultKinds(t *testing.T) {
	conn := openAttemptStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedAttemptLog(t, conn, "failed", []model.RelayAttemptDetail{
		{Round: 1, Channel: "ch", FaultKind: "request", Error: "bad request"},
	}, false)
	seedAttemptLog(t, conn, "failed", []model.RelayAttemptDetail{
		{Round: 1, Channel: "ch", FaultKind: "member", Error: "invalid key"},
	}, false)
	seedAttemptLog(t, conn, "failed", []model.RelayAttemptDetail{
		{Round: 1, Channel: "ch", FaultKind: "transient", Error: "429"},
	}, false)
	// 升级前的存量轮：有失败但没有归因字段。
	seedAttemptLog(t, conn, "failed", []model.RelayAttemptDetail{
		{Round: 1, Channel: "ch", Error: "some old error"},
	}, false)

	got, err := AttemptChainStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	if len(got.Channels) != 1 {
		t.Fatalf("应只有 1 个渠道, got %d", len(got.Channels))
	}
	ch := got.Channels[0]
	if ch.RequestFault != 1 || ch.MemberFault != 1 || ch.TransientFault != 1 {
		t.Errorf("归因分桶错误: %+v", ch)
	}
	if ch.UnclassifiedFault != 1 {
		t.Errorf("UnclassifiedFault = %d, want 1 —— 没归因的失败不能并进任何一类", ch.UnclassifiedFault)
	}
	if ch.Failures != 4 {
		t.Errorf("Failures = %d, want 4", ch.Failures)
	}
	// 有 Error 但没有 FaultKind 的轮，绝不能算成「成功」。
	if ch.Successes != 0 {
		t.Errorf("Successes = %d, want 0 —— 带 Error 的轮不是成功轮", ch.Successes)
	}
}

// 截断必须被标记出来 —— 链不完整时聚合值偏低，读者必须知道。
func TestAttemptStatsFlagsTruncated(t *testing.T) {
	conn := openAttemptStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedAttemptLog(t, conn, "failed", []model.RelayAttemptDetail{
		{Round: 1, Channel: "ch", FaultKind: "transient"},
	}, true)

	got, err := AttemptChainStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	if got.Truncated != 1 {
		t.Errorf("Truncated = %d, want 1", got.Truncated)
	}
}

// 存量行（没有 attempt_detail）不进 Scanned，但必须计入 Window ——
// 这样「统计为空」和「没有数据」才能被区分。
func TestAttemptStatsSeparatesLegacyRows(t *testing.T) {
	conn := openAttemptStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	if err := conn.Create(&model.RelayLog{Status: "success", TargetChannel: "old"}).Error; err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	seedAttemptLog(t, conn, "success", []model.RelayAttemptDetail{
		{Round: 1, Channel: "new", WaitMs: 10},
	}, false)

	got, err := AttemptChainStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	if got.Window != 2 {
		t.Errorf("Window = %d, want 2（含存量行）", got.Window)
	}
	if got.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1（只含有链的行）", got.Scanned)
	}
}

// LastError 必须落在**最近一次**失败上，而不是窗口内最早那条。
//
// 抓的是遍历方向：Find 是 id DESC，若正序遍历，最后写入的会是窗口里最早的行。
func TestAttemptStatsLastErrorIsMostRecent(t *testing.T) {
	conn := openAttemptStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedAttemptLog(t, conn, "failed", []model.RelayAttemptDetail{
		{Round: 1, Channel: "ch", FaultKind: "transient", Error: "OLDER_ERROR"},
	}, false)
	seedAttemptLog(t, conn, "failed", []model.RelayAttemptDetail{
		{Round: 1, Channel: "ch", FaultKind: "transient", Error: "NEWER_ERROR"},
	}, false)

	got, err := AttemptChainStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	if len(got.Channels) != 1 {
		t.Fatalf("应只有 1 个渠道, got %d", len(got.Channels))
	}
	if got.Channels[0].LastError != "NEWER_ERROR" {
		t.Errorf("LastError = %q, want NEWER_ERROR —— 应取最近一次失败", got.Channels[0].LastError)
	}
}

// 超长错误必须被截断 —— 有的站点返回整页 HTML。
// 同时用中文测试，确认是按字符而非字节截断（按字节会把汉字切成半个）。
func TestAttemptStatsTruncatesLongErrorByRunes(t *testing.T) {
	conn := openAttemptStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	long := strings.Repeat("错", 500)
	seedAttemptLog(t, conn, "failed", []model.RelayAttemptDetail{
		{Round: 1, Channel: "ch", FaultKind: "transient", Error: long},
	}, false)

	got, err := AttemptChainStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	gotErr := got.Channels[0].LastError
	runes := []rune(gotErr)
	// 200 个字符 + 省略号 = 201
	if len(runes) != lastErrorMaxRunes+1 {
		t.Errorf("截断后字符数 = %d, want %d（按字符截断，不是按字节）", len(runes), lastErrorMaxRunes+1)
	}
	if !strings.HasSuffix(gotErr, "…") {
		t.Errorf("截断后应带省略号，got %q", gotErr)
	}
	// 关键：不能出现半个汉字（UTF-8 替换字符）。
	if strings.ContainsRune(gotErr, '\uFFFD') {
		t.Errorf("截断产生了无效 UTF-8 字符: %q", gotErr)
	}
}
