package op

// 分组快速建立的判据（需求1）。
//
// # 要防住的事
//
//  1. **建出空分组** —— (渠道, 模型) 取不到可用凭据时必须报错，而不是建一个
//     调不通的组。空分组比没有分组更糟：它会出现在列表里，用户点了才知道不通。
//  2. **幂等** —— 重复点同一个模型不该报错，应复用已建的分组。
//  3. **成员不同时不许静默复用** —— 同名分组若成员不一样，复用会悄悄改变调用行为。
//  4. **禁用凭据不进组** —— 否则建出来的组必然失败。

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var quickGroupSeq int64

func openQuickGroupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:quickgrp-%s-%d?mode=memory&cache=shared", t.Name(), atomic.AddInt64(&quickGroupSeq, 1))), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{},
		&model.ChannelGrant{}, &model.Group{}, &model.GroupItem{},
		&model.Setting{}, &model.APIKey{}, &model.RelayLog{},
		&model.LLMInfo{}, &model.StatsTotal{}, &model.StatsDaily{},
		&model.StatsHourly{}, &model.StatsAPIKey{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))
	return conn
}

// seedQuickGroupChannel 建一个渠道 + 两个可用凭据 + 一个模型，返回 (渠道ID, 模型名, 两个授权ID)。
func seedQuickGroupChannel(t *testing.T, conn *gorm.DB) (int, string, []int) {
	t.Helper()
	ch := model.Channel{ChannelConfig: model.ChannelConfig{Name: "53HK-L", Enabled: true}}
	if err := conn.Create(&ch).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	cm := model.ChannelModel{ChannelID: ch.ID, Name: "claude-fable-5"}
	if err := conn.Create(&cm).Error; err != nil {
		t.Fatalf("seed model: %v", err)
	}
	var grantIDs []int
	for i, keyName := range []string{"key-a", "key-b"} {
		key := model.ChannelKey{ChannelID: ch.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: keyName, Enabled: true}}
		if err := conn.Create(&key).Error; err != nil {
			t.Fatalf("seed key: %v", err)
		}
		g := model.ChannelGrant{ChannelModelID: cm.ID, ChannelKeyID: key.ID, Protocols: 1}
		if err := conn.Create(&g).Error; err != nil {
			t.Fatalf("seed grant: %v", err)
		}
		grantIDs = append(grantIDs, g.ID)
		_ = i
	}
	if err := InitCache(); err != nil {
		t.Fatalf("InitCache: %v", err)
	}
	return ch.ID, "claude-fable-5", grantIDs
}

// 建组成功：组名是 `渠道/模型`，两个可用凭据都进组。
func TestGroupQuickCreateBuildsChannelSlashModelGroup(t *testing.T) {
	conn := openQuickGroupTestDB(t)
	chID, modelName, grants := seedQuickGroupChannel(t, conn)

	result, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{
		ChannelID: chID, ModelName: modelName,
	})
	if err != nil {
		t.Fatalf("GroupQuickCreate: %v", err)
	}
	if result.Group.Name != "53HK-L/claude-fable-5" {
		t.Errorf("组名 = %q, want 53HK-L/claude-fable-5", result.Group.Name)
	}
	if len(result.Group.Items) != 2 {
		t.Errorf("两个可用凭据都应进组，实得 %d", len(result.Group.Items))
	}
	if len(result.KeyNames) != 2 {
		t.Errorf("应回带凭据名供界面说明，实得 %d", len(result.KeyNames))
	}
	if result.Reused {
		t.Error("首次创建不该标记为复用")
	}
	// 默认模式应是 failover：多凭据时这才具备故障转移能力。
	if result.Group.Mode != model.GroupModeFailover {
		t.Errorf("默认模式 = %q, want failover", result.Group.Mode)
	}
	_ = grants
}

// 模型没有可用凭据时必须报错，不建空分组。
func TestGroupQuickCreateRejectsWhenNoGrant(t *testing.T) {
	conn := openQuickGroupTestDB(t)
	ch := model.Channel{ChannelConfig: model.ChannelConfig{Name: "Empty", Enabled: true}}
	if err := conn.Create(&ch).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := InitCache(); err != nil {
		t.Fatalf("InitCache: %v", err)
	}

	_, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{
		ChannelID: ch.ID, ModelName: "no-such-model",
	})
	if err == nil {
		t.Fatal("没有可用凭据时应报错")
	}
	// 报错必须说清是哪个渠道哪个模型（否则用户不知道该去补什么）。
	if !hasSubstr(err.Error(), "Empty") || !hasSubstr(err.Error(), "no-such-model") {
		t.Errorf("错误应点名渠道与模型，实得：%v", err)
	}
	// 且不留下任何分组。
	var n int64
	conn.Model(&model.Group{}).Count(&n)
	if n != 0 {
		t.Errorf("不该建出任何分组，实得 %d", n)
	}
}

// 幂等：重复建同一 (渠道, 模型) 应复用，不报错、不建第二组。
func TestGroupQuickCreateIsIdempotent(t *testing.T) {
	conn := openQuickGroupTestDB(t)
	chID, modelName, _ := seedQuickGroupChannel(t, conn)

	first, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{ChannelID: chID, ModelName: modelName})
	if err != nil {
		t.Fatalf("首次: %v", err)
	}
	second, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{ChannelID: chID, ModelName: modelName})
	if err != nil {
		t.Fatalf("第二次不该报错: %v", err)
	}
	if !second.Reused {
		t.Error("第二次应标记为复用")
	}
	if second.Group.ID != first.Group.ID {
		t.Errorf("应复用同一分组，实得 %d vs %d", second.Group.ID, first.Group.ID)
	}
	var n int64
	conn.Model(&model.Group{}).Count(&n)
	if n != 1 {
		t.Errorf("只应有 1 个分组，实得 %d", n)
	}
}

// 同名分组但成员不同：必须报错，不许静默复用（那会悄悄改变调用行为）。
func TestGroupQuickCreateRejectsWhenMembersDiffer(t *testing.T) {
	conn := openQuickGroupTestDB(t)
	chID, modelName, grants := seedQuickGroupChannel(t, conn)

	// 先建一个只有第一个凭据的同名分组。
	if _, err := GroupCreate(&model.GroupCreateRequest{
		Name: "53HK-L/claude-fable-5", Mode: model.GroupModeManual,
		Items: []model.GroupItemInput{{ChannelGrantID: grants[0]}},
	}, context.Background()); err != nil {
		t.Fatalf("预建: %v", err)
	}

	_, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{ChannelID: chID, ModelName: modelName})
	if err == nil {
		t.Fatal("成员不同时应报错")
	}
	if !hasSubstr(err.Error(), "成员不同") {
		t.Errorf("错误应说明是成员不同，实得：%v", err)
	}
}

// 成员数相同但凭据不同：长度比对兜不住，必须靠集合比对拦住。
//
// 这条是给 M3 变异准备的隔离用例 —— 上面的用例长度是 1 vs 2，长度检查先命中，
// 集合比对是否生效根本测不出来（本轮实测：把 sameGrantSet 改成恒真，上面那条照样绿）。
func TestGroupQuickCreateRejectsWhenSameCountDifferentMembers(t *testing.T) {
	conn := openQuickGroupTestDB(t)
	chID, modelName, grants := seedQuickGroupChannel(t, conn)

	// 预建同名分组，成员 = 第二把凭据（数量与下面要建的 1 个相同，但身份不同）。
	if _, err := GroupCreate(&model.GroupCreateRequest{
		Name: "53HK-L/claude-fable-5", Mode: model.GroupModeManual,
		Items: []model.GroupItemInput{{ChannelGrantID: grants[1]}},
	}, context.Background()); err != nil {
		t.Fatalf("预建: %v", err)
	}

	// 只用第一把凭据快速建组：算出的成员集合与已有分组等长但不同。
	_, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{
		ChannelID: chID, ModelName: modelName, KeyID: keyIDByName(t, conn, chID, "key-a"),
	})
	if err == nil {
		t.Fatal("成员集合不同（即便数量相同）时应报错，否则调用会静默跑在另一把凭据上")
	}
	if !hasSubstr(err.Error(), "成员不同") {
		t.Errorf("错误应说明是成员不同，实得：%v", err)
	}
}

// 指定凭据时只取那一把：这条同时是 keyID 筛选的判据 ——
// 早先写成 `cand.ID == 0`（对真实授权恒假），筛选等于不存在，
// 用户指定了凭据却照旧把两把都拉进组。
func TestGroupQuickCreateFiltersByKeyID(t *testing.T) {
	conn := openQuickGroupTestDB(t)
	chID, modelName, _ := seedQuickGroupChannel(t, conn)

	result, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{
		ChannelID: chID, ModelName: modelName, KeyID: keyIDByName(t, conn, chID, "key-a"),
	})
	if err != nil {
		t.Fatalf("GroupQuickCreate: %v", err)
	}
	if len(result.Group.Items) != 1 {
		t.Fatalf("指定单把凭据后应只有 1 个成员，实得 %d", len(result.Group.Items))
	}
	if len(result.KeyNames) != 1 || result.KeyNames[0] != "key-a" {
		t.Errorf("入选凭据应是 key-a，实得 %v", result.KeyNames)
	}
}

// keyIDByName 按名称取凭据 ID。
func keyIDByName(t *testing.T, conn *gorm.DB, channelID int, name string) int {
	t.Helper()
	var key model.ChannelKey
	if err := conn.Where("channel_id = ? AND name = ?", channelID, name).First(&key).Error; err != nil {
		t.Fatalf("lookup key %s: %v", name, err)
	}
	return key.ID
}

// 禁用凭据不进组：建出来的组必须只含可用凭据。
func TestGroupQuickCreateSkipsDisabledKeys(t *testing.T) {
	conn := openQuickGroupTestDB(t)
	chID, modelName, _ := seedQuickGroupChannel(t, conn)
	// 禁用第二把凭据。
	if err := conn.Model(&model.ChannelKey{}).Where("name = ?", "key-b").Update("enabled", false).Error; err != nil {
		t.Fatalf("disable: %v", err)
	}
	// 建组前刷新缓存，让 Available 判定看到新状态。
	if err := InitCache(); err != nil {
		t.Fatalf("InitCache: %v", err)
	}

	result, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{ChannelID: chID, ModelName: modelName})
	if err != nil {
		t.Fatalf("GroupQuickCreate: %v", err)
	}
	if len(result.Group.Items) != 1 {
		t.Errorf("只应入选 1 个可用凭据，实得 %d", len(result.Group.Items))
	}
}

// 渠道不存在要报错（而不是建出一个名字里带空渠道名的组）。
func TestGroupQuickCreateRejectsUnknownChannel(t *testing.T) {
	openQuickGroupTestDB(t)
	_, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{
		ChannelID: 9999, ModelName: "m",
	})
	if err == nil {
		t.Fatal("渠道不存在应报错")
	}
}

// 模型名为空要报错（binding 挡了一层，op 层自己也要挡）。
func TestGroupQuickCreateRejectsEmptyModel(t *testing.T) {
	openQuickGroupTestDB(t)
	_, err := GroupQuickCreate(context.Background(), &QuickGroupRequest{
		ChannelID: 1, ModelName: "   ",
	})
	if err == nil {
		t.Fatal("空模型名应报错")
	}
}

func hasSubstr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
