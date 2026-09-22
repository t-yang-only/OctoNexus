package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-usability-005 清理「已失效的自动分组」的判据。
//
// ## 这个功能的两种失败方式，危险程度完全不同
//
//   - **漏删**：残留分组继续存在，客户端还能选中、调用时报错（或等待超时）。
//     用户会困惑，但数据没坏，下次保存渠道时还有机会清掉。
//
//   - **误删**：把用户手工编排的分组删了。那是用户的资产，删掉找不回来
//     （分组里有成员顺序、档位标注、relay 配置等人工决策）。
//
// 所以下面「不能误删」的用例比「要能删掉」的用例更重要 ——
// 前者错了是不可逆的破坏，后者错了只是没清理干净。

// 造一个渠道 + 凭据 + 模型 + 授权的最小组合。
func seedChannelWithGrant(t *testing.T, conn *gorm.DB, channelName, modelName string) (channelID, grantID int) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: channelName, Enabled: true}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk", Enabled: true}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	m := model.ChannelModel{ChannelID: channel.ID, Name: modelName}
	if err := conn.Create(&m).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	grant := model.ChannelGrant{ChannelModelID: m.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := conn.Create(&grant).Error; err != nil {
		t.Fatalf("create grant: %v", err)
	}
	return channel.ID, grant.ID
}

func groupExists(conn *gorm.DB, name string) bool {
	var n int64
	conn.Model(&model.Group{}).Where("name = ?", name).Count(&n)
	return n > 0
}

// 核心场景：授权被删掉后，对应的自动分组应当被清理。
func TestPruneRemovesGroupWhenGrantGone(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channelID, grantID := seedChannelWithGrant(t, conn, "ch", "m1")

	name := "ch/m1"
	if err := conn.Create(&model.Group{Name: name}).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	var group model.Group
	conn.Where("name = ?", name).First(&group)
	ref := grantID
	if err := conn.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &ref, Priority: 1}).Error; err != nil {
		t.Fatalf("create item: %v", err)
	}

	// 模拟「撤销授权」：删掉授权行，分组与成员都还在。
	if err := conn.Where("id = ?", grantID).Delete(&model.ChannelGrant{}).Error; err != nil {
		t.Fatalf("delete grant: %v", err)
	}

	if err := pruneStaleAutoGroups(conn, channelID, "ch"); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if groupExists(conn, name) {
		t.Fatalf("授权已删除，分组 %q 应当被清理（否则客户端会看到一条已死的记录）", name)
	}
}

// 仍有有效授权的分组绝不能被删 —— 这是最基本的不能误删。
func TestPruneKeepsGroupWithValidGrant(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channelID, grantID := seedChannelWithGrant(t, conn, "ch", "m1")

	name := "ch/m1"
	conn.Create(&model.Group{Name: name})
	var group model.Group
	conn.Where("name = ?", name).First(&group)
	ref := grantID
	conn.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &ref, Priority: 1})

	if err := pruneStaleAutoGroups(conn, channelID, "ch"); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !groupExists(conn, name) {
		t.Fatalf("授权仍然有效，分组 %q 被误删了", name)
	}
}

// 含**子分组成员**的分组不能删：那说明用户手工编排过整条链。
func TestPruneKeepsGroupWithChildGroupItem(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channelID, grantID := seedChannelWithGrant(t, conn, "ch", "m1")

	// 另一个分组作为子分组被引用。
	child := model.Group{Name: "手工编排的子分组"}
	conn.Create(&child)

	name := "ch/m1"
	conn.Create(&model.Group{Name: name})
	var group model.Group
	conn.Where("name = ?", name).First(&group)

	// 一个已失效的授权成员 + 一个子分组成员。
	staleRef := grantID
	conn.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &staleRef, Priority: 1})
	childRef := child.ID
	conn.Create(&model.GroupItem{GroupID: group.ID, ChildGroupID: &childRef, Priority: 2})
	conn.Where("id = ?", grantID).Delete(&model.ChannelGrant{})

	if err := pruneStaleAutoGroups(conn, channelID, "ch"); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !groupExists(conn, name) {
		t.Fatalf("分组含子分组成员（用户手工编排的证据），不该被删")
	}
	// 而且失效成员也要保留：整组跳过是刻意的，避免在用户编排过的分组里做局部手术。
	var n int64
	conn.Model(&model.GroupItem{}).Where("group_id = ?", group.ID).Count(&n)
	if n != 2 {
		t.Fatalf("含子分组的分组应整体跳过，成员数应保持 2，实得 %d", n)
	}
}

// 含**别的渠道授权**的分组不能删：同样是用户手工混用的证据。
//
// **这个用例也是变异检查逼出来的**：第一版里分组只有一个「别的渠道的有效授权」成员，
// 去掉这条守卫后它照样不会被删 —— 因为删分组的条件是"所有成员都失效"，
// 而那个成员是有效的。守卫等于没测（变异后测试依然绿）。
//
// 现在让分组里**同时**有：
//
//	· 一个引用别的渠道**有效**授权的成员（触发守卫）
//	· 一个**失效**成员（如果守卫不生效，它会被删掉）
//
// 于是守卫的作用才可观测：有守卫 → 整组跳过、失效成员保留；
// 没守卫 → 失效成员被删。
func TestPruneKeepsGroupMixingOtherChannelGrant(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channelID, ownGrantID := seedChannelWithGrant(t, conn, "ch", "m1")
	// 另一个渠道的有效授权，被混进本渠道的分组里。
	_, otherGrantID := seedChannelWithGrant(t, conn, "other", "m9")

	name := "ch/m1"
	conn.Create(&model.Group{Name: name})
	var group model.Group
	conn.Where("name = ?", name).First(&group)

	// 成员1：别的渠道的**有效**授权 —— 用户手工混用的证据。
	foreignRef := otherGrantID
	conn.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &foreignRef, Priority: 1})
	// 成员2：本渠道的授权，稍后删掉 → 失效成员。
	staleRef := ownGrantID
	conn.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &staleRef, Priority: 2})
	conn.Where("id = ?", ownGrantID).Delete(&model.ChannelGrant{})

	if err := pruneStaleAutoGroups(conn, channelID, "ch"); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !groupExists(conn, name) {
		t.Fatalf("分组引用了别的渠道的授权（用户手工混用），不该被删")
	}
	// 关键断言：混用的分组要**整组跳过**，不能做局部手术。
	// 没有这条断言时，去掉「引用别的渠道就跳过」的守卫测试照样绿。
	var n int64
	conn.Model(&model.GroupItem{}).Where("group_id = ?", group.ID).Count(&n)
	if n != 2 {
		t.Fatalf("含别的渠道授权的分组应整组跳过（成员数保持 2），实得 %d —— "+
			"说明守卫没生效，在对用户手工混用的分组做局部删除", n)
	}
}

// 别的渠道的同名分组不能被牵连：前缀必须精确。
//
// **这个用例是变异检查逼出来的**：第一版里 "ch-other/m1" 的成员指向
// ch-other 渠道自己的有效授权，于是「引用别的渠道授权就跳过」那条守卫
// 会兜住它 —— 把前缀匹配改成不精确（`ch` 而不是 `ch/`）时测试照样绿，
// 前缀守卫等于没测。
//
// 现在让 "ch-other/m1" 的成员指向**已删除**的授权：
//
//	· 前缀精确 → 它不在候选集合里，不会被碰 ✓
//	· 前缀不精确 → 它进了候选，成员授权不存在 → 被当成残留清理掉 ✗
//
// 这样"别的渠道"守卫不会兜住，判据才真正压在前缀上。
func TestPruneDoesNotTouchOtherChannelGroups(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channelID, _ := seedChannelWithGrant(t, conn, "ch", "m1")
	_, otherGrantID := seedChannelWithGrant(t, conn, "ch-other", "m1")

	// "ch-other/m1" 以 "ch" 开头，但不是 "ch/" 前缀下的分组。
	name := "ch-other/m1"
	conn.Create(&model.Group{Name: name})
	var group model.Group
	conn.Where("name = ?", name).First(&group)
	ref := otherGrantID
	conn.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &ref, Priority: 1})

	// 关键：把 ch-other 的授权删掉，让这个成员也处于"失效"状态。
	// 否则「别的渠道授权」守卫会兜住它，前缀守卫就测不到。
	conn.Where("id = ?", otherGrantID).Delete(&model.ChannelGrant{})

	if err := pruneStaleAutoGroups(conn, channelID, "ch"); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !groupExists(conn, name) {
		t.Fatalf("清理渠道 ch 时误伤了 ch-other 的分组（前缀匹配不精确：用了 %q 而不是 %q）",
			"ch", "ch/")
	}
}

// 部分成员失效时：只删失效的成员，保留分组（因为还有有效成员）。
func TestPruneRemovesOnlyStaleItemsWhenOthersRemain(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channelID, keepGrantID := seedChannelWithGrant(t, conn, "ch", "m1")
	// 同一渠道的第二个模型 + 授权，稍后删掉它的授权。
	key := model.ChannelKey{ChannelID: channelID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k2", Key: "sk2", Enabled: true}}
	conn.Create(&key)
	m2 := model.ChannelModel{ChannelID: channelID, Name: "m2"}
	conn.Create(&m2)
	stale := model.ChannelGrant{ChannelModelID: m2.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	conn.Create(&stale)

	name := "ch/m1"
	conn.Create(&model.Group{Name: name})
	var group model.Group
	conn.Where("name = ?", name).First(&group)
	keepRef, staleRef := keepGrantID, stale.ID
	conn.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &keepRef, Priority: 1})
	conn.Create(&model.GroupItem{GroupID: group.ID, ChannelGrantID: &staleRef, Priority: 2})
	conn.Where("id = ?", stale.ID).Delete(&model.ChannelGrant{})

	if err := pruneStaleAutoGroups(conn, channelID, "ch"); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if !groupExists(conn, name) {
		t.Fatalf("还有有效成员，分组不该被删")
	}
	var n int64
	conn.Model(&model.GroupItem{}).Where("group_id = ?", group.ID).Count(&n)
	if n != 1 {
		t.Fatalf("失效成员应被删掉、有效成员保留，成员数应为 1，实得 %d", n)
	}
}
