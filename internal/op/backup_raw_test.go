package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openBackupTestDB ???????????????????NM-CUR-042 count-safe ??????
func openBackupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
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
