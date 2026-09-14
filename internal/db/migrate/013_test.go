package migrate

import (
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 本文件是 T-group-001 转 done 的演练单测（150 裁决前置）：
// 旧形状表（无 child_group_id、grant 列 NOT NULL 且参与唯一索引）→ 跑 013 →
// 新形状 + 历史行原样搬运；重跑幂等；中途失败重入（表已是新形状）不重复搬数。

var migrateTestSeq int64

func openMigrateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&migrateTestSeq, 1)
	dsn := fmt.Sprintf("file:m13-%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	return conn
}

// createLegacyGroupItems 按迁移 013 的旧形状手搓 group_items：
// channel_grant_id NOT NULL，(group_id, channel_grant_id) 唯一，无 child_group_id。
func createLegacyGroupItems(t *testing.T, conn *gorm.DB, rows int) {
	t.Helper()
	if err := conn.Exec(`CREATE TABLE group_items (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		group_id INTEGER NOT NULL,
		channel_grant_id INTEGER NOT NULL,
		priority INTEGER NOT NULL,
		CONSTRAINT idx_group_grant UNIQUE (group_id, channel_grant_id)
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	for i := 1; i <= rows; i++ {
		if err := conn.Exec(
			`INSERT INTO group_items (id, group_id, channel_grant_id, priority) VALUES (?, ?, ?, ?)`,
			i, 7, 100+i, i,
		).Error; err != nil {
			t.Fatalf("seed legacy row %d: %v", i, err)
		}
	}
}

// TestMigrate13RebuildsLegacyShapeAndReruns 演练：旧形状表迁移到新形状，
// 历史行搬运无损；迁移重跑幂等（列存在即早退，不重建不丢行）。
func TestMigrate13RebuildsLegacyShapeAndReruns(t *testing.T) {
	conn := openMigrateTestDB(t)
	// 013 重建走 db.AutoMigrate(&model.GroupItem{})，其外键与结尾的
	// clearStaleActiveItems 分别需要渠道授权体系与 groups 表先建好。
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{}, &model.Group{}); err != nil {
		t.Fatalf("setup referenced tables: %v", err)
	}
	if conn.Migrator().HasTable("group_items") {
		if err := conn.Migrator().DropTable("group_items"); err != nil {
			t.Fatalf("drop group_items before legacy setup: %v", err)
		}
	}

	createLegacyGroupItems(t, conn, 3)
	if err := migrateGroupItemChildRef(conn); err != nil {
		t.Fatalf("run 013 on legacy shape: %v", err)
	}

	if !hasPhysicalColumn(conn, "group_items", "child_group_id") {
		t.Fatal("child_group_id column missing after migration")
	}
	var migrated []model.GroupItem
	if err := conn.Order("id ASC").Find(&migrated).Error; err != nil {
		t.Fatalf("read migrated rows: %v", err)
	}
	if len(migrated) != 3 {
		t.Fatalf("migrated rows = %d, want 3", len(migrated))
	}
	for i, item := range migrated {
		if item.ID != i+1 || item.GroupID != 7 || item.GrantRef() != 101+i || item.Priority != i+1 {
			t.Fatalf("row %d carried wrongly: %+v", i, item)
		}
		if item.ChildGroupID != nil {
			t.Fatalf("row %d: legacy rows must have NULL child_group_id, got %v", i, *item.ChildGroupID)
		}
	}

	// 重跑演练：列已存在 → 早退 no-op，行数与形状不变。
	if err := migrateGroupItemChildRef(conn); err != nil {
		t.Fatalf("rerun 013 (idempotent): %v", err)
	}
	var count int64
	if err := conn.Model(&model.GroupItem{}).Count(&count).Error; err != nil {
		t.Fatalf("count after rerun: %v", err)
	}
	if count != 3 {
		t.Fatalf("rows after rerun = %d, want 3", count)
	}
}

// TestMigrate13InterruptedReentry 演练"中途失败重跑"：首遍已建新形状表（模拟
// drop 后重建成功、断电前回填未完成的中间态），重入须幂等：列存在即早退，
// 不重建不清列。
func TestMigrate13InterruptedReentry(t *testing.T) {
	conn := openMigrateTestDB(t)
	if err := conn.AutoMigrate(&model.Group{}, &model.GroupItem{}); err != nil {
		t.Fatalf("setup new-shape tables: %v", err)
	}
	if !hasPhysicalColumn(conn, "group_items", "child_group_id") {
		t.Fatal("setup broken: new-shape table must carry child_group_id")
	}
	if err := migrateGroupItemChildRef(conn); err != nil {
		t.Fatalf("reentry on new-shape table: %v", err)
	}
	if !hasPhysicalColumn(conn, "group_items", "child_group_id") {
		t.Fatal("child_group_id must survive reentry")
	}
}

// TestMigrate13NoLegacyTable 全新库（无 group_items 表）直接放行。
func TestMigrate13NoLegacyTable(t *testing.T) {
	conn := openMigrateTestDB(t)
	if err := migrateGroupItemChildRef(conn); err != nil {
		t.Fatalf("fresh db: %v", err)
	}
}

// TestMigrate13NilDB 空库防御。
func TestMigrate13NilDB(t *testing.T) {
	if err := migrateGroupItemChildRef(nil); err == nil {
		t.Fatal("nil db: want error")
	}
}
