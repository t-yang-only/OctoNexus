package db

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// 全新安装冒烟（T-deploy-004）：在一张空库上跑完整的 InitDB，断言关键表都在。
//
// 为什么必须有这条：AutoMigrate 的清单是**手写**的，新增模型（如本轮的手动订阅
// manual_subscriptions）时忘记加一行，单测全会通过、`go build` 也过，
// 只有真正全新安装的用户会在运行时撞上 "no such table"。
// 这条用例把"清单齐全"变成可执行判据，代价是打开一个临时 SQLite 文件。
func TestFreshInstallCreatesAllTables(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "fresh.db")
	if err := InitDB("sqlite", dsn, false); err != nil {
		t.Fatalf("InitDB 失败: %v", err)
	}
	conn := GetDB()
	if conn == nil {
		t.Fatalf("GetDB 返回 nil")
	}
	t.Cleanup(func() {
		if sqlDB, err := conn.DB(); err == nil && sqlDB != nil {
			sqlDB.Close()
		}
	})

	// 与 db.go 里 AutoMigrate 的清单一一对应：漏登记模型 = 全新库缺表 = 用户侧 no such table。
	models := []any{
		&model.User{},
		&model.Channel{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
		&model.Group{},
		&model.GroupItem{},
		&model.LLMInfo{},
		&model.APIKey{},
		&model.Setting{},
		&model.StatsTotal{},
		&model.StatsDaily{},
		&model.StatsHourly{},
		&model.StatsAPIKey{},
		&model.RelayLog{},
		&model.QuotaAction{},
		&model.OfficialAccount{},
		&model.JumpToken{},
		&model.UsageHourly{},
		&model.ManualSubscription{},
		&model.ProxyNode{},
		&model.ProxySubscription{},
		&model.PriceSnapshot{},
		&model.UsageSnapshot{},
		&model.Plugin{},
		&model.CredentialSource{},
	}
	missing := []string{}
	for _, item := range models {
		if !conn.Migrator().HasTable(item) {
			name := reflect.TypeOf(item).Elem().Name()
			missing = append(missing, conn.NamingStrategy.TableName(name))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("全新安装缺表: %v（AutoMigrate 清单漏了模型？）", missing)
	}

	// 表数不该少于模型数（多出来的表可能是迁移过程的遗留，不报错）。
	var tables []string
	if err := conn.Raw("select name from sqlite_master where type='table'").Scan(&tables).Error; err != nil {
		t.Fatalf("读表清单失败: %v", err)
	}
	if len(tables) < len(models) {
		t.Fatalf("表数 %d 少于模型数 %d: %v", len(tables), len(models), tables)
	}
	t.Logf("全新安装建表 %d 张（模型 %d 个）", len(tables), len(models))
}
