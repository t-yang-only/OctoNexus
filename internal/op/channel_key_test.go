package op

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// openChannelKeyTestDB 建一个内存 SQLite（glebarez 纯 Go 实现, 不需要 cgo）, 只建本次用到的表。
func openChannelKeyTestDB(t *testing.T) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:channel_key_test?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := conn.AutoMigrate(&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{},
		&model.ChannelGrant{}, &model.Group{}, &model.GroupItem{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
}

// TestChannelCreateHonoursDisabledKey 回归: 创建渠道时显式 enabled:false 的凭据必须真的落库为禁用。
// 修复前 ChannelKeyConfig.Enabled 带 `gorm:"default:true"`, GORM 会把零值字段跳出 INSERT 交给列默认值,
// 于是这条断言会拿到 true（活体实测: create 返回 enabled=true, 库里 channel_keys.enabled=1）。
func TestChannelCreateHonoursDisabledKey(t *testing.T) {
	openChannelKeyTestDB(t)

	disabled := false
	detail := &model.ChannelDetail{
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-keyflag", BaseURL: "http://127.0.0.1:1", Enabled: true},
		Keys: []model.ChannelKeyInput{
			{Name: "off", Key: "sk-off", Enabled: &disabled},
			{Name: "on", Key: "sk-on"}, // 没提交 enabled: 应当默认启用
		},
	}
	created, err := ChannelCreate(detail, context.Background())
	if err != nil {
		t.Fatalf("ChannelCreate: %v", err)
	}
	assertKeyEnabled(t, created.Keys, map[string]bool{"off": false, "on": true})

	// 库里也必须一致（接口形状对但落库被 DEFAULT 覆盖过, 所以直接查行, 不走缓存）。
	var rows []model.ChannelKey
	if err := db.GetDB().Where("channel_id = ?", created.ID).Find(&rows).Error; err != nil {
		t.Fatalf("query channel_keys: %v", err)
	}
	enabledByName := map[string]bool{}
	for _, key := range rows {
		enabledByName[key.Name] = key.Enabled
	}
	if enabledByName["off"] {
		t.Fatalf("库里 off 凭据仍是启用状态: %+v", enabledByName)
	}
	if !enabledByName["on"] {
		t.Fatalf("库里 on 凭据应为启用: %+v", enabledByName)
	}

	// 反过来更新也应生效（走 Updates(map) 路径, 与创建路径互为对照）。
	enable := true
	updated, err := ChannelUpdate(&model.ChannelDetail{
		ID:            created.ID,
		ChannelConfig: model.ChannelConfig{Name: "DS-UNIT-keyflag", BaseURL: "http://127.0.0.1:1", Enabled: true},
		Keys: []model.ChannelKeyInput{
			{Name: "off", Key: "sk-off", Enabled: &enable},
			{Name: "on", Key: "sk-on", Enabled: &disabled},
		},
	}, context.Background())
	if err != nil {
		t.Fatalf("ChannelUpdate: %v", err)
	}
	assertKeyEnabled(t, updated.Keys, map[string]bool{"off": true, "on": false})
}

func assertKeyEnabled(t *testing.T, keys []model.ChannelKeyInput, want map[string]bool) {
	t.Helper()
	if len(keys) != len(want) {
		t.Fatalf("凭据数量 %d, want %d (%+v)", len(keys), len(want), keys)
	}
	for _, key := range keys {
		if key.Enabled == nil {
			t.Fatalf("key %s 的 enabled 不该为空", key.Name)
		}
		if expected, ok := want[key.Name]; ok && *key.Enabled != expected {
			t.Fatalf("key %s enabled=%v, want %v", key.Name, *key.Enabled, expected)
		}
	}
}
