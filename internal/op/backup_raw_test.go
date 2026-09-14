package op

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
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

// TestRawCreateSkipsAssociations ?? 183 P0 ???????? rawCreate ??????
// ?? "rows 0 ? group_items ????" ? reflect ???? panic?
// rawCreate ???????? GroupItem.ChannelGrantID ??? ChannelGrant??????
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

// TestRawCreateEmpty ???????? 0 ??????
func TestRawCreateEmpty(t *testing.T) {
	conn := openBackupTestDB(t)
	if n, err := rawCreate(conn, []model.Group{}, "groups", nil, true); err != nil || n != 0 {
		t.Fatalf("empty = %d/%v, want 0/nil", n, err)
	}
}

// TestCreateRowsRawRejectsUnknown ?????????????????
func TestCreateRowsRawRejectsUnknown(t *testing.T) {
	conn := openBackupTestDB(t)
	if _, err := createRowsRaw(conn, []int{1}, nil, true); err == nil {
		t.Fatal("unknown dump shape accepted, want error")
	}
}

// intPtrOf ???????????op ???????NM-CUR-195 ????
// 183 ??????? TestCreateRowsRawRejectsUnknown/intPtrOf ???
// op ???????????????
func intPtrOf(v int) *int { return &v }
