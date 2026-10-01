package op

import (
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 需求8「账号页面加入一个删除功能」的判据。
//
// # 这个功能真正要防住的事
//
// 账号本身不参与转发，号池把它映射成渠道凭据之后才进选路。所以"删账号"必须**连凭据
// 一起删**：只删账号行会留下一条指向失效 token 的凭据，它照样被选路选中，于是每次请求
// 以 401 失败并触发冷却，而面板上看不出这条凭据是哪来的 —— 故障被留给下一次请求，
// 且看起来跟"渠道坏了"一模一样。
//
// # 必须分开验证的四件事
//
//  1. 账号行真的没了（最基本，也最容易被"返回 200 但没删"蒙混）；
//  2. 号池凭据、以及引用它的授权一起没了（级联清理）；
//  3. 分组里指向这些成员的 active_item_id 被清成 0（否则分组指向一个空 id）；
//  4. **别的渠道里的同名凭据不受影响** —— 匹配按"号池渠道 + ExternalName"双条件，
//     少一个条件就会误删操作者手工建的同名凭据。

var officialDeleteTestDBSeq int64

// openOfficialDeleteTestDB 建一张带齐关联表的库：删账号会级联到渠道/凭据/授权/分组，
// 缺任何一张表都验证不到对应的清理动作。
//
// 用 db.SetDBForTest 把这张库设成全局库：删除收尾要重建分组缓存，而 groupRefreshCache
// 读的是全局库 —— 不设的话公开路径会对着一个空库重建（实测直接 panic），
// 于是"公开路径能跑通"这件事反而没被验证到。
func openOfficialDeleteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&officialDeleteTestDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.OfficialAccount{},
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{},
		&model.ChannelGrant{},
		&model.Group{}, &model.GroupItem{},
	); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))
	return conn
}

// seedPoolCredential 造一个"号池已同步"的最小现场：号池渠道 + 以账号标识命名的凭据
// + 一条引用该凭据的授权 + 一个指向该授权的分组 active_item。
func seedPoolCredential(t *testing.T, conn *gorm.DB, account model.OfficialAccount) (channelID, keyID, grantID int) {
	t.Helper()
	channel := model.Channel{ChannelConfig: model.ChannelConfig{Name: OfficialPoolChannelName(account.Provider), Enabled: true}}
	if err := conn.Create(&channel).Error; err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	key := model.ChannelKey{
		ChannelKeyConfig: model.ChannelKeyConfig{Name: account.ExternalName, Key: "sealed:test", Enabled: true},
		ChannelID:        channel.ID,
	}
	if err := conn.Create(&key).Error; err != nil {
		t.Fatalf("seed channel key: %v", err)
	}
	channelModel := model.ChannelModel{ChannelID: channel.ID, Name: "gpt-test"}
	if err := conn.Create(&channelModel).Error; err != nil {
		t.Fatalf("seed channel model: %v", err)
	}
	grant := model.ChannelGrant{ChannelModelID: channelModel.ID, ChannelKeyID: key.ID, Protocols: model.ProtocolOpenAIChatCompletion}
	if err := conn.Create(&grant).Error; err != nil {
		t.Fatalf("seed grant: %v", err)
	}
	group := model.Group{Name: "delete-target-group", Mode: model.GroupModeFailover, ActiveItemID: 0}
	if err := conn.Create(&group).Error; err != nil {
		t.Fatalf("seed group: %v", err)
	}
	item := model.GroupItem{GroupID: group.ID, ChannelGrantID: &grant.ID, Priority: 1}
	if err := conn.Create(&item).Error; err != nil {
		t.Fatalf("seed group item: %v", err)
	}
	if err := conn.Model(&model.Group{}).Where("id = ?", group.ID).
		Update("active_item_id", item.ID).Error; err != nil {
		t.Fatalf("seed active item: %v", err)
	}
	return channel.ID, key.ID, grant.ID
}

func countRows(t *testing.T, conn *gorm.DB, out any, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := conn.Model(out).Where(query, args...).Count(&n).Error; err != nil {
		t.Fatalf("count %T: %v", out, err)
	}
	return n
}

// 删账号要连号池凭据、引用它的授权、以及分组的 active_item 引用一起清掉。
// 少任何一级，都会留下"看起来是渠道坏了"的故障。
func TestOfficialAccountDeleteClearsPoolCredentialAndRefs(t *testing.T) {
	conn := openOfficialDeleteTestDB(t)
	account := model.OfficialAccount{
		Provider: model.OfficialAccountProviderOpenAI, ExternalName: "me@example.com",
		Status: model.OfficialAccountStatusActive, AccessCipher: "enc:v1:test",
	}
	if err := conn.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	channelID, keyID, grantID := seedPoolCredential(t, conn, account)

	if err := OfficialAccountDelete(account.ID); err != nil {
		t.Fatalf("OfficialAccountDelete: %v", err)
	}

	if got := countRows(t, conn, &model.OfficialAccount{}, "id = ?", account.ID); got != 0 {
		t.Errorf("账号行应已删除，实得 %d 行", got)
	}
	if got := countRows(t, conn, &model.ChannelKey{}, "id = ?", keyID); got != 0 {
		t.Errorf("号池凭据应一并删除，实得 %d 行 —— 只删账号会留下指向失效 token 的死凭据", got)
	}
	if got := countRows(t, conn, &model.ChannelGrant{}, "id = ?", grantID); got != 0 {
		t.Errorf("引用该凭据的授权应一并删除，实得 %d 行", got)
	}

	// active_item_id 必须被清成 0：否则分组指向一个已不存在的成员 id。
	var group model.Group
	if err := conn.Where("name = ?", "delete-target-group").First(&group).Error; err != nil {
		t.Fatalf("load group: %v", err)
	}
	if group.ActiveItemID != 0 {
		t.Errorf("分组的 active_item_id 应被清成 0，实得 %d（指向已删除的成员）", group.ActiveItemID)
	}

	// 渠道本身是操作者可能改过地址的共享对象，删账号不该把它一起删掉。
	if got := countRows(t, conn, &model.Channel{}, "id = ?", channelID); got != 1 {
		t.Errorf("号池渠道应保留（地址/路径可能被操作者改过），实得 %d 行", got)
	}
}

// 从没同步过号池时删除也必须成功：那是正常情形，不该因为"找不到凭据"就拒绝删除。
func TestOfficialAccountDeleteWithoutPoolSync(t *testing.T) {
	conn := openOfficialDeleteTestDB(t)
	account := model.OfficialAccount{
		Provider: model.OfficialAccountProviderGemini, ExternalName: "no-pool@example.com",
		Status: model.OfficialAccountStatusPending, AccessCipher: "enc:v1:test",
	}
	if err := conn.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}

	if err := OfficialAccountDelete(account.ID); err != nil {
		t.Fatalf("未同步号池的账号删除应成功: %v", err)
	}
	if got := countRows(t, conn, &model.OfficialAccount{}, "id = ?", account.ID); got != 0 {
		t.Errorf("账号行应已删除，实得 %d 行", got)
	}
}

// 删不存在的账号要回哨兵错误：界面据此显示"已经没了"，而不是让运维以为服务故障。
// 边界 id（0 / 负数 / 超大值）必须走 not-found 而不是 panic 或误删。
//
// 为什么值得单独测：handler 用 strconv.Atoi 解析路径参数，而 Atoi 接受负数与 0。
// 这些值一路传到 gorm 的 First(&account, id)，行为取决于主键类型与驱动的隐式转换 ——
// 不测就等于把"某个 id 形状会不会删掉别的东西"留给运气。
// 尤其负数：某些驱动会把它当偏移或静默取整，那是静默数据损坏。
func TestOfficialAccountDeleteRejectsBoundaryIDs(t *testing.T) {
	cases := []struct {
		name string
		id   int
	}{
		{"零", 0},
		{"负一", -1},
		{"负极大", -2147483648},
		{"超大正数", 2147483647},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := openOfficialDeleteTestDB(t)
			// 先造一条真实账号：确认边界 id 不会把它删掉。
			account := model.OfficialAccount{Provider: "boundary-provider", ExternalName: "boundary-name"}
			if err := conn.Create(&account).Error; err != nil {
				t.Fatalf("seed account: %v", err)
			}

			err := OfficialAccountDelete(tc.id)
			if !errors.Is(err, ErrOfficialAccountNotFound) {
				t.Fatalf("边界 id %d 应返回 not found，实得 %v", tc.id, err)
			}
			// 真实账号必须还在：边界 id 绝不能误删数据。
			if got := countRows(t, conn, &[]model.OfficialAccount{}, "1 = 1"); got != 1 {
				t.Errorf("边界 id 删除后账号应仍为 1 条，实得 %d", got)
			}
		})
	}
}

func TestOfficialAccountDeleteUnknownID(t *testing.T) {
	// 库里一个账号都没有：删不存在的 id 必须是"找不到"，不能是别的错误
	// （空库不是故障，界面该显示"已经没了"）。
	_ = openOfficialDeleteTestDB(t)
	err := OfficialAccountDelete(4242)
	if err == nil {
		t.Fatal("删不存在的账号应报错")
	}
	if !errors.Is(err, ErrOfficialAccountNotFound) {
		t.Fatalf("应返回 ErrOfficialAccountNotFound，实得 %v", err)
	}
}

// 匹配必须按「号池渠道 + 账号标识」双条件：只按名字删会误伤别的渠道里的同名凭据
// （操作者完全可能手工建过一条同名凭据，那是另一份数据）。
func TestOfficialAccountDeleteKeepsOtherChannelSameNameKey(t *testing.T) {
	conn := openOfficialDeleteTestDB(t)
	account := model.OfficialAccount{
		Provider: model.OfficialAccountProviderClaude, ExternalName: "shared@example.com",
		Status: model.OfficialAccountStatusActive, AccessCipher: "enc:v1:test",
	}
	if err := conn.Create(&account).Error; err != nil {
		t.Fatalf("seed account: %v", err)
	}
	seedPoolCredential(t, conn, account)

	// 另一个渠道里放一条同名凭据：与号池无关，删账号绝不能碰它。
	other := model.Channel{ChannelConfig: model.ChannelConfig{Name: "手工渠道", Enabled: true}}
	if err := conn.Create(&other).Error; err != nil {
		t.Fatalf("seed other channel: %v", err)
	}
	manualKey := model.ChannelKey{
		ChannelKeyConfig: model.ChannelKeyConfig{Name: account.ExternalName, Key: "sealed:manual", Enabled: true},
		ChannelID:        other.ID,
	}
	if err := conn.Create(&manualKey).Error; err != nil {
		t.Fatalf("seed manual key: %v", err)
	}

	if err := OfficialAccountDelete(account.ID); err != nil {
		t.Fatalf("OfficialAccountDelete: %v", err)
	}
	if got := countRows(t, conn, &model.ChannelKey{}, "id = ?", manualKey.ID); got != 1 {
		t.Errorf("别的渠道里的同名凭据不该被删，实得 %d 行", got)
	}
}
