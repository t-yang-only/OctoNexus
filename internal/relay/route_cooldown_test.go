package relay

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-route-003 冷却持久化的 relay 侧接入验证。
//
// op 层的读写单测只能证明"表能存能取"；真正要证明的是**接入点接对了**：
//   打冷却 → 库里出现该行；解除 → 该行消失；恢复 → 内存里真的有了。
// 这三条有任何一条没接上，重启后冷却照样会丢（或反过来复活）。

var routeCooldownTestSeq int64

func openRouteCooldownTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&routeCooldownTestSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Channel{}, &model.ChannelKey{}, &model.ChannelModel{}, &model.ChannelGrant{},
		&model.Group{}, &model.GroupItem{}, &model.APIKey{}, &model.Setting{}, &model.LLMInfo{},
		&model.StatsTotal{}, &model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{},
		&model.QuotaAction{}, &model.RouteCooldown{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))
	if err := op.InitCache(); err != nil {
		t.Fatalf("init cache: %v", err)
	}
	// 路由状态是包级全局：用例间必须隔离，否则上一例的冷却会漏进下一例。
	resetAllRouteState()
	return conn
}

func resetAllRouteState() {
	routeMu.Lock()
	defer routeMu.Unlock()
	routes = make(map[int]*RouteState)
}

func cooldownRowCount(t *testing.T, groupID, itemID int) int64 {
	t.Helper()
	var count int64
	if err := db.GetDB().Model(&model.RouteCooldown{}).
		Where("group_id = ? AND item_id = ?", groupID, itemID).Count(&count).Error; err != nil {
		t.Fatalf("统计冷却行失败: %v", err)
	}
	return count
}

// 打冷却必须落库：库里有行，重启才有东西可恢复。
func TestRecordRouteFailurePersistsCooldown(t *testing.T) {
	openRouteCooldownTestDB(t)
	resetAllRouteState()

	group := cooldownTestGroup(2001, 60)
	// 先建状态（真实链路里由第一次选路创建）。
	routeMu.Lock()
	routes[group.ID] = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
	routeMu.Unlock()

	cooled := recordRouteFailure(group, 11, group.RelayConfig.MemberMaxAttempts, 0)
	if !cooled {
		t.Fatalf("达到尝试上限应触发冷却")
	}
	if got := cooldownRowCount(t, group.ID, 11); got != 1 {
		t.Fatalf("打冷却后库里应有 1 行，实得 %d —— 落库没接上", got)
	}

	// 库里的截止时刻必须与内存一致。
	loaded, err := op.RouteCooldownLoad(context.Background())
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	memDeadline := cooldownOf(group.ID, 11)
	if loaded[group.ID][11] != memDeadline {
		t.Fatalf("库中 deadline = %d, 内存 = %d（两处必须同一个数）", loaded[group.ID][11], memDeadline)
	}
}

// 解除冷却必须删库：否则探活刚证明可用，重启又把它封回去。
func TestClearMemberCooldownRemovesRow(t *testing.T) {
	openRouteCooldownTestDB(t)
	resetAllRouteState()

	group := cooldownTestGroup(2002, 60)
	routeMu.Lock()
	routes[group.ID] = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
	routeMu.Unlock()

	recordRouteFailure(group, 11, group.RelayConfig.MemberMaxAttempts, 0)
	if cooldownRowCount(t, group.ID, 11) != 1 {
		t.Fatalf("前置：应已落库")
	}

	if !clearMemberCooldown(group, 11) {
		t.Fatalf("应确有冷却被解除")
	}
	if got := cooldownRowCount(t, group.ID, 11); got != 0 {
		t.Fatalf("解除后库里不应留行，实得 %d —— 重启会让它复活", got)
	}
}

// 恢复：未到期的写进内存，已到期的丢掉。
//
// 这条同时守住两个方向：漏恢复（冷却丢）与错恢复（过期冷却复活）。
func TestRestoreRouteCooldowns(t *testing.T) {
	openRouteCooldownTestDB(t)
	resetAllRouteState()

	// 直接从库侧造数据：模拟"上一次进程留下的冷却"。
	alive := time.Now().Add(10 * time.Minute).UnixMilli()
	expired := time.Now().Add(-10 * time.Minute).UnixMilli()
	op.RouteCooldownSave(2003, 31, alive)
	op.RouteCooldownSave(2003, 32, expired)

	RestoreRouteCooldowns(context.Background())

	routeMu.Lock()
	route := routes[2003]
	routeMu.Unlock()
	if route == nil {
		t.Fatalf("恢复后应出现分组 2003 的路由状态")
	}
	if route.Cooldowns[31] != alive {
		t.Fatalf("未到期的冷却应被恢复: %+v", route.Cooldowns)
	}
	if _, ok := route.Cooldowns[32]; ok {
		t.Fatalf("已到期的冷却不该被恢复: %+v", route.Cooldowns)
	}
}

// 无冷却可恢复时不该凭空创建分组状态（避免给 341 个分组各留一个空壳）。
func TestRestoreRouteCooldownsNoopWhenEmpty(t *testing.T) {
	openRouteCooldownTestDB(t)
	resetAllRouteState()

	RestoreRouteCooldowns(context.Background())

	routeMu.Lock()
	count := len(routes)
	routeMu.Unlock()
	if count != 0 {
		t.Fatalf("没有冷却时不该创建路由状态，实得 %d 个", count)
	}
}

// 分组被重置（删除/切模式）时，库里的冷却要一并清掉。
func TestResetRouteStateClearsPersistedCooldowns(t *testing.T) {
	openRouteCooldownTestDB(t)
	resetAllRouteState()

	op.RouteCooldownSave(2004, 41, time.Now().Add(time.Minute).UnixMilli())
	routeMu.Lock()
	routes[2004] = &RouteState{GroupID: 2004, Cooldowns: map[int]int64{41: time.Now().Add(time.Minute).UnixMilli()}}
	routeMu.Unlock()

	ResetRouteState(2004)

	if got := cooldownRowCount(t, 2004, 41); got != 0 {
		t.Fatalf("重置后库里不应留行，实得 %d", got)
	}
}

// cooldownTestGroup 构造一个可触发冷却的分组（与 allocate_guard_test.go 的装置同款）。
func cooldownTestGroup(id int, cooldownSeconds int) model.Group {
	return model.Group{
		ID:   id,
		Name: fmt.Sprintf("COOLDOWN-TEST-%d", id),
		Mode: model.GroupModeFailover,
		RelayConfig: model.GroupRelayConfig{
			MemberMaxAttempts:     1,
			MemberCooldownSeconds: cooldownSeconds,
		},
		Items: []model.GroupItem{{ID: 11, Priority: 1}},
	}
}
