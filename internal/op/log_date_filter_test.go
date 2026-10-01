package op

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 需求4「按天查询」的判据。
//
// # 这个功能的失败长什么样
//
// 日期筛选写错时不报错、只是查不到东西：用户选了一天，看到"今天没有日志"，
// 而事实上是查询条件把数据全挡在外面了。所以判据必须**同时验证三条**：
//  1. 界内的行真的能被查到（正向）；
//  2. 界外的行真的查不到（反向 —— 只验正向的话，"不过滤"也会全过）；
//  3. 边界那条不多不少：半开区间的两端各测一次。

var logDateTestSeq int

func openLogDateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	logDateTestSeq++
	dsn := fmt.Sprintf("file:logdate-%s-%d?mode=memory&cache=shared", t.Name(), logDateTestSeq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.RelayLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))
	return conn
}

// seedLogOnDay 在指定时刻插一条日志，request_id 用时刻本身，便于断言时认出是哪条。
// 形参叫 modelName 而不是 model：后者会把 internal/model 这个包名遮住，
// 函数体里就再也写不了 model.RelayLog（实测踩到，编译报 "model.RelayLog is not a type"）。
func seedLogOnDay(t *testing.T, conn *gorm.DB, at time.Time, modelName string) {
	t.Helper()
	row := model.RelayLog{
		RequestID: uint64(at.Unix()),
		Status:    "success",
		Model:     modelName,
		StartedAt: at,
		CreatedAt: at,
	}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatalf("seed log on %s: %v", at.Format(time.RFC3339), err)
	}
}

func logModels(t *testing.T, filter model.RelayLogFilter) []string {
	t.Helper()
	rows, _ := relayLogListOn(nil, filter)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Model)
	}
	return out
}

func logModelsContain(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}

// 单日查询：当天的进来，前一天与后一天的都进不来。
func TestRelayLogFilterBySingleDay(t *testing.T) {
	conn := openLogDateTestDB(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)
	seedLogOnDay(t, conn, day.Add(9*time.Hour), "in-day-0900")
	seedLogOnDay(t, conn, day.Add(23*time.Hour+59*time.Minute), "in-day-2359")
	seedLogOnDay(t, conn, day.Add(-2*time.Hour), "prev-day-2200")
	seedLogOnDay(t, conn, day.Add(25*time.Hour), "next-day-0100")

	from := day
	to := day.AddDate(0, 0, 1)
	got := logModels(t, model.RelayLogFilter{From: &from, To: &to})

	if !logModelsContain(got, "in-day-0900") || !logModelsContain(got, "in-day-2359") {
		t.Errorf("当天的两条都应查到，实得 %v", got)
	}
	if logModelsContain(got, "prev-day-2200") {
		t.Errorf("前一天 22:00 不该进来，实得 %v", got)
	}
	if logModelsContain(got, "next-day-0100") {
		t.Errorf("后一天 01:00 不该进来（半开区间），实得 %v", got)
	}
}

// 只给一端：另一侧不设界，不能因为零值时间把全部历史挡掉。
func TestRelayLogFilterOpenEndedRanges(t *testing.T) {
	conn := openLogDateTestDB(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)
	seedLogOnDay(t, conn, day.Add(-72*time.Hour), "three-days-ago")
	seedLogOnDay(t, conn, day.Add(3*time.Hour), "in-day-0300")

	from := day
	if got := logModels(t, model.RelayLogFilter{From: &from}); !logModelsContain(got, "in-day-0300") || logModelsContain(got, "three-days-ago") {
		t.Errorf("只给 from 应含当天、不含更早，实得 %v", got)
	}
	to := day.AddDate(0, 0, 1)
	if got := logModels(t, model.RelayLogFilter{To: &to}); !logModelsContain(got, "three-days-ago") || !logModelsContain(got, "in-day-0300") {
		t.Errorf("只给 to 应含全部更早，实得 %v", got)
	}
	// 两端都不给：必须一条都不过滤。
	if got := logModels(t, model.RelayLogFilter{}); len(got) != 2 {
		t.Errorf("不给日期应返回全部 2 条，实得 %d 条 %v", len(got), got)
	}
}

// 边界贴合：从当天 00:00:00 起、到次日 00:00:00 止（不含）。
func TestRelayLogFilterBoundaryIsInclusiveStartExclusiveEnd(t *testing.T) {
	conn := openLogDateTestDB(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)
	seedLogOnDay(t, conn, day, "exactly-midnight")
	seedLogOnDay(t, conn, day.AddDate(0, 0, 1), "exactly-next-midnight")

	from := day
	to := day.AddDate(0, 0, 1)
	got := logModels(t, model.RelayLogFilter{From: &from, To: &to})

	if !logModelsContain(got, "exactly-midnight") {
		t.Errorf("当天 00:00:00 应被包含，实得 %v", got)
	}
	if logModelsContain(got, "exactly-next-midnight") {
		t.Errorf("次日 00:00:00 不该被包含（半开区间），实得 %v", got)
	}
}

// 日期筛选要能与其他维度叠加：单独验日期会漏掉"日期把别的条件覆盖掉"这类错。
func TestRelayLogFilterDateCombinesWithOtherDimensions(t *testing.T) {
	conn := openLogDateTestDB(t)
	day := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)
	seedLogOnDay(t, conn, day.Add(5*time.Hour), "wanted")
	seedLogOnDay(t, conn, day.Add(5*time.Hour), "other")

	from := day
	to := day.AddDate(0, 0, 1)
	got := logModels(t, model.RelayLogFilter{From: &from, To: &to, Model: "wanted"})

	if len(got) != 1 || got[0] != "wanted" {
		t.Errorf("日期 + 模型名应只剩 wanted，实得 %v", got)
	}
}
