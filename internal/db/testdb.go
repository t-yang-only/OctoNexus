package db

import "gorm.io/gorm"

// SetDBForTest 在直连内存库测试中替换全局库（仅测试进程使用，勿在生产调用）。
// 返回恢复函数以便测试收尾还原。
func SetDBForTest(replacement *gorm.DB) func() {
	original := db
	db = replacement
	return func() { db = original }
}
