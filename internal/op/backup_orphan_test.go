package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestSanitizeOrphanRowsDropsOnlyUninsertableRows 覆盖上游 PR #344 的同类问题:
// 备份里可能残留"父行早已删除"的子行, 直接插入会撞外键而让整份备份导入失败。
// 过滤只该丢掉确定插不进去的行, 有效行一行不少, 并如实回报丢弃数量。
func TestSanitizeOrphanRowsDropsOnlyUninsertableRows(t *testing.T) {
	conn := openBackupTestDB(t)
	if err := conn.AutoMigrate(&model.APIKey{}, &model.StatsAPIKey{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	grantToLive := 30
	grantToDropped := 31
	childLive := 40
	childMissing := 41
	dump := &model.DBDump{
		Channels: []model.Channel{{ID: 1}},
		ChannelKeys: []model.ChannelKey{
			{ID: 10, ChannelID: 1},  // 有效
			{ID: 11, ChannelID: 99}, // 孤儿: 渠道 99 不在转储里也不在库里
		},
		ChannelModels: []model.ChannelModel{
			{ID: 20, ChannelID: 1},
			{ID: 21, ChannelID: 99},
		},
		ChannelGrants: []model.ChannelGrant{
			{ID: 30, ChannelModelID: 20, ChannelKeyID: 10}, // 有效
			{ID: 31, ChannelModelID: 21, ChannelKeyID: 10}, // 孤儿: 模型 21 会被丢掉
			{ID: 32, ChannelModelID: 20, ChannelKeyID: 11}, // 孤儿: 凭据 11 会被丢掉
		},
		Groups: []model.Group{{ID: 40}},
		GroupItems: []model.GroupItem{
			{ID: 50, GroupID: 40, ChannelGrantID: &grantToLive},    // 有效
			{ID: 51, GroupID: 41, ChannelGrantID: &grantToLive},    // 孤儿: 分组 41 不存在
			{ID: 52, GroupID: 40, ChannelGrantID: &grantToDropped}, // 孤儿: 授权 31 会被丢掉
			{ID: 53, GroupID: 40, ChildGroupID: &childMissing},     // 孤儿: 子分组 41 不存在
			{ID: 54, GroupID: 40, ChildGroupID: &childLive},        // 有效: 子分组就是分组 40
		},
		APIKeys: []model.APIKey{{ID: 60}},
		StatsAPIKey: []model.StatsAPIKey{
			{APIKeyID: 60}, // 有效
			{APIKeyID: 61}, // 孤儿: Key 61 不存在
		},
	}

	skipped, err := sanitizeOrphanRows(conn, dump)
	if err != nil {
		t.Fatalf("sanitize 失败: %v", err)
	}

	want := map[string]int64{"channel_keys": 1, "channel_models": 1, "channel_grants": 2, "group_items": 3, "stats_api_key": 1}
	for table, count := range want {
		if skipped[table] != count {
			t.Errorf("%s 丢弃数=%d, 期望 %d（全部: %v）", table, skipped[table], count, skipped)
		}
	}
	if len(skipped) != len(want) {
		t.Errorf("不应报告其它表的丢弃: %v", skipped)
	}

	if len(dump.ChannelKeys) != 1 || dump.ChannelKeys[0].ID != 10 {
		t.Errorf("有效凭据被误删: %+v", dump.ChannelKeys)
	}
	if len(dump.ChannelModels) != 1 || dump.ChannelModels[0].ID != 20 {
		t.Errorf("有效模型被误删: %+v", dump.ChannelModels)
	}
	if len(dump.ChannelGrants) != 1 || dump.ChannelGrants[0].ID != 30 {
		t.Errorf("有效授权被误删: %+v", dump.ChannelGrants)
	}
	if len(dump.GroupItems) != 2 {
		t.Fatalf("有效成员数量不对: %+v", dump.GroupItems)
	}
	if dump.GroupItems[0].ID != 50 || dump.GroupItems[1].ID != 54 {
		t.Errorf("留下的成员不是 50/54: %+v", dump.GroupItems)
	}
	if len(dump.StatsAPIKey) != 1 || dump.StatsAPIKey[0].APIKeyID != 60 {
		t.Errorf("有效 Key 统计被误删: %+v", dump.StatsAPIKey)
	}
}

// TestSanitizeOrphanRowsAcceptsParentsAlreadyInDatabase 增量导入: 父行可能早就在库里,
// 此时转储里的子行是合法的, 不能因为没有一起导出就被丢掉。
func TestSanitizeOrphanRowsAcceptsParentsAlreadyInDatabase(t *testing.T) {
	conn := openBackupTestDB(t)
	if err := conn.AutoMigrate(&model.APIKey{}, &model.StatsAPIKey{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := conn.Create(&model.Channel{ID: 7, ChannelConfig: model.ChannelConfig{Name: "existing",
		BaseURL: "http://127.0.0.1:18099/v1", Enabled: true}}).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	dump := &model.DBDump{
		// 转储里没有渠道 7, 但它已在库里 ⇒ 引用它的凭据有效。
		ChannelKeys: []model.ChannelKey{{ID: 10, ChannelID: 7}},
	}

	skipped, err := sanitizeOrphanRows(conn, dump)
	if err != nil {
		t.Fatalf("sanitize 失败: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("不该丢任何行: %v", skipped)
	}
	if len(dump.ChannelKeys) != 1 {
		t.Fatalf("引用库内渠道的凭据被误删: %+v", dump.ChannelKeys)
	}
}
