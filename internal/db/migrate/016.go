package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	// 必须用 After：这一列由紧随的 AutoMigrate 才建出来，
	// 注册成 Before 时列还不存在，hasPhysicalColumn 会直接跳过，
	// 结果是存量行的值停在 NULL（json 序列化器读 NULL 会失败，
	// 表现成"升级后老 Key 全用不了"）——实测踩到。
	RegisterAfterAutoMigration(Migration{
		Version: 16,
		Up:      migrateAPIKeyAllowedCIDRs,
	})
}

// migrateAPIKeyAllowedCIDRs 给 api_keys.allowed_cidrs 回填空数组。
//
// 为什么需要回填：这一列由 GORM 的 json 序列化器读写，存量行在 ALTER 之后是 NULL，
// 反序列化 NULL 会失败（而不是得到空切片），于是"升级后老 Key 全部用不了"。
// 空数组的语义正是「不限制」，与升级前的行为逐字一致。
//
// 幂等：已经是有值（含 "[]"）的行跳过，中途失败可重跑。
func migrateAPIKeyAllowedCIDRs(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	// 表或列不存在都直接返回：本迁移只负责改写取值，
	// 建列这件事交给随后的 AutoMigrate（与 012 的分工一致）。
	if !db.Migrator().HasTable("api_keys") || !hasPhysicalColumn(db, "api_keys", "allowed_cidrs") {
		return nil
	}

	type apiKeyRow struct {
		ID           int     // API Key 主键。
		AllowedCIDRs *string // 迁移前的取值；NULL 表示尚未回填。
	}
	rows := make([]apiKeyRow, 0)
	if err := db.Table("api_keys").Select("id, allowed_cidrs").Find(&rows).Error; err != nil {
		return fmt.Errorf("failed to read api_keys: %w", err)
	}

	for _, row := range rows {
		if row.AllowedCIDRs != nil && *row.AllowedCIDRs != "" {
			continue // 已有取值（含 "[]"）不动，保证可重跑
		}
		if err := db.Table("api_keys").Where("id = ?", row.ID).
			Update("allowed_cidrs", "[]").Error; err != nil {
			return fmt.Errorf("failed to backfill api_key %d allowed_cidrs: %w", row.ID, err)
		}
	}
	return nil
}
