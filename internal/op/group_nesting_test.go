package op

import (
	"fmt"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// seedNestingGroups 建两个空分组并返回主键, 供嵌套引用类测试搭链。
func seedNestingGroups(t *testing.T, conn *gorm.DB, names ...string) map[string]int {
	t.Helper()
	ids := make(map[string]int, len(names))
	for _, name := range names {
		group := model.Group{Name: name}
		if err := conn.Create(&group).Error; err != nil {
			t.Fatalf("create group %q: %v", name, err)
		}
		ids[name] = group.ID
	}
	return ids
}

// TestSyncGroupItemsNestedRef 更新成员集合时子分组引用按引用键匹配:
// 重复提交同一子分组引用保留成员主键, 混合授权与子分组的集合整体替换不串位。
func TestSyncGroupItemsNestedRef(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	ids := seedNestingGroups(t, conn, "parent", "childA", "childB")

	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["parent"], []model.GroupItemInput{
			{ChildGroupID: ids["childA"]},
			{ChildGroupID: ids["childB"]},
		})
	}); err != nil {
		t.Fatalf("sync nested refs: %v", err)
	}

	var first []model.GroupItem
	if err := conn.Where("group_id = ?", ids["parent"]).Order("priority").Find(&first).Error; err != nil {
		t.Fatalf("load items: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("items = %d, want 2", len(first))
	}
	if first[0].ChildRef() != ids["childA"] || first[1].ChildRef() != ids["childB"] {
		t.Fatalf("child refs = %d/%d, want %d/%d", first[0].ChildRef(), first[1].ChildRef(), ids["childA"], ids["childB"])
	}
	if first[0].GrantRef() != 0 {
		t.Fatalf("child item grant ref = %d, want 0", first[0].GrantRef())
	}

	// 重排后整体提交: 引用键相同, 主键必须保留。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["parent"], []model.GroupItemInput{
			{ChildGroupID: ids["childB"]},
			{ChildGroupID: ids["childA"]},
		})
	}); err != nil {
		t.Fatalf("resync reordered: %v", err)
	}
	var second []model.GroupItem
	if err := conn.Where("group_id = ?", ids["parent"]).Order("priority").Find(&second).Error; err != nil {
		t.Fatalf("load reordered items: %v", err)
	}
	if len(second) != 2 || second[0].ID != first[1].ID || second[1].ID != first[0].ID {
		t.Fatalf("reordered items lost primary keys: before %v after %v", itemIDs(first), itemIDs(second))
	}
}

// TestSyncGroupItemsPersistsSmartTier 覆盖智能路由显式档位的落库与清空（T-smart-007）：
// 新建时带上档位、重排后仍带上、显式清空（提交空串）要真的写回空值。
func TestSyncGroupItemsPersistsSmartTier(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	ids := seedNestingGroups(t, conn, "tier-parent", "tier-child")

	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["tier-parent"], []model.GroupItemInput{
			{ChildGroupID: ids["tier-child"], SmartTier: model.GroupSmartTierDecision},
		})
	}); err != nil {
		t.Fatalf("sync with tier: %v", err)
	}
	var first []model.GroupItem
	if err := conn.Where("group_id = ?", ids["tier-parent"]).Find(&first).Error; err != nil {
		t.Fatalf("load items: %v", err)
	}
	if len(first) != 1 || first[0].SmartTier != model.GroupSmartTierDecision {
		t.Fatalf("落库档位 = %q, 期望 %q", first[0].SmartTier, model.GroupSmartTierDecision)
	}

	// 同一引用改成执行档：走的是「更新既有行」的分支，档位必须随之改写。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["tier-parent"], []model.GroupItemInput{
			{ChildGroupID: ids["tier-child"], SmartTier: model.GroupSmartTierExecution},
		})
	}); err != nil {
		t.Fatalf("update tier: %v", err)
	}
	var second []model.GroupItem
	if err := conn.Where("group_id = ?", ids["tier-parent"]).Find(&second).Error; err != nil {
		t.Fatalf("reload items: %v", err)
	}
	if second[0].ID != first[0].ID {
		t.Fatalf("成员主键变了: %d -> %d", first[0].ID, second[0].ID)
	}
	if second[0].SmartTier != model.GroupSmartTierExecution {
		t.Fatalf("更新后的档位 = %q, 期望 %q", second[0].SmartTier, model.GroupSmartTierExecution)
	}

	// 提交空串 = 改回「按顺序自动切分」：零值也必须落库，不能被 GORM 的零值忽略挡下。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["tier-parent"], []model.GroupItemInput{
			{ChildGroupID: ids["tier-child"]},
		})
	}); err != nil {
		t.Fatalf("clear tier: %v", err)
	}
	var third []model.GroupItem
	if err := conn.Where("group_id = ?", ids["tier-parent"]).Find(&third).Error; err != nil {
		t.Fatalf("reload items: %v", err)
	}
	if third[0].SmartTier != model.GroupSmartTierAuto {
		t.Fatalf("清空后的档位 = %q, 期望空串", third[0].SmartTier)
	}
}

// TestSyncGroupItemsMixedRefs 授权与子分组混合集合: 互斥形状逐条校验, 全非法输入被拒。
func TestSyncGroupItemsMixedRefs(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	_, modelIDs := seedAutoGroupChannel(t, conn, "mixed")
	grantID := grantIDOfModel(t, conn, modelIDs["m1"])
	ids := seedNestingGroups(t, conn, "mixed-parent", "mixed-child")

	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["mixed-parent"], []model.GroupItemInput{
			{ChannelGrantID: grantID},
			{ChildGroupID: ids["mixed-child"]},
		})
	}); err != nil {
		t.Fatalf("sync mixed refs: %v", err)
	}
	var count int64
	conn.Model(&model.GroupItem{}).Where("group_id = ?", ids["mixed-parent"]).Count(&count)
	if count != 2 {
		t.Fatalf("items = %d, want 2 (1 grant + 1 child)", count)
	}

	// 同一条成员同时给两个引用: 互斥拒绝。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["mixed-parent"], []model.GroupItemInput{
			{ChannelGrantID: grantID, ChildGroupID: ids["mixed-child"]},
		})
	}); err == nil {
		t.Fatal("both refs accepted, want mutual-exclusion error")
	}

	// 两个引用全空: 拒绝。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["mixed-parent"], []model.GroupItemInput{{}})
	}); err == nil {
		t.Fatal("empty refs accepted, want error")
	}

	// 子分组不存在: 拒绝。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["mixed-parent"], []model.GroupItemInput{{ChildGroupID: 99999}})
	}); err == nil {
		t.Fatal("missing child group accepted, want error")
	}
}

// TestSyncGroupItemsCycleGuard 自引用与两节点成环均被拒, 合法无环链放行。
func TestSyncGroupItemsCycleGuard(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	ids := seedNestingGroups(t, conn, "root", "mid", "leaf")

	// 自引用: 分组成员指向自己。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["root"], []model.GroupItemInput{{ChildGroupID: ids["root"]}})
	}); err == nil {
		t.Fatal("self reference accepted, want cycle error")
	}

	// 两节点环: mid → leaf 建立后, root → mid 且 mid → root 成环, 后一步必须被拒。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["mid"], []model.GroupItemInput{{ChildGroupID: ids["leaf"]}})
	}); err != nil {
		t.Fatalf("seed mid->leaf: %v", err)
	}
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["leaf"], []model.GroupItemInput{{ChildGroupID: ids["mid"]}})
	}); err == nil {
		t.Fatal("two-node cycle accepted, want cycle error")
	}

	// 深层无环链放行: root → mid 已可, 再补 root → leaf 依旧无环。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["root"], []model.GroupItemInput{
			{ChildGroupID: ids["mid"]},
			{ChildGroupID: ids["leaf"]},
		})
	}); err != nil {
		t.Fatalf("acyclic chain rejected: %v", err)
	}
}

// TestSyncGroupItemsDepthCap 嵌套链超过 GroupItemMaxDepth 时拒绝写入。
func TestSyncGroupItemsDepthCap(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	names := make([]string, 0, model.GroupItemMaxDepth+1)
	for i := 0; i <= model.GroupItemMaxDepth; i++ {
		names = append(names, fmt.Sprintf("depth-%02d", i))
	}
	ids := seedNestingGroups(t, conn, names...)

	// 逐级建链 depth-00 → depth-01 → ... → depth-08: 第 depth-08 行落在深度上限内应放行,
	// 再让 depth-08 指向已深达上限的链顶端 depth-00, 使其链深超限而拒绝。
	for i := 1; i <= model.GroupItemMaxDepth; i++ {
		parent := names[i-1]
		child := names[i]
		if err := conn.Transaction(func(tx *gorm.DB) error {
			return syncGroupItems(tx, ids[parent], []model.GroupItemInput{{ChildGroupID: ids[child]}})
		}); err != nil {
			t.Fatalf("chain %s->%s: %v", parent, child, err)
		}
	}
	// depth-08 → depth-00: depth-00 经 depth-01...回到 depth-08 共 MaxDepth 层, 会超限, 拒绝。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids[names[model.GroupItemMaxDepth]], []model.GroupItemInput{{ChildGroupID: ids[names[0]]}})
	}); err == nil {
		t.Fatalf("chain deeper than max depth %d accepted", model.GroupItemMaxDepth)
	}
}

// TestValidateGroupTreeRefsOnCreate 新建分组时子分组引用的存在性校验同样生效。
func TestValidateGroupTreeRefsOnCreate(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	ids := seedNestingGroups(t, conn, "create-child")

	items := []model.GroupItem{mustGrantItem(t, ids["create-child"])}
	if err := validateGroupTreeRefs(conn, items); err != nil {
		t.Fatalf("existing child ref rejected on create: %v", err)
	}

	missing := []model.GroupItem{mustGrantItem(t, 424242)}
	if err := validateGroupTreeRefs(conn, missing); err == nil {
		t.Fatal("missing child ref accepted on create, want error")
	}
}

// TestGroupDelCascadesChildRefs 删除被引用的子分组时, 父分组指向它的成员一并删除且当前成员清零。
func TestGroupDelCascadesChildRefs(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	ids := seedNestingGroups(t, conn, "del-parent", "del-child")
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return syncGroupItems(tx, ids["del-parent"], []model.GroupItemInput{{ChildGroupID: ids["del-child"]}})
	}); err != nil {
		t.Fatalf("seed child ref: %v", err)
	}
	var refItem model.GroupItem
	if err := conn.Where("group_id = ?", ids["del-parent"]).First(&refItem).Error; err != nil {
		t.Fatalf("load child ref item: %v", err)
	}
	if err := conn.Model(&model.Group{}).Where("id = ?", ids["del-parent"]).
		Update("active_item_id", refItem.ID).Error; err != nil {
		t.Fatalf("set active item: %v", err)
	}

	// 级联清理属 GroupDel 的事务本体 (groupDelOn), 直删库行不经过它不会级联:
	// 测试走真实生产路径, 缓存处置留生产 wrapper 不在此验证。
	if err := groupDelOn(conn, ids["del-child"]); err != nil {
		t.Fatalf("delete child group: %v", err)
	}
	var parents int64
	conn.Model(&model.GroupItem{}).Where("child_group_id = ?", ids["del-child"]).Count(&parents)
	if parents != 0 {
		t.Fatalf("child references survived group deletion: %d rows", parents)
	}
	var active int
	if err := conn.Model(&model.Group{}).Where("id = ?", ids["del-parent"]).
		Select("active_item_id").Row().Scan(&active); err != nil {
		t.Fatalf("load active item: %v", err)
	}
	if active != 0 {
		t.Fatalf("active_item_id = %d, want 0 after cascade cleanup", active)
	}
}

// grantIDOfModel 取某渠道模型的唯一授权主键, 供测试按模型定位授权。
func grantIDOfModel(t *testing.T, conn *gorm.DB, modelID int) int {
	t.Helper()
	var grant model.ChannelGrant
	if err := conn.Where("channel_model_id = ?", modelID).First(&grant).Error; err != nil {
		t.Fatalf("load grant of model %d: %v", modelID, err)
	}
	return grant.ID
}

// mustGrantItem 构造一条授权成员行 (非空引用), 与子分组成员行互补测试互斥形状。
func mustGrantItem(t *testing.T, childGroupID int) model.GroupItem {
	t.Helper()
	if err := model.ValidateGroupItemRef(0, childGroupID); err != nil {
		t.Fatalf("build child item: %v", err)
	}
	return model.GroupItem{ChildGroupID: &childGroupID}
}

// itemIDs 提取成员主键列表, 供断言输出。
func itemIDs(items []model.GroupItem) []int {
	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}
