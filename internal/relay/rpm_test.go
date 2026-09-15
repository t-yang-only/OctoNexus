package relay

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// T-route-007（lowest_tpm_rpm）的单测：近期负载窗口、按消耗定序、模式切换选中者，
// 以及"尝试就计负载"的生产接线（成功与失败都要记）。

// loadStub 构造负载 provider：只给列出的成员消耗，其余视作"窗口内无记录"（按 0 参与排序）。
func loadStub(values map[int][2]int) LoadProvider {
	return func(itemID int) (int, int, bool) {
		value, ok := values[itemID]
		if !ok {
			return 0, 0, false
		}
		return value[0], value[1], true
	}
}

// TestMemberLoadWindow 负载窗口计数与过期：窗口内可读，过期即视为无记录（避免陈旧负载长期影响选路）。
func TestMemberLoadWindow(t *testing.T) {
	resetMemberLoadForTest()
	now := time.Now().UnixMilli()
	relayNowMs = func() int64 { return now }
	t.Cleanup(func() {
		relayNowMs = func() int64 { return time.Now().UnixMilli() }
		resetMemberLoadForTest()
	})

	recordMemberAttempt(701)
	recordMemberAttempt(701)
	recordMemberTokens(701, 120)

	requests, tokens, ok := memberRecentLoad(701)
	if !ok || requests != 2 || tokens != 120 {
		t.Fatalf("load = (%d, %d, %v), want (2, 120, true)", requests, tokens, ok)
	}

	// 窗口内继续累加。
	recordMemberAttempt(701)
	requests, _, _ = memberRecentLoad(701)
	if requests != 3 {
		t.Fatalf("requests = %d, want 3", requests)
	}

	// 越过 60s 窗口后按"无记录"处理；再记录则从新窗口重新开始。
	now += memberLoadWindowMs + 1
	if _, _, ok := memberRecentLoad(701); ok {
		t.Fatal("expired window still reports load")
	}
	recordMemberTokens(701, 7)
	requests, tokens, ok = memberRecentLoad(701)
	if !ok || requests != 0 || tokens != 7 {
		t.Fatalf("fresh window = (%d, %d, %v), want (0, 7, true)", requests, tokens, ok)
	}

	// 未选目标的尝试（itemID=0）与非正 token 不上账。
	recordMemberAttempt(0)
	recordMemberTokens(702, 0)
	if _, _, ok := memberRecentLoad(702); ok {
		t.Fatal("zero-token record created a load sample")
	}
}

// TestRankByRecentLoadOrdersByRecentConsumption token 消耗少的在前，其次请求数，再按 priority 与 ID。
func TestRankByRecentLoadOrdersByRecentConsumption(t *testing.T) {
	now := time.Now().UnixMilli()
	items := []model.GroupItem{
		strategyItem(9440, 91, 1, "heavy"),   // 1200 token
		strategyItem(9440, 92, 3, "light-a"), // 10 token，请求数 2
		strategyItem(9440, 93, 2, "light-b"), // 10 token，请求数 1
		strategyItem(9440, 94, 4, "fresh"),   // 窗口内无记录 → 按 0
	}
	load := loadStub(map[int][2]int{91: {1, 1200}, 92: {2, 10}, 93: {1, 10}})

	ranked, err := rankByRecentLoad(items, nil, now, nil, load)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	// 无记录(0 token) → 93(10 token, 1 次) → 92(10 token, 2 次) → 91(1200 token)
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{94, 93, 92, 91}) {
		t.Fatalf("order = %v, want [94 93 92 91]", got)
	}

	// 冷却中的成员压队尾，与其它策略同一套过滤口径。
	ranked, err = rankByRecentLoad(items, map[int]int64{94: now + 60_000}, now, nil, load)
	if err != nil {
		t.Fatalf("rank with cooldown: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{93, 92, 91, 94}) {
		t.Fatalf("order with cooldown = %v, want [93 92 91 94]", got)
	}

	// 未接线（nil provider）时全部按 0，退化为 priority 定序。
	ranked, err = rankByRecentLoad(items, nil, now, nil, nil)
	if err != nil {
		t.Fatalf("rank without provider: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{91, 93, 92, 94}) {
		t.Fatalf("order without provider = %v, want priority order [91 93 92 94]", got)
	}
}

// TestRankByRecentLoadAllCoolingReturnsNoEligible 候选全被剔除时返回 ErrNoEligibleMember，调用方据此等待。
func TestRankByRecentLoadAllCoolingReturnsNoEligible(t *testing.T) {
	now := time.Now().UnixMilli()
	items := []model.GroupItem{
		strategyItem(9441, 95, 1, "a"),
		strategyItem(9441, 96, 2, "b"),
	}
	_, err := rankByRecentLoad(items, map[int]int64{95: now + 1000, 96: now + 1000}, now, nil, nil)
	if !errors.Is(err, ErrNoEligibleMember) {
		t.Fatalf("err = %v, want ErrNoEligibleMember", err)
	}
}

// TestPickGroupItemLowestTpmRpmChangesSelection 模式决定选中者：failover 按 priority 选，
// lowest_tpm_rpm 把刚消耗过的成员让开，改选窗口内消耗更少的成员。
func TestPickGroupItemLowestTpmRpmChangesSelection(t *testing.T) {
	resetMemberLoadForTest()
	t.Cleanup(resetMemberLoadForTest)

	items := []model.GroupItem{
		strategyItem(9442, 97, 1, "preferred-but-loaded"), // priority 1，但刚消耗过
		strategyItem(9442, 98, 2, "idle"),
	}
	load := loadStub(map[int][2]int{97: {3, 5000}})

	failover := model.Group{ID: 9442, Mode: model.GroupModeFailover, Items: items}
	if picked := pickGroupItemByMode(failover, routeDeps{load: load}, false); picked.ID != 97 {
		t.Fatalf("failover picked %d, want 97 (priority first)", picked.ID)
	}

	loaded := model.Group{ID: 9442, Mode: model.GroupModeLowestTpmRpm, Items: items}
	if picked := pickGroupItemByMode(loaded, routeDeps{load: load}, false); picked.ID != 98 {
		t.Fatalf("lowest_tpm_rpm picked %d, want 98 (less recent consumption)", picked.ID)
	}
}

// TestRouteOutcomeRecordsMemberLoad 生产接线：一轮成功或失败都要计入该成员的近期负载
// （失败的尝试一样占了上游配额，不能只算成功）。
func TestRouteOutcomeRecordsMemberLoad(t *testing.T) {
	resetMemberLoadForTest()
	t.Cleanup(resetMemberLoadForTest)

	group := model.Group{ID: 9443, Mode: model.GroupModeFailover, Items: []model.GroupItem{
		strategyItem(9443, 99, 1, "m"),
	}, RelayConfig: model.DefaultGroupRelayConfig()}

	recordRouteSuccess(group, 99, 12)
	if _, _, ok := memberRecentLoad(99); !ok {
		t.Fatal("successful round did not record member load")
	}
	recordRouteFailure(group, 99, 1, 30)
	requests, _, ok := memberRecentLoad(99)
	if !ok || requests != 2 {
		t.Fatalf("requests = %d (ok=%v), want 2 after one success and one failure", requests, ok)
	}
}
