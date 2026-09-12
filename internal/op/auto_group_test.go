package op

import (
	"sort"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openAutoGroupTestDB 打开一块内存 SQLite, 建出自动分组依赖的全部表。
// 用 glebarez/sqlite 纯 Go 驱动: 与业务 db.go 同驱动, 无需 cgo, 测试在任何机器都能跑。
// 每个测试独享一份内存库 (t.Name() 作区分): 并行或顺序跑都不会撞 channels.name 唯一键。
func openAutoGroupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Discard,
	})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
		&model.Group{},
		&model.GroupItem{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn
}

// seedAutoGroupChannel 造一个最小可用渠道: 1 凭据 × 2 模型, 全组合授权。
// 返回渠道主键、凭据主键与模型名->模型主键的映射, 供断言成员归属。
func seedAutoGroupChannel(t *testing.T, conn *gorm.DB, channelName string) (int, map[string]int) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: channelName}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	key := model.ChannelKey{ChannelID: channel.ID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k1", Key: "sk-test"}}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("create key: %v", err)
	}
	modelIDs := map[string]int{}
	for _, name := range []string{"m1", "m2"} {
		m := model.ChannelModel{ChannelID: channel.ID, Name: name}
		if err := conn.Create(&m).Error; err != nil {
			t.Fatalf("create model %q: %v", name, err)
		}
		modelIDs[name] = m.ID
		grant := model.ChannelGrant{ChannelModelID: m.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion}
		if err := conn.Create(&grant).Error; err != nil {
			t.Fatalf("create grant %q: %v", name, err)
		}
	}
	return channel.ID, modelIDs
}

func groupNamesOf(t *testing.T, conn *gorm.DB) []string {
	t.Helper()
	var groups []model.Group
	if err := conn.Find(&groups).Error; err != nil {
		t.Fatalf("list groups: %v", err)
	}
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		names = append(names, g.Name)
	}
	sort.Strings(names)
	return names
}

func groupItemsGrantIDs(t *testing.T, conn *gorm.DB, groupName string) []int {
	t.Helper()
	var group model.Group
	if err := conn.Where("name = ?", groupName).First(&group).Error; err != nil {
		t.Fatalf("load group %q: %v", groupName, err)
	}
	var items []model.GroupItem
	if err := conn.Where("group_id = ?", group.ID).Order("priority ASC").Find(&items).Error; err != nil {
		t.Fatalf("load items %q: %v", groupName, err)
	}
	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ChannelGrantID)
	}
	return ids
}

func TestAutoGroupName(t *testing.T) {
	got, err := AutoGroupName(" ch1 ", " GPT-4o ")
	if err != nil {
		t.Fatalf("AutoGroupName: %v", err)
	}
	// 渠道名去空白, 模型名保留原大小写: 分组名是客户端模型标识, 大小写属协议语义。
	if got != "ch1/GPT-4o" {
		t.Fatalf("AutoGroupName = %q, want %q", got, "ch1/GPT-4o")
	}
	for _, tc := range []struct {
		channel, model string
	}{
		{"", "m1"},
		{"  ", "m1"},
		{"ch1", ""},
		{"ch1", "  "},
	} {
		if _, err := AutoGroupName(tc.channel, tc.model); err == nil {
			t.Fatalf("AutoGroupName(%q, %q) = nil error, want error", tc.channel, tc.model)
		}
	}
}

func TestAutoGroupConfig(t *testing.T) {
	mode, relay := autoGroupConfig()
	if mode != model.GroupModeFailover {
		t.Fatalf("auto group mode = %q, want failover", mode)
	}
	if relay != model.DefaultGroupRelayConfig() {
		t.Fatalf("auto group relay config = %+v, want defaults", relay)
	}
}

// TestEnsureAutoGroupsLocked_CreateAndIdempotent 覆盖主路径:
// 首次调用建出 渠道名/模型名 分组并挂上成员, 重复调用不产生重复分组与成员。
func TestEnsureAutoGroupsLocked_CreateAndIdempotent(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channelID, modelIDs := seedAutoGroupChannel(t, conn, "ch1")

	if err := conn.Transaction(func(tx *gorm.DB) error {
		return ensureAutoGroupsLocked(tx, channelID, "ch1", []string{"m1", " m2 ", "m1", "", "ghost"})
	}); err != nil {
		t.Fatalf("ensureAutoGroupsLocked: %v", err)
	}

	// ghost 不在渠道模型里, 应被跳过; 去重与空白后只剩 m1, m2。
	if names := groupNamesOf(t, conn); len(names) != 2 || names[0] != "ch1/m1" || names[1] != "ch1/m2" {
		t.Fatalf("groups = %v, want [ch1/m1 ch1/m2]", names)
	}
	for _, m := range []string{"m1", "m2"} {
		ids := groupItemsGrantIDs(t, conn, "ch1/"+m)
		if len(ids) != 1 {
			t.Fatalf("group ch1/%s items = %v, want exactly 1 grant", m, ids)
		}
		var grant model.ChannelGrant
		if err := conn.First(&grant, ids[0]).Error; err != nil {
			t.Fatalf("load grant: %v", err)
		}
		if grant.ChannelModelID != modelIDs[m] {
			t.Fatalf("group ch1/%s points to model %d, want %d", m, grant.ChannelModelID, modelIDs[m])
		}
	}

	var before []model.GroupItem
	if err := conn.Find(&before).Error; err != nil {
		t.Fatalf("list items: %v", err)
	}
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return ensureAutoGroupsLocked(tx, channelID, "ch1", []string{"m1", "m2"})
	}); err != nil {
		t.Fatalf("second ensureAutoGroupsLocked: %v", err)
	}
	var after []model.GroupItem
	if err := conn.Find(&after).Error; err != nil {
		t.Fatalf("list items: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("idempotency broken: items %d -> %d", len(before), len(after))
	}
	if names := groupNamesOf(t, conn); len(names) != 2 {
		t.Fatalf("idempotency broken: groups = %v", names)
	}
}

// TestEnsureAutoGroupsLocked_PreservesExisting 只补缺失成员:
// 已存在的分组不改模式与 Relay 参数, 不删已有成员, 新增授权按优先级追加。
func TestEnsureAutoGroupsLocked_PreservesExisting(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channelID, modelIDs := seedAutoGroupChannel(t, conn, "ch1")

	// 预置一个手动模式的 ch1/m1 分组, 观察调用后是否被改写。
	pre := model.Group{Name: "ch1/m1", Mode: model.GroupModeManual, RelayConfig: model.GroupRelayConfig{}}
	if err := conn.Create(&pre).Error; err != nil {
		t.Fatalf("create pre group: %v", err)
	}
	_ = modelIDs

	var firstGrant model.ChannelGrant
	if err := conn.Order("id ASC").First(&firstGrant).Error; err != nil {
		t.Fatalf("load first grant: %v", err)
	}
	if err := conn.Create(&model.GroupItem{GroupID: pre.ID, ChannelGrantID: firstGrant.ID, Priority: 1}).Error; err != nil {
		t.Fatalf("create pre item: %v", err)
	}

	// 给 m2 再加一个凭据授权, 调用后 ch1/m2 应新建分组并同时挂上两个授权。
	key2 := model.ChannelKey{ChannelID: channelID, ChannelKeyConfig: model.ChannelKeyConfig{Name: "k2", Key: "sk-test-2"}}
	if err := conn.Create(&key2).Error; err != nil {
		t.Fatalf("create key2: %v", err)
	}
	var m2 model.ChannelModel
	if err := conn.Where("channel_id = ? AND name = ?", channelID, "m2").First(&m2).Error; err != nil {
		t.Fatalf("load m2: %v", err)
	}
	extra := model.ChannelGrant{ChannelModelID: m2.ID, ChannelKeyID: key2.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := conn.Create(&extra).Error; err != nil {
		t.Fatalf("create extra grant: %v", err)
	}

	if err := conn.Transaction(func(tx *gorm.DB) error {
		return ensureAutoGroupsLocked(tx, channelID, "ch1", []string{"m1", "m2"})
	}); err != nil {
		t.Fatalf("ensureAutoGroupsLocked: %v", err)
	}

	var kept model.Group
	if err := conn.Where("name = ?", "ch1/m1").First(&kept).Error; err != nil {
		t.Fatalf("load kept group: %v", err)
	}
	if kept.Mode != model.GroupModeManual {
		t.Fatalf("existing group mode = %q, want manual (unchanged)", kept.Mode)
	}
	if ids := groupItemsGrantIDs(t, conn, "ch1/m1"); len(ids) != 1 || ids[0] != firstGrant.ID {
		t.Fatalf("existing group items = %v, want [%d]", ids, firstGrant.ID)
	}
	ids := groupItemsGrantIDs(t, conn, "ch1/m2")
	if len(ids) != 2 {
		t.Fatalf("new group items = %v, want 2 grants", ids)
	}
}

// TestEnsureAutoGroupsLocked_NoGrantsOrEmpty 无授权与空输入直接返回 nil, 不建分组。
func TestEnsureAutoGroupsLocked_NoGrantsOrEmpty(t *testing.T) {
	conn := openAutoGroupTestDB(t)
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: "lonely"}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	// 渠道下无任何模型行: 即使传了模型名也应因查不到授权而跳过。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return ensureAutoGroupsLocked(tx, channel.ID, "lonely", []string{"m1"})
	}); err != nil {
		t.Fatalf("ensureAutoGroupsLocked: %v", err)
	}
	if names := groupNamesOf(t, conn); len(names) != 0 {
		t.Fatalf("groups = %v, want none", names)
	}
	// 空输入与空白渠道名: 前者静默返回, 后者报错。
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return ensureAutoGroupsLocked(tx, channel.ID, "lonely", nil)
	}); err != nil {
		t.Fatalf("empty models: %v", err)
	}
	if err := conn.Transaction(func(tx *gorm.DB) error {
		return ensureAutoGroupsLocked(tx, channel.ID, "  ", []string{"m1"})
	}); err == nil {
		t.Fatal("blank channel name = nil error, want error")
	}
}
