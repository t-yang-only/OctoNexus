package migrate

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// 本文件锁住 009 的一个真实缺陷（全新安装第一轮没有请求日志表）：
//
// 009 在 AutoMigrate **之后**运行。全新库启动时 AutoMigrate 先按当前模型建出 relay_logs，
// 紧接着 009 无条件把它 DROP 掉；而 009 的迁移记录已经写下，后续启动不再重跑，
// 于是新装实例整轮都没有 relay_logs：RelayLogSave 忽略落库错误，请求日志被静默丢弃，
// 日志页与导出全空，重启一次才补回空表。
// 实测复现：全新库第一轮 20 张表且无 relay_logs，重启后 21 张。
//
// 修法是"先判形状再删"：新格式表保留，旧格式表（2026-09 改造前的列）照旧删掉。
// 下面三个用例分别覆盖全新安装、旧库升级、以及同批的 base_urls 删列行为。

// seedRelayLogsCurrentShape 按当前模型建出 relay_logs（等同全新库 AutoMigrate 的结果）。
func seedRelayLogsCurrentShape(t *testing.T, conn *gorm.DB) {
	t.Helper()
	if err := conn.AutoMigrate(&model.RelayLog{}); err != nil {
		t.Fatalf("auto migrate RelayLog: %v", err)
	}
}

// seedRelayLogsLegacyShape 按 2026-09 改造前的形状手搓 relay_logs（列与新格式无一处同名）。
func seedRelayLogsLegacyShape(t *testing.T, conn *gorm.DB) {
	t.Helper()
	if err := conn.Exec(`CREATE TABLE relay_logs (
		id INTEGER PRIMARY KEY,
		time INTEGER,
		request_model_name TEXT,
		channel INTEGER,
		actual_model_name TEXT,
		input_tokens INTEGER,
		output_tokens INTEGER,
		ftut INTEGER,
		use_time INTEGER,
		cost REAL,
		request_content TEXT,
		response_content TEXT
	)`).Error; err != nil {
		t.Fatalf("create legacy relay_logs: %v", err)
	}
	if err := conn.Exec(`INSERT INTO relay_logs (id, time, request_model_name, channel, actual_model_name,` +
		` input_tokens, output_tokens, ftut, use_time, cost, request_content, response_content)` +
		` VALUES (1, 100, 'gpt-5.6', 3, 'gpt-5.6', 10, 20, 123, 456, 0.01, 'hi', 'hello')`).Error; err != nil {
		t.Fatalf("seed legacy log row: %v", err)
	}
}

// TestMigrate9KeepsFreshRelayLogs 锁住缺陷本体：全新库（当前形状的空表）不得被删。
func TestMigrate9KeepsFreshRelayLogs(t *testing.T) {
	conn := openMigrateTestDB(t)
	seedRelayLogsCurrentShape(t, conn)

	if err := migrateDropLegacyChannelSchema(conn); err != nil {
		t.Fatalf("run 009 on fresh shape: %v", err)
	}

	if !conn.Migrator().HasTable("relay_logs") {
		t.Fatal("relay_logs was dropped on a fresh install: 新装第一轮会没有请求日志表（正是要修的缺陷）")
	}
	// 不只是"表还在"：还要确认它真能落一行（列齐全，写入不报错）。
	entry := model.RelayLog{RequestID: 7, Status: "success", Model: "gpt-x", TargetChannel: "53HK"}
	if err := conn.Create(&entry).Error; err != nil {
		t.Fatalf("fresh relay_logs must accept a row: %v", err)
	}
	var loaded []model.RelayLog
	if err := conn.Where("request_id = ?", 7).Find(&loaded).Error; err != nil {
		t.Fatalf("read back the row: %v", err)
	}
	if len(loaded) != 1 || loaded[0].TargetChannel != "53HK" {
		t.Fatalf("read back the row = %v, want one row with target_channel 53HK", loaded)
	}
}

// TestMigrate9DropsLegacyRelayLogs 旧库升级路径保持原意：旧格式日志行照旧清掉。
func TestMigrate9DropsLegacyRelayLogs(t *testing.T) {
	conn := openMigrateTestDB(t)
	seedRelayLogsLegacyShape(t, conn)

	if !legacyRelayLogs(conn) {
		t.Fatal("legacy-shaped relay_logs must be recognised as legacy")
	}
	if err := migrateDropLegacyChannelSchema(conn); err != nil {
		t.Fatalf("run 009 on legacy shape: %v", err)
	}
	if conn.Migrator().HasTable("relay_logs") {
		t.Fatal("legacy relay_logs must be dropped: 旧格式日志行不该留着让日志页读到脏行")
	}
}

// TestMigrate9NoRelayLogsTableIsFine 全新库在 AutoMigrate 之前调用时不应报错（表不存在即无事）。
func TestMigrate9NoRelayLogsTableIsFine(t *testing.T) {
	conn := openMigrateTestDB(t)
	if conn.Migrator().HasTable("relay_logs") {
		t.Fatal("precondition: relay_logs must not exist yet")
	}
	if err := migrateDropLegacyChannelSchema(conn); err != nil {
		t.Fatalf("run 009 without relay_logs: %v", err)
	}
	if conn.Migrator().HasTable("relay_logs") {
		t.Fatal("009 must not create relay_logs itself")
	}
}

// TestMigrate9StillDropsLegacyBaseURLs 同批的旧列清理没有被这次改动带偏：channels.base_urls 照旧删。
func TestMigrate9StillDropsLegacyBaseURLs(t *testing.T) {
	conn := openMigrateTestDB(t)
	if err := conn.Exec(`CREATE TABLE channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT,
		base_urls TEXT
	)`).Error; err != nil {
		t.Fatalf("create legacy channels: %v", err)
	}
	if !hasPhysicalColumn(conn, "channels", "base_urls") {
		t.Fatal("precondition: base_urls must exist")
	}
	seedRelayLogsCurrentShape(t, conn)

	if err := migrateDropLegacyChannelSchema(conn); err != nil {
		t.Fatalf("run 009: %v", err)
	}
	if hasPhysicalColumn(conn, "channels", "base_urls") {
		t.Fatal("legacy channels.base_urls must still be dropped by 009")
	}
	if !conn.Migrator().HasTable("relay_logs") {
		t.Fatal("the fresh relay_logs must survive in the same run")
	}
}
