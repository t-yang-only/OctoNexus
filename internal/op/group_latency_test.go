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

// T-perf-003 分组维度延迟画像的判据。
//
// ## 与渠道画像的关键差别（判据要盯住的点）
//
//   1. 维度是 **group_id** 而不是 target_channel —— 同一渠道的请求可能属于不同分组，
//      混起来就答不了"我这个分组多快"；
//   2. 排序按**样本数**倒序（"我常用的"优先），而渠道画像按延迟升序（"挑快的用"）；
//   3. **必须带上成员数**：单成员分组没有选择空间，慢也只能用它 ——
//      只看延迟会让用户以为"换个成员就好了"，而它根本没得换。

func openGroupLatencyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.Group{}, &model.GroupItem{}, &model.RelayLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return conn
}

func seedGroupForLatency(t *testing.T, conn *gorm.DB, name string, members int) int {
	t.Helper()
	g := model.Group{Name: name}
	if err := conn.Create(&g).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	for i := 0; i < members; i++ {
		ref := i + 1
		if err := conn.Create(&model.GroupItem{GroupID: g.ID, ChannelGrantID: &ref, Priority: i + 1}).Error; err != nil {
			t.Fatalf("create item: %v", err)
		}
	}
	return g.ID
}

func seedGroupLatencyLog(conn *gorm.DB, groupID int, channel string, firstByteMs, durationMs int64) {
	if err := conn.Create(&model.RelayLog{
		Status:        "success",
		GroupID:       groupID,
		TargetChannel: channel,
		Model:         "m",
		FirstByteMs:   firstByteMs,
		DurationMs:    durationMs,
	}).Error; err != nil {
		panic(err)
	}
}

// 维度正确：同一个渠道的请求分属不同分组时，各归各的。
func TestGroupLatencyGroupsByGroupID(t *testing.T) {
	conn := openGroupLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	fast := seedGroupForLatency(t, conn, "fast-group", 2)
	slow := seedGroupForLatency(t, conn, "slow-group", 3)

	// **同一个渠道**（ch）下的请求，分属两个分组。
	seedGroupLatencyLog(conn, fast, "ch", 100, 500)
	seedGroupLatencyLog(conn, fast, "ch", 120, 520)
	seedGroupLatencyLog(conn, slow, "ch", 8000, 9000)

	stats, err := GroupLatencyStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(stats.Groups) != 2 {
		t.Fatalf("应按分组分成两个条目（同一渠道不算一个），实得 %d", len(stats.Groups))
	}
	byName := map[string]GroupLatency{}
	for _, g := range stats.Groups {
		byName[g.Name] = g
	}
	if got := byName["fast-group"].FirstByteP50Ms; got < 100 || got > 120 {
		t.Fatalf("fast-group 首字节中位应在 100-120，实得 %d", got)
	}
	if got := byName["slow-group"].FirstByteP50Ms; got != 8000 {
		t.Fatalf("slow-group 首字节中位应为 8000，实得 %d", got)
	}
}

// **必须带成员数**：单成员分组没有选择空间。
func TestGroupLatencyCarriesMemberCount(t *testing.T) {
	conn := openGroupLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	single := seedGroupForLatency(t, conn, "single", 1)
	multi := seedGroupForLatency(t, conn, "multi", 5)
	seedGroupLatencyLog(conn, single, "ch", 9000, 9500)
	seedGroupLatencyLog(conn, multi, "ch", 200, 600)

	stats, _ := GroupLatencyStats(context.Background(), 100)
	byName := map[string]GroupLatency{}
	for _, g := range stats.Groups {
		byName[g.Name] = g
	}
	if byName["single"].MemberCount != 1 {
		t.Fatalf("single 的成员数应为 1，实得 %d", byName["single"].MemberCount)
	}
	if byName["multi"].MemberCount != 5 {
		t.Fatalf("multi 的成员数应为 5，实得 %d", byName["multi"].MemberCount)
	}
	// 模式也要带上：用户据此判断"选路是否在起作用"。
	if byName["single"].Mode == "" && byName["multi"].Mode == "" {
		t.Fatalf("模式字段应随结果返回（用户据此判断选路是否生效）")
	}
}

// 排序按样本数倒序：**我常用的在前**（与渠道画像的"快的在前"取向不同）。
func TestGroupLatencySortedBySamplesDesc(t *testing.T) {
	conn := openGroupLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	rare := seedGroupForLatency(t, conn, "rare", 2)     // 1 条记录，但很快
	common := seedGroupForLatency(t, conn, "common", 2) // 5 条记录，但很慢
	seedGroupLatencyLog(conn, rare, "ch", 50, 100)
	for i := 0; i < 5; i++ {
		seedGroupLatencyLog(conn, common, "ch", 6000, 7000)
	}

	stats, _ := GroupLatencyStats(context.Background(), 100)
	if stats.Groups[0].Name != "common" {
		t.Fatalf("应按样本数倒序（我常用的在前），实得首个为 %q", stats.Groups[0].Name)
	}
}

// 无 group_id 的请求不进任何分组画像，但计入 Window（落差是信号）。
func TestGroupLatencySkipsZeroGroupID(t *testing.T) {
	conn := openGroupLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	g := seedGroupForLatency(t, conn, "g", 1)
	seedGroupLatencyLog(conn, g, "ch", 100, 200)
	// group_id=0：请求没走到任何分组。
	if err := conn.Create(&model.RelayLog{Status: "failed", GroupID: 0, TargetChannel: "ch", FirstByteMs: -1}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	stats, _ := GroupLatencyStats(context.Background(), 100)
	if len(stats.Groups) != 1 {
		t.Fatalf("group_id=0 不该造出分组条目，实得 %d", len(stats.Groups))
	}
	if stats.Window != 2 {
		t.Fatalf("Window 应含全部扫描到的日志（2 条），实得 %d", stats.Window)
	}
}

// 分组已删除但日志还在：用占位名兜底，不能显示成空白。
func TestGroupLatencyHandlesDeletedGroup(t *testing.T) {
	conn := openGroupLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	g := seedGroupForLatency(t, conn, "will-delete", 1)
	seedGroupLatencyLog(conn, g, "ch", 100, 200)
	// 删掉分组，日志留着（日志保留期 7 天，分组随时可能被删）。
	if err := conn.Where("id = ?", g).Delete(&model.Group{}).Error; err != nil {
		t.Fatalf("delete group: %v", err)
	}

	stats, _ := GroupLatencyStats(context.Background(), 100)
	if len(stats.Groups) != 1 {
		t.Fatalf("已删除分组的日志仍应出现在画像里，实得 %d", len(stats.Groups))
	}
	if stats.Groups[0].Name == "" {
		t.Fatalf("已删除的分组必须有可辨认的名字（不能是空字符串）")
	}
}

// 未提交首字节的请求不进首字节分布（与渠道画像同一口径）。
func TestGroupLatencyExcludesNeverCommitted(t *testing.T) {
	conn := openGroupLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	g := seedGroupForLatency(t, conn, "g", 2)
	seedGroupLatencyLog(conn, g, "ch", 300, 800)
	seedGroupLatencyLog(conn, g, "ch", -1, 5000)

	stats, _ := GroupLatencyStats(context.Background(), 100)
	row := stats.Groups[0]
	if row.FirstByteSamples != 1 {
		t.Fatalf("只有 1 条提交了首字节，样本数应为 1，实得 %d", row.FirstByteSamples)
	}
	if row.FirstByteP50Ms != 300 {
		t.Fatalf("首字节中位应为 300，实得 %d", row.FirstByteP50Ms)
	}
	if row.Samples != 2 {
		t.Fatalf("总耗时样本应含两条（-1 的那条也有耗时），实得 %d", row.Samples)
	}
}

// 空库不报错。
func TestGroupLatencyEmptyDB(t *testing.T) {
	conn := openGroupLatencyTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	stats, err := GroupLatencyStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("空库不该报错: %v", err)
	}
	if len(stats.Groups) != 0 || stats.Window != 0 {
		t.Fatalf("空库应得到空结果，实得 groups=%d window=%d", len(stats.Groups), stats.Window)
	}
}
