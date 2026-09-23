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

// T-usability-009 分组使用情况的判据。
//
// ## 这个功能的红线：不能暗示"没用过就该删"
//
// 未被调用**不等于**该删 —— 备用分组、给子分组复用的分组、尚未启用的新分组
// 都可能合法地没有流量。所以判据要盯住两件事：
//
//  1. 统计要准（谁在用、用几次）；
//  2. **不能把"未使用"和"该删除"混为一谈** —— 输出里只有事实，没有建议。

func openGroupUsageTestDB(t *testing.T) *gorm.DB {
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

func seedGroup(t *testing.T, conn *gorm.DB, name string, items int) int {
	t.Helper()
	g := model.Group{Name: name}
	if err := conn.Create(&g).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	for i := 0; i < items; i++ {
		ref := i + 1
		if err := conn.Create(&model.GroupItem{GroupID: g.ID, ChannelGrantID: &ref, Priority: i + 1}).Error; err != nil {
			t.Fatalf("create item: %v", err)
		}
	}
	return g.ID
}

func seedGroupCall(conn *gorm.DB, groupID int, status string) {
	if err := conn.Create(&model.RelayLog{
		GroupID: groupID,
		Status:  status,
		Model:   "m",
	}).Error; err != nil {
		panic(err)
	}
}

// 统计要准：用过的进 Used、没用过的进 Unused，且调用次数正确。
func TestGroupUsageCountsCalls(t *testing.T) {
	conn := openGroupUsageTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	used := seedGroup(t, conn, "常用的", 2)
	seedGroup(t, conn, "备用的", 1)
	seedGroupCall(conn, used, "success")
	seedGroupCall(conn, used, "success")
	seedGroupCall(conn, used, "failed")

	stats, err := GroupUsageStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 2 {
		t.Fatalf("总分组应为 2，实得 %d", stats.Total)
	}
	if stats.Used != 1 || stats.Unused != 1 {
		t.Fatalf("应为 1 用 / 1 未用，实得 %d / %d", stats.Used, stats.Unused)
	}
	// 排序：常用的在前。
	if stats.Groups[0].Name != "常用的" {
		t.Fatalf("应按调用次数倒序，实得首个为 %q", stats.Groups[0].Name)
	}
	if stats.Groups[0].CallCount != 3 {
		t.Fatalf("调用次数应为 3（成功失败都算调用），实得 %d", stats.Groups[0].CallCount)
	}
	// 失败也是"被调用过"——它证明用户确实在用它，只是没成功。
	if stats.Groups[0].CallCount == 0 {
		t.Fatalf("失败的调用也应计入调用次数（用户确实用了它）")
	}
}

// 名字形态：含斜杠的标记为自动生成。
func TestGroupUsageMarksAutoNames(t *testing.T) {
	conn := openGroupUsageTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedGroup(t, conn, "senseaudio/deepseek-v4.1-flash", 1) // 自动形态
	seedGroup(t, conn, "我的高速组", 1)                          // 手工命名
	seedGroup(t, conn, "trailing/", 1)                      // 斜杠在末尾：不是有效形态
	seedGroup(t, conn, "/leading", 1)                       // 斜杠在开头：同上

	stats, _ := GroupUsageStats(context.Background(), 100)
	byName := map[string]GroupUsage{}
	for _, g := range stats.Groups {
		byName[g.Name] = g
	}
	if !byName["senseaudio/deepseek-v4.1-flash"].IsAuto {
		t.Fatalf("「渠道名/模型名」形态应标记为自动生成")
	}
	if byName["我的高速组"].IsAuto {
		t.Fatalf("手工命名的分组不该被标成自动生成")
	}
	// 斜杠在两端是无效形态（空的一段），不该算自动。
	if byName["trailing/"].IsAuto {
		t.Fatalf("斜杠在末尾（空模型名）不该算自动分组形态")
	}
	if byName["/leading"].IsAuto {
		t.Fatalf("斜杠在开头（空渠道名）不该算自动分组形态")
	}
	if stats.Auto != 1 || stats.Manual != 3 {
		t.Fatalf("应为 1 自动 / 3 手工，实得 %d / %d", stats.Auto, stats.Manual)
	}
}

// 成员数要一次查准（防 N+1 查询写成漏算）。
func TestGroupUsageCountsItems(t *testing.T) {
	conn := openGroupUsageTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedGroup(t, conn, "三个成员", 3)
	seedGroup(t, conn, "零成员", 0)

	stats, _ := GroupUsageStats(context.Background(), 100)
	byName := map[string]GroupUsage{}
	for _, g := range stats.Groups {
		byName[g.Name] = g
	}
	if byName["三个成员"].ItemCount != 3 {
		t.Fatalf("成员数应为 3，实得 %d", byName["三个成员"].ItemCount)
	}
	if byName["零成员"].ItemCount != 0 {
		t.Fatalf("零成员分组的成员数应为 0，实得 %d", byName["零成员"].ItemCount)
	}
}

// 空库不报错、不返回 NaN 类怪值。
func TestGroupUsageEmptyDB(t *testing.T) {
	conn := openGroupUsageTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	stats, err := GroupUsageStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("空库不该报错: %v", err)
	}
	if stats.Total != 0 || stats.Used != 0 || stats.Unused != 0 {
		t.Fatalf("空库应得到全零，实得 total=%d used=%d unused=%d",
			stats.Total, stats.Used, stats.Unused)
	}
	if len(stats.Groups) != 0 {
		t.Fatalf("空库不该有分组条目，实得 %d", len(stats.Groups))
	}
}

// 无 group_id 的日志（分组不存在等）不该凭空造出一个分组。
func TestGroupUsageIgnoresZeroGroupID(t *testing.T) {
	conn := openGroupUsageTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	seedGroup(t, conn, "真实分组", 1)
	// group_id=0 的日志：请求没走到任何分组。
	if err := conn.Create(&model.RelayLog{GroupID: 0, Status: "failed", Model: "m"}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	stats, _ := GroupUsageStats(context.Background(), 100)
	if stats.Total != 1 {
		t.Fatalf("group_id=0 不该造出分组，实得 total=%d", stats.Total)
	}
	if stats.Unused != 1 {
		t.Fatalf("那个分组确实没被调用过，应为 Unused，实得 used=%d unused=%d",
			stats.Used, stats.Unused)
	}
}
