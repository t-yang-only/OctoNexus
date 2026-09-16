package migrate

import (
	"fmt"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 9,
		Up:      migrateDropLegacyChannelSchema,
	})
}

// legacyRelayLogColumns 是旧形状 relay_logs 独有的列。
//
// relay_logs 在 2026-09 的日志卡片改造里被重新建表（列与新格式无一处同名），
// 因此"旧形状独有的列还在不在"就是这张表新旧的可判据；
// 而 GORM 的 AutoMigrate 只加列、不删列，所以即使它已经给旧表补齐了新格式的列，
// 这些旧列依然存在 —— 判据在 AutoMigrate 之前与之后都成立。
var legacyRelayLogColumns = []string{
	"request_model_name", "actual_model_name", "ftut", "use_time", "request_content",
}

// legacyRelayLogs 判断现存的 relay_logs 是否是需要清掉的旧形状。
func legacyRelayLogs(db *gorm.DB) bool {
	if !db.Migrator().HasTable("relay_logs") {
		return false
	}
	for _, column := range legacyRelayLogColumns {
		if hasPhysicalColumn(db, "relay_logs", column) {
			return true
		}
	}
	return false
}

// migrateDropLegacyChannelSchema 删除遗留的渠道多地址字段和遗留形状的转发日志表。
//
// relay_logs 这里**先判形状再删**，不能无条件 DROP：
// 本迁移在 AutoMigrate 之后运行，全新库此时刚由 AutoMigrate 建好 relay_logs，
// 无条件删除会把新表删掉；而本迁移的记录已经写下、后续启动不会重跑，
// 于是新装的实例整轮都没有请求日志表，RelayLogSave 又忽略错误（落库静默丢弃），
// 表现为"第一次运行期间日志页与导出全空，要重启一次才补回"。
// 实测：全新库第一轮 20 张表且无 relay_logs，重启后 21 张才补回来。
//
// 也不能把本迁移整体挪到 AutoMigrate 之前：同批的 005 依赖遗留的 channels.base_urls 列做迁移，
// 009 提前删列会让 005 失去输入（旧库升级时 base_url 会丢）。故只在原地加形状判据。
//
// 旧形状表仍会被删：它承载的是 2026-09 改造前的旧格式日志行，列与新格式无一处同名，
// 留着只会让日志页读到字段缺失的脏行。
func migrateDropLegacyChannelSchema(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if db.Migrator().HasTable("channels") {
		if err := dropColumnIfExists(db, &model.Channel{}, "channels", "base_urls"); err != nil {
			return err
		}
	}
	if legacyRelayLogs(db) {
		if err := db.Migrator().DropTable("relay_logs"); err != nil {
			return fmt.Errorf("failed to drop legacy relay_logs: %w", err)
		}
	}
	return nil
}
