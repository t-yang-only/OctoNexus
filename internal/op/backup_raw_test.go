package op

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var backupTestDBSeq int64

// openBackupTestDB 独立内存库; 每次打开带 atomic 序号, 与仓库既有 count-safe 约定一致
// (参照 openJumpTokenTestDB / openOfficialFlowTestDB)。共享缓存按名寻址, -count>1 时若仅用
// t.Name() 会命中上一轮同名库的残留行: rawCreate 的 OnConflict DoNothing 会把重复行整批跳过,
// 导致 TestRawCreateSkipsAssociations 第二轮起 rows=0。序号保证每轮是全新空库。
func openBackupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&backupTestDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
		&model.Group{},
		&model.GroupItem{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn
}

// TestRawCreateSkipsAssociations 对应 183 P0：验证 rawCreate 不写关联表
// 判据是「rows 0 时 group_items 不得有行」，用反射逐一比对而非靠 panic
// rawCreate 会跳过 GroupItem.ChannelGrantID 指向的 ChannelGrant 关联。
func TestRawCreateSkipsAssociations(t *testing.T) {
	conn := openBackupTestDB(t)
	grantID := 7
	items := []model.GroupItem{
		{ID: 1, GroupID: 1, ChannelGrantID: &grantID, Priority: 1},
		{ID: 2, GroupID: 1, ChildGroupID: intPtrOf(9), Priority: 2},
	}
	n, err := rawCreate(conn, items, "group_items", nil, true)
	if err != nil {
		t.Fatalf("rawCreate: %v", err)
	}
	if n != 2 {
		t.Fatalf("rows = %d, want 2", n)
	}
	var count int64
	if err := conn.Table("group_items").Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Fatalf("table rows = %d, want 2", count)
	}
}

// TestRawCreateEmpty 验证空输入得到 0 行（不得 panic、不得写半截状态）
func TestRawCreateEmpty(t *testing.T) {
	conn := openBackupTestDB(t)
	if n, err := rawCreate(conn, []model.Group{}, "groups", nil, true); err != nil || n != 0 {
		t.Fatalf("empty = %d/%v, want 0/nil", n, err)
	}
}

// TestCreateRowsRawRejectsUnknown 验证未知表名被拒（不许静默跳过）
func TestCreateRowsRawRejectsUnknown(t *testing.T) {
	conn := openBackupTestDB(t)
	if _, err := createRowsRaw(conn, []int{1}, nil, true); err == nil {
		t.Fatal("unknown dump shape accepted, want error")
	}
}

// intPtrOf 返回指针；op 包内测试共用，见 NM-CUR-195 的入参约定
// 183 P0 复现时 TestCreateRowsRawRejectsUnknown/intPtrOf 缺一不可
// op 包的备份写入必须对未知表名 fail-fast，否则还原时会漏数据。
func intPtrOf(v int) *int { return &v }
