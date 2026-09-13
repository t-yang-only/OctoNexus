package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterBeforeAutoMigration(Migration{
		Version: 13,
		Up:      migrateGroupItemChildRef,
	})
}

// migrateGroupItemChildRef 为分组嵌套引用补齐 child_group_id 列。
// 旧表唯一索引 idx_group_grant 建在 (group_id, channel_grant_id) 且 channel_grant_id NOT NULL;
// 新模型允许 channel_grant_id 为 0 (引用子分组时), 并为 child_group_id 建独立唯一索引。
// SQLite 改列约束须整表重建, 重建在事务外执行: DROP 原表在外键开启下会级联删子表行,
// 与 011 同因。列的存在性即执行标记, 中途失败重跑时跳过已完成的步骤。
func migrateGroupItemChildRef(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("group_items") {
		return nil
	}
	if hasPhysicalColumn(db, "group_items", "child_group_id") {
		return nil
	}

	// 整表重建: 按当前模型形状建新表 (AutoMigrate 只建 group_items 一张, 不回溯父表),
	// 旧数据按 (group_id, channel_grant_id) 原样搬运 —— 历史行全部是授权引用, child_group_id 恒为 0。
	type legacyItemRow struct {
		ID             int // 分组项主键。
		GroupID        int // 所属分组主键。
		ChannelGrantID int // 引用的渠道授权 ID。
		Priority       int // 展示与故障转移顺序。
	}
	legacyItems := make([]legacyItemRow, 0)
	if err := db.Table("group_items").Select("id, group_id, channel_grant_id, priority").
		Order("id ASC").Find(&legacyItems).Error; err != nil {
		return fmt.Errorf("failed to read legacy group_items: %w", err)
	}

	if err := db.Migrator().DropTable("group_items"); err != nil {
		return fmt.Errorf("failed to drop legacy group_items: %w", err)
	}
	if err := db.AutoMigrate(&model.GroupItem{}); err != nil {
		return fmt.Errorf("failed to create group_items: %w", err)
	}
	if len(legacyItems) > 0 {
		items := make([]model.GroupItem, 0, len(legacyItems))
		for _, item := range legacyItems {
			grantID := item.ChannelGrantID
			items = append(items, model.GroupItem{
				ID:             item.ID,
				GroupID:        item.GroupID,
				ChannelGrantID: &grantID,
				Priority:       item.Priority,
			})
		}
		if err := db.Create(&items).Error; err != nil {
			return fmt.Errorf("failed to restore migrated group_items: %w", err)
		}
	}
	return clearStaleActiveItems(db)
}
