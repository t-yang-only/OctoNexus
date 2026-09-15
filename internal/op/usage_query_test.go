package op

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openUsageTestDB 打开一块内存 SQLite 并建出用量明细表。
// 与业务同驱动 (glebarez/sqlite 纯 Go), 不依赖 cgo; 库名按用例名隔离, 避免
// cache=shared 让同一进程内的用例互相看到对方的数据。
func openUsageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:usage_%d?mode=memory&cache=shared", time.Now().UnixNano())
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := conn.AutoMigrate(&model.UsageHourly{}); err != nil {
		t.Fatalf("migrate usage_hourlies: %v", err)
	}
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	return conn
}

// resetUsageBucketsForTest 清空包级内存桶, 避免用例之间互相污染。
func resetUsageBucketsForTest() {
	usageBucketsLock.Lock()
	usageBuckets = make(map[usageKey]*model.UsageHourly)
	usageBucketsLock.Unlock()
}

// TestUsageQueryReadsPersistedBuckets 回归 NM-DS-002: UsageQuery 曾直接 Find(&[]UsageRow),
// GORM 从扫描目标推出不存在的 usage_rows 表, 查询报错被日志吞掉, 于是 /api/v1/stats/usage
// 恒返回空明细 —— 已落库的桶必须查得到。
func TestUsageQueryReadsPersistedBuckets(t *testing.T) {
	openUsageTestDB(t)
	resetUsageBucketsForTest()
	ctx := context.Background()
	hour := model.UsageHourKey(time.Now())

	LogUsageHourly("mock-good", "chan-a", model.StatsMetrics{RequestSuccess: 2, InputToken: 20, OutputToken: 14})
	UsageSaveDB(ctx)

	rows := UsageQuery(ctx, model.UsageRange24h)
	if len(rows) != 1 {
		t.Fatalf("query after save returned %d rows, want 1 (usage_rows/usage_hourlies mix-up?)", len(rows))
	}
	got := rows[0]
	if got.Hour != hour || got.ModelName != "mock-good" || got.ChannelName != "chan-a" {
		t.Fatalf("bucket key mismatch: %+v", got)
	}
	if got.RequestSuccess != 2 || got.InputToken != 20 || got.OutputToken != 14 {
		t.Fatalf("metrics mismatch: %+v", got.StatsMetrics)
	}

	// 落库后内存桶已清空: 上面的结果只能来自库表, 再查一次锁死"库内可读"。
	if again := UsageQuery(ctx, model.UsageRange24h); len(again) != 1 || again[0].RequestSuccess != 2 {
		t.Fatalf("second query returned %+v, want the persisted bucket", again)
	}
}

// TestUsageQueryCountsMemoryAndStoredBucketsOnce 回归 NM-DS-002 修的第二个 bug:
// 内存桶曾被"先整批追加、再按键并入"各算一次, 于是未落库的当小时用量翻倍且出现重复行。
func TestUsageQueryCountsMemoryAndStoredBucketsOnce(t *testing.T) {
	openUsageTestDB(t)
	resetUsageBucketsForTest()
	ctx := context.Background()

	// 只有内存桶 (尚未到保存周期): 必须原值出现一次。
	LogUsageHourly("mem-only", "chan-a", model.StatsMetrics{RequestSuccess: 3, InputToken: 30})
	rows := UsageQuery(ctx, model.UsageRange24h)
	if len(rows) != 1 || rows[0].ModelName != "mem-only" {
		t.Fatalf("memory-only bucket returned %d rows: %+v", len(rows), rows)
	}
	if rows[0].RequestSuccess != 3 || rows[0].InputToken != 30 {
		t.Fatalf("memory-only bucket doubled: %+v", rows[0].StatsMetrics)
	}

	// 同键库内行 + 内存增量: 合并成一行且合计正确。
	LogUsageHourly("merge-me", "chan-b", model.StatsMetrics{RequestSuccess: 5, InputToken: 50})
	UsageSaveDB(ctx)
	LogUsageHourly("merge-me", "chan-b", model.StatsMetrics{RequestSuccess: 1, InputToken: 3})
	merged := UsageQuery(ctx, model.UsageRange24h)
	var found *UsageRow
	count := 0
	for i := range merged {
		if merged[i].ModelName == "merge-me" {
			count++
			found = &merged[i]
		}
	}
	if count != 1 {
		t.Fatalf("merged key produced %d rows, want 1: %+v", count, merged)
	}
	if found.RequestSuccess != 6 || found.InputToken != 53 {
		t.Fatalf("merged metrics wrong (want 6/53): %+v", found.StatsMetrics)
	}
}

// TestUsageSaveDBAccumulatesAcrossFlushes 回归 NM-DS-002 修的第三个 bug: 内存桶落库即被取走,
// 只装"上次落库之后"的增量; 若直接 UpdateAll 覆盖库内同键行, 同一小时第二个保存周期会把
// 第一个周期的用量抹掉, 明细于是比真实流量少。
func TestUsageSaveDBAccumulatesAcrossFlushes(t *testing.T) {
	openUsageTestDB(t)
	resetUsageBucketsForTest()
	ctx := context.Background()

	LogUsageHourly("mock-good", "chan-a", model.StatsMetrics{RequestSuccess: 3, InputToken: 30, OutputToken: 21})
	UsageSaveDB(ctx)
	LogUsageHourly("mock-good", "chan-a", model.StatsMetrics{RequestSuccess: 2, InputToken: 20, OutputToken: 14})
	UsageSaveDB(ctx)

	rows := UsageQuery(ctx, model.UsageRange24h)
	if len(rows) != 1 {
		t.Fatalf("two flushes produced %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].RequestSuccess != 5 || rows[0].InputToken != 50 || rows[0].OutputToken != 35 {
		t.Fatalf("second flush overwrote the first window: %+v", rows[0].StatsMetrics)
	}

	// 失败计数与第三个周期同样要累加, 且不同键各自独立成行。
	LogUsageHourly("mock-good", "chan-a", model.StatsMetrics{RequestSuccess: 1, RequestFailed: 2})
	LogUsageHourly("other-model", "chan-b", model.StatsMetrics{RequestSuccess: 7})
	UsageSaveDB(ctx)

	rows = UsageQuery(ctx, model.UsageRange24h)
	if len(rows) != 2 {
		t.Fatalf("third flush produced %d rows, want 2: %+v", len(rows), rows)
	}
	byKey := map[string]UsageRow{}
	for _, r := range rows {
		byKey[r.ModelName] = r
	}
	if got := byKey["mock-good"]; got.RequestSuccess != 6 || got.RequestFailed != 2 {
		t.Fatalf("counters not accumulated: %+v", got.StatsMetrics)
	}
	if got := byKey["other-model"]; got.RequestSuccess != 7 {
		t.Fatalf("unrelated key wrong: %+v", got.StatsMetrics)
	}
}

// TestUsageSaveDBRestoresBucketsOnFailure 落库失败时桶要放回内存, 供下一轮重试。
func TestUsageSaveDBRestoresBucketsOnFailure(t *testing.T) {
	conn := openUsageTestDB(t)
	resetUsageBucketsForTest()

	if err := conn.Migrator().DropTable(&model.UsageHourly{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	LogUsageHourly("mock-good", "chan-a", model.StatsMetrics{RequestSuccess: 2, InputToken: 20})
	UsageSaveDB(context.Background())

	usageBucketsLock.Lock()
	restored := len(usageBuckets)
	var success int64
	for _, bucket := range usageBuckets {
		success += bucket.RequestSuccess
	}
	usageBucketsLock.Unlock()
	if restored != 1 || success != 2 {
		t.Fatalf("buckets not restored after a failed save: rows=%d success=%d", restored, success)
	}
}

// TestUsageQueryFiltersByWindow 窗口外的旧桶不出现在结果里, 且无归属请求不进桶。
func TestUsageQueryFiltersByWindow(t *testing.T) {
	conn := openUsageTestDB(t)
	resetUsageBucketsForTest()
	ctx := context.Background()

	old := time.Now().AddDate(0, 0, -40)
	if err := conn.Create(&model.UsageHourly{
		Hour: model.UsageHourKey(old), ModelName: "old-model", ChannelName: "chan-a",
		StatsMetrics: model.StatsMetrics{RequestSuccess: 9},
	}).Error; err != nil {
		t.Fatalf("seed old bucket: %v", err)
	}

	if rows := UsageQuery(ctx, model.UsageRange24h); len(rows) != 0 {
		t.Fatalf("24h window leaked %d rows: %+v", len(rows), rows)
	}
	if rows := UsageQuery(ctx, model.UsageRange30d); len(rows) != 0 {
		t.Fatalf("30d window leaked %d rows: %+v", len(rows), rows)
	}

	LogUsageHourly("", "chan-a", model.StatsMetrics{RequestFailed: 1})
	LogUsageHourly("some-model", "", model.StatsMetrics{RequestFailed: 1})
	UsageSaveDB(ctx)
	if rows := UsageQuery(ctx, model.UsageRange24h); len(rows) != 0 {
		t.Fatalf("unattributed request entered usage detail: %+v", rows)
	}
}
