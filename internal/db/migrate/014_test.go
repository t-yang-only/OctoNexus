package migrate

import (
	"encoding/json"
	"testing"

	"gorm.io/gorm"
)

// 本文件是 T-group-002（存量分组统一开启流式无进展上限）的演练单测：
// 缺失该键 / 显式为 0 的分组被回填成 300；已配成正数的分组逐字不动；
// 其它字段（用户改过的尝试次数等）在回填后保持不变；解析不了的行原样跳过。

// createLegacyGroups 造一张只有 id/name/relay_config 的旧形状 groups 表（迁移只碰这三列）。
func createLegacyGroups(t *testing.T, conn *gorm.DB, configs map[int]string) {
	t.Helper()
	if err := conn.Exec(`CREATE TABLE groups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		relay_config TEXT
	)`).Error; err != nil {
		t.Fatalf("create legacy groups: %v", err)
	}
	for id, config := range configs {
		if err := conn.Exec(`INSERT INTO groups (id, name, relay_config) VALUES (?, ?, ?)`,
			id, "g", config).Error; err != nil {
			t.Fatalf("seed group %d: %v", id, err)
		}
	}
}

func readRelayConfig(t *testing.T, conn *gorm.DB, id int) map[string]any {
	t.Helper()
	var raw string
	if err := conn.Table("groups").Select("relay_config").Where("id = ?", id).Scan(&raw).Error; err != nil {
		t.Fatalf("read group %d: %v", id, err)
	}
	config := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatalf("group %d relay_config is not JSON: %v (%q)", id, err, raw)
	}
	return config
}

func TestMigrate14BackfillsStreamIdleTimeout(t *testing.T) {
	conn := openMigrateTestDB(t)
	createLegacyGroups(t, conn, map[int]string{
		1: `{"member_max_attempts":5}`,                                        // 存量形状: 没有该键
		2: `{"member_stream_idle_timeout_seconds":0}`,                         // 用户显式关掉
		3: `{"member_stream_idle_timeout_seconds":90}`,                        // 用户配过: 必须保留
		4: `{"member_max_attempts":3,"member_stream_idle_timeout_seconds":0}`, // 显式 0 + 改过的尝试次数
		5: `not json`,                                                         // 坏行: 原样跳过
	})

	if err := migrateGroupStreamIdleTimeout(conn); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// 1/2/4 被回填成 300。
	for _, id := range []int{1, 2, 4} {
		config := readRelayConfig(t, conn, id)
		if got := config["member_stream_idle_timeout_seconds"]; got != float64(300) {
			t.Fatalf("group %d idle timeout = %v, want 300", id, got)
		}
	}
	// 3 保持用户配的 90。
	if got := readRelayConfig(t, conn, 3)["member_stream_idle_timeout_seconds"]; got != float64(90) {
		t.Fatalf("group 3 idle timeout = %v, want 90 (用户配过的值必须保留)", got)
	}
	// 回填不动同行的其它字段。
	if got := readRelayConfig(t, conn, 1)["member_max_attempts"]; got != float64(5) {
		t.Fatalf("group 1 member_max_attempts = %v, want 5 (只补一个键, 不整体重置)", got)
	}
	if got := readRelayConfig(t, conn, 4)["member_max_attempts"]; got != float64(3) {
		t.Fatalf("group 4 member_max_attempts = %v, want 3", got)
	}
	// 坏行原样保留（不覆盖、不删）。
	var raw string
	if err := conn.Table("groups").Select("relay_config").Where("id = ?", 5).Scan(&raw).Error; err != nil {
		t.Fatalf("read group 5: %v", err)
	}
	if raw != "not json" {
		t.Fatalf("group 5 relay_config = %q, want 原样保留", raw)
	}
}

// TestMigrate14IsIdempotent 重跑不产生副作用：已经是 300 的分组不会被改写（值不动、键不动）。
func TestMigrate14IsIdempotent(t *testing.T) {
	conn := openMigrateTestDB(t)
	createLegacyGroups(t, conn, map[int]string{
		1: `{"member_stream_idle_timeout_seconds":300}`,
	})
	before := readRelayConfig(t, conn, 1)
	if err := migrateGroupStreamIdleTimeout(conn); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := migrateGroupStreamIdleTimeout(conn); err != nil {
		t.Fatalf("second run: %v", err)
	}
	after := readRelayConfig(t, conn, 1)
	if len(after) != len(before) {
		t.Fatalf("key set changed: before=%v after=%v", before, after)
	}
	if after["member_stream_idle_timeout_seconds"] != float64(300) {
		t.Fatalf("idle timeout = %v, want 300", after["member_stream_idle_timeout_seconds"])
	}
}
