package relay

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// busyStub 构造在途 provider：只给列出的成员在途数，其余视作 0（空闲）。
func busyStub(values map[int]int) BusyProvider {
	return func(itemID int) int { return values[itemID] }
}

// TestRankByBusyOrdersByInFlight 在途少的在前；平手时按 priority，再按 ID。
func TestRankByBusyOrdersByInFlight(t *testing.T) {
	now := time.Now().UnixMilli()
	items := []model.GroupItem{
		strategyItem(9430, 91, 1, "busy"),     // 在途 3
		strategyItem(9430, 92, 3, "idle-b"),   // 在途 0
		strategyItem(9430, 93, 2, "idle-a"),   // 在途 0，priority 更靠前
		strategyItem(9430, 94, 4, "one-shot"), // 在途 1
	}
	busy := busyStub(map[int]int{91: 3, 94: 1})

	ranked, err := rankByBusy(items, nil, now, nil, busy)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{93, 92, 94, 91}) {
		t.Fatalf("order = %v, want [93 92 94 91] (idle first by priority, then 1 in-flight, busiest last)", got)
	}

	// 冷却中的成员压队尾（与其它策略同一套过滤口径）。
	ranked, err = rankByBusy(items, map[int]int64{93: now + 60_000}, now, nil, busy)
	if err != nil {
		t.Fatalf("rank with cooldown: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{92, 94, 91, 93}) {
		t.Fatalf("order = %v, want [92 94 91 93] (cooling member last)", got)
	}
}

// TestRankByBusyNilProviderKeepsPriorityOrder provider 未接线时全部按 0，退化为 priority 定序。
func TestRankByBusyNilProviderKeepsPriorityOrder(t *testing.T) {
	items := []model.GroupItem{
		strategyItem(9431, 95, 2, "second"),
		strategyItem(9431, 96, 1, "first"),
	}
	ranked, err := rankByBusy(items, nil, time.Now().UnixMilli(), nil, nil)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{96, 95}) {
		t.Fatalf("order = %v, want [96 95]", got)
	}
}

// TestPickGroupItemByModeLeastBusyBeatsPriority 最空闲真的改变选中的人：
// 并发压上来时（高优先级成员已有 1 个在途）改选空闲成员，failover 仍按 priority。
func TestPickGroupItemByModeLeastBusyBeatsPriority(t *testing.T) {
	gid := 9432
	items := []model.GroupItem{
		strategyItem(gid, 97, 1, "preferred"),
		strategyItem(gid, 98, 2, "spare"),
	}
	cfg := model.DefaultGroupRelayConfig()
	busy := busyStub(map[int]int{97: 1})

	legacy := model.Group{ID: gid, Name: "b-legacy", Mode: model.GroupModeFailover, Items: items, RelayConfig: cfg}
	if got := pickGroupItemByMode(legacy, routeDeps{busy: busy}, false); got.ID != 97 {
		t.Fatalf("failover picked %d, want 97 (priority order unchanged by in-flight)", got.ID)
	}

	busyGroup := model.Group{ID: gid, Name: "b", Mode: model.GroupModeLeastBusy, Items: items, RelayConfig: cfg}
	if got := pickGroupItemByMode(busyGroup, routeDeps{busy: busy}, false); got.ID != 98 {
		t.Fatalf("least_busy picked %d, want 98 (idle member wins)", got.ID)
	}
	// 空闲成员被占用后，选择回到原来的成员（两个都在途时按 priority）。
	if got := pickGroupItemByMode(busyGroup, routeDeps{busy: busyStub(map[int]int{97: 1, 98: 1})}, false); got.ID != 97 {
		t.Fatalf("least_busy picked %d, want 97 when both are busy (priority tie-break)", got.ID)
	}
}

// registerTestRequest 把一个请求直接放进活动请求注册表（不走 newRequestState，避免依赖 op/DB），
// 用例结束后自动摘除，防止污染其它用例。
func registerTestRequest(t *testing.T, id uint64) *RequestState {
	t.Helper()
	request := &RequestState{ID: id, Status: StatusRunning, StartedAt: time.Now()}
	mu.Lock()
	requests[id] = request
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		delete(requests, id)
		mu.Unlock()
	})
	return request
}

// TestMemberBusyCountFollowsRounds 在途数直接从活动请求注册表派生：
// 一轮开始即计入、轮次结束即归零，两个请求压在同一个成员上算 2，且不会出现"漏释放"。
func TestMemberBusyCountFollowsRounds(t *testing.T) {
	first := registerTestRequest(t, 900001)
	second := registerTestRequest(t, 900002)

	if got := memberBusyCount(71); got != 0 {
		t.Fatalf("busy(71) = %d before any round, want 0", got)
	}

	first.startRound(func() {}, 71, "chan-a", "mock-a", model.ProtocolOpenAIChatCompletion)
	if got := memberBusyCount(71); got != 1 {
		t.Fatalf("busy(71) = %d after first round start, want 1", got)
	}
	second.startRound(func() {}, 71, "chan-a", "mock-a", model.ProtocolOpenAIChatCompletion)
	if got := memberBusyCount(71); got != 2 {
		t.Fatalf("busy(71) = %d with two requests waiting, want 2", got)
	}
	// 别的成员不受影响。
	if got := memberBusyCount(72); got != 0 {
		t.Fatalf("busy(72) = %d, want 0", got)
	}

	first.finishRound(nil, false)
	if got := memberBusyCount(71); got != 1 {
		t.Fatalf("busy(71) = %d after one round finished, want 1 (no leak, no over-count)", got)
	}
	second.finishRound(errors.New("upstream 500"), false)
	if got := memberBusyCount(71); got != 0 {
		t.Fatalf("busy(71) = %d after both rounds finished, want 0", got)
	}
	// 哨兵：itemID 0 永远为 0。
	if got := memberBusyCount(0); got != 0 {
		t.Fatalf("busy(0) = %d, want 0", got)
	}
}
