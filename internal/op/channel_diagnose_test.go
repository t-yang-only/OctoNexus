package op

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-usability-001 渠道可用性诊断的判据。
//
// 这个功能回答的是「我配好的模型为什么用不上」。一个模型要能被客户端调用，
// 必须三层齐全：模型 → 凭据授权 → 分组。任何一层断掉，客户端都只看到
// model not found，而界面上看不出是哪一层。
//
// 所以判据的重点不是「能不能跑」，而是**断在哪一层、原因对不对**：
// 原因写错会把用户引到错误的修复方向（比如让他去查分组名，实际问题是没有授权）。

// 三层齐全 → 可用。
func TestDiagnoseUsableWhenAllThreeLayersPresent(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "full", Enabled: true}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk", Enabled: true}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	m := model.ChannelModel{ChannelID: channel.ID, Name: "m1"}
	if err := conn.Create(&m).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	grant := model.ChannelGrant{ChannelModelID: m.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := conn.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	groupName, err := AutoGroupName("full", "m1")
	if err != nil {
		t.Fatalf("group name: %v", err)
	}
	if err := conn.Create(&model.Group{Name: groupName}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}

	items, err := ChannelDiagnoseAll(context.Background(), channel.ID)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("channels=%d, want 1", len(items))
	}
	got := items[0]
	if got.TotalCount != 1 || got.UsableCount != 1 {
		t.Fatalf("usable=%d/%d, want 1/1；broken=%+v", got.UsableCount, got.TotalCount, got.Broken)
	}
	if !got.Models[0].Usable {
		t.Fatalf("三层齐全应判为可用，实得 reason=%q", got.Models[0].Reason)
	}
	if got.Models[0].GroupName != groupName {
		t.Fatalf("分组名=%q, want %q", got.Models[0].GroupName, groupName)
	}
}

// **没有授权** → 不可用，且原因必须指向「授权」而不是别的层。
//
// 这是生产上实测最常见的一种（91 个模型卡在这里），原因写错会让用户
// 跑去查分组名或渠道开关，白费一圈。
func TestDiagnoseBrokenWhenNoGrant(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "nogrant", Enabled: true}}
	conn.Create(&channel)
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk", Enabled: true}}
	conn.Create(&key)
	m := model.ChannelModel{ChannelID: channel.ID, Name: "orphan-model"}
	conn.Create(&m)

	items, err := ChannelDiagnoseAll(context.Background(), channel.ID)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	got := items[0]
	if got.UsableCount != 0 {
		t.Fatalf("无授权不该判为可用")
	}
	if len(got.Broken) != 1 {
		t.Fatalf("broken=%d, want 1", len(got.Broken))
	}
	gap := got.Broken[0]
	if gap.ModelName != "orphan-model" {
		t.Fatalf("模型名=%q", gap.ModelName)
	}
	if len(gap.GrantKeys) != 0 {
		t.Fatalf("不该有授权凭据，实得 %v", gap.GrantKeys)
	}
	if gap.Reason == "" || !contains(gap.Reason, "授权") {
		t.Fatalf("原因应指向授权，实得 %q", gap.Reason)
	}
}

// 凭据被禁用 → 不可用，且原因与「没授权」不同。
//
// 反例：把禁用凭据上的授权当成有效授权，诊断会说「可用」，
// 而实际调用会被 ChannelGrantGet 以 "channel key is disabled" 拒绝。
func TestDiagnoseDisabledKeyMakesGrantIneffective(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "diskey", Enabled: true}}
	conn.Create(&channel)
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk", Enabled: false}}
	conn.Create(&key)
	m := model.ChannelModel{ChannelID: channel.ID, Name: "m1"}
	conn.Create(&m)
	conn.Create(&model.ChannelGrant{ChannelModelID: m.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion})
	groupName, _ := AutoGroupName("diskey", "m1")
	conn.Create(&model.Group{Name: groupName})

	items, err := ChannelDiagnoseAll(context.Background(), channel.ID)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	got := items[0]
	if got.KeyCount != 0 {
		t.Fatalf("启用的凭据数应为 0，实得 %d", got.KeyCount)
	}
	if got.UsableCount != 0 {
		t.Fatalf("凭据全禁用时不该判为可用")
	}
	if !contains(got.Broken[0].Reason, "凭据") {
		t.Fatalf("原因应指向凭据，实得 %q", got.Broken[0].Reason)
	}
}

// **部分凭据禁用**时，只有启用凭据上的授权算数。
//
// 这是上一条的精确版：上一条凭据全禁用，KeyCount==0 的分支会先命中，
// 于是「禁用凭据上的授权不算数」这个守卫即使被删掉，用例也照样通过
// （变异检查当场证明了这一点）。本用例让**有启用的凭据存在**，
// 但目标模型只被禁用凭据授权 —— 此时只有真正检查了凭据状态才会判为不可用。
func TestDiagnoseGrantOnDisabledKeyIsNotCounted(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "mixed", Enabled: true}}
	conn.Create(&channel)
	// k1 启用、k2 禁用；模型只被 k2 授权。
	k1 := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "live", Key: "sk1", Enabled: true}}
	conn.Create(&k1)
	k2 := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "dead", Key: "sk2", Enabled: false}}
	conn.Create(&k2)
	// ChannelKey.Enabled 没有 default 标签，但仍统一显式落一次，避免装置与断言不一致。
	conn.Model(&model.ChannelKey{}).Where("id = ?", k2.ID).Update("enabled", false)

	m := model.ChannelModel{ChannelID: channel.ID, Name: "m1"}
	conn.Create(&m)
	conn.Create(&model.ChannelGrant{ChannelModelID: m.ID, ChannelKeyID: k2.ID, Protocols: model.ProtocolOpenAIChatCompletion})
	groupName, _ := AutoGroupName("mixed", "m1")
	conn.Create(&model.Group{Name: groupName})

	items, err := ChannelDiagnoseAll(context.Background(), channel.ID)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	got := items[0]
	if got.KeyCount != 1 {
		t.Fatalf("启用的凭据数应为 1，实得 %d", got.KeyCount)
	}
	if got.UsableCount != 0 {
		t.Fatalf("模型只被禁用凭据授权时不该判为可用（诊断在说谎）")
	}
	if len(got.Broken) != 1 || len(got.Broken[0].GrantKeys) != 0 {
		t.Fatalf("不该把禁用凭据算成有效授权，实得 %v", got.Broken)
	}
}

// 渠道停用 → 不可用，原因指向渠道本身。
func TestDiagnoseDisabledChannel(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "off", Enabled: false}}
	conn.Create(&channel)
	// Channel.Enabled 带 gorm default:true，Create 时零值被跳过、落库成 true，
	// 所以「停用」必须创建后再显式改一次——否则装置造出的是启用渠道，
	// 用例会以「原因应指向渠道停用，实得…没有对应分组」的形式误报（本用例当场踩到）。
	if err := conn.Model(&model.Channel{}).Where("id = ?", channel.ID).
		Update("enabled", false).Error; err != nil {
		t.Fatalf("disable channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk", Enabled: true}}
	conn.Create(&key)
	m := model.ChannelModel{ChannelID: channel.ID, Name: "m1"}
	conn.Create(&m)
	conn.Create(&model.ChannelGrant{ChannelModelID: m.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion})

	items, err := ChannelDiagnoseAll(context.Background(), channel.ID)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	if items[0].Enabled {
		t.Fatalf("装置未生效：渠道应处于停用状态")
	}
	if items[0].UsableCount != 0 {
		t.Fatalf("渠道停用时不该判为可用")
	}
	if !contains(items[0].Broken[0].Reason, "停用") {
		t.Fatalf("原因应指向渠道停用，实得 %q", items[0].Broken[0].Reason)
	}
}

// 有授权但分组缺失 → 不可用，原因指向分组（与「没授权」区分开）。
func TestDiagnoseGrantWithoutGroup(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "nogrp", Enabled: true}}
	conn.Create(&channel)
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk", Enabled: true}}
	conn.Create(&key)
	m := model.ChannelModel{ChannelID: channel.ID, Name: "m1"}
	conn.Create(&m)
	conn.Create(&model.ChannelGrant{ChannelModelID: m.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion})
	// 刻意不建分组

	items, err := ChannelDiagnoseAll(context.Background(), channel.ID)
	if err != nil {
		t.Fatalf("diagnose: %v", err)
	}
	got := items[0]
	if got.UsableCount != 0 {
		t.Fatalf("无分组时不该判为可用")
	}
	if len(got.Broken[0].GrantKeys) != 1 {
		t.Fatalf("应报告授权凭据，实得 %v", got.Broken[0].GrantKeys)
	}
	if !contains(got.Broken[0].Reason, "分组") {
		t.Fatalf("原因应指向分组，实得 %q", got.Broken[0].Reason)
	}
}

// 汇总：可用率与「有缺口的渠道数」都要对得上。
func TestSummarizeChannelDiagnose(t *testing.T) {
	items := []ChannelDiagnose{
		{ChannelID: 1, TotalCount: 3, UsableCount: 3},
		{ChannelID: 2, TotalCount: 2, UsableCount: 0, Broken: []ChannelModelGap{
			{ModelName: "a", Reason: "没有凭据被授权使用该模型：x"},
			{ModelName: "b", Reason: "没有凭据被授权使用该模型：y"},
		}},
	}
	got := SummarizeChannelDiagnose(items)
	if got.Channels != 2 || got.TotalModels != 5 || got.UsableModels != 3 || got.BrokenModels != 2 {
		t.Fatalf("汇总=%+v", got)
	}
	if got.ChannelsWithGap != 1 {
		t.Fatalf("有缺口的渠道数=%d, want 1", got.ChannelsWithGap)
	}
	counts := DiagnoseReasonCounts(items)
	if counts["没有凭据被授权使用该模型"] != 2 {
		t.Fatalf("原因归类=%v", counts)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
