package migrate

import (
	"fmt"

	"gorm.io/gorm"
)

func init() {
	// 在 AutoMigrate 之前运行：
	// 清理早期测试版可能残留在全局 SQLite 空间中的冲突索引名（idx_sub2api_platform），
	// 防止新表 sub2api_accounts 在建索引时触发 "index idx_sub2api_platform already exists"
	RegisterBeforeAutoMigration(Migration{
		Version: 17,
		Up:      cleanLegacySub2APIIndex,
	})
}

func cleanLegacySub2APIIndex(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	// 无论是否存在，安全 DROP INDEX
	_ = db.Exec("DROP INDEX IF EXISTS idx_sub2api_platform").Error
	return nil
}