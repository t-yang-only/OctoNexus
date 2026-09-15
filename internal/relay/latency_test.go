package relay

import (
	"reflect"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// latencyStub 构造延迟 provider：只给列出的成员耗时，其余视作"无耗时数据"。
func latencyStub(values map[int]int64) LatencyProvider {
	return func(itemID int) (int64, bool) {
		value, ok := values[itemID]
		return value, ok
	}
}

// TestMemberLatencyWindow 延迟样本同样只反映窗口内的表现；耗时 <=0 不覆盖已知值。
func TestMemberLatencyWindow(t *testing.T) {
	advance := withFrozenClock(t, 5_000_000)

	recordMemberOutcome(51, true, 120)
	if got, ok := memberLatencyMs(51); !ok || got != 120 {
		t.Fatalf("latency = %d ok=%v, want 120 true", got, ok)
	}
	// 后一次更快：取最近一次，说明"最近耗时"确实在更新。
	recordMemberOutcome(51, true, 40)
	if got, _ := memberLatencyMs(51); got != 40 {
		t.Fatalf("latency = %d, want the latest 40", got)
	}
	// 无耗时（<=0，例如上游错误没给出可测耗时）不得覆盖已知值。
	recordMemberOutcome(51, false, 0)
	if got, ok := memberLatencyMs(51); !ok || got != 40 {
		t.Fatalf("latency = %d ok=%v, want 40 true (zero latency must not overwrite)", got, ok)
	}
	// 从未记录过的成员没有耗时数据。
	if _, ok := memberLatencyMs(52); ok {
		t.Fatalf("unknown member must report no latency")
	}
	// 窗口过期后按无数据处理。
	advance(memberQualityWindowMs + 1)
	if _, ok := memberLatencyMs(51); ok {
		t.Fatalf("window expired but latency survived")
	}
}

// TestRankByLatencyOrdersByLatency 耗时短的在前；无耗时数据按 0 参与（乐观先验）。
func TestRankByLatencyOrdersByLatency(t *testing.T) {
	now := time.Now().UnixMilli()
	items := []model.GroupItem{
		strategyItem(9420, 61, 1, "slow"),    // 900ms
		strategyItem(9420, 62, 2, "fast"),    // 50ms
		strategyItem(9420, 63, 3, "unknown"), // 无数据 → 0ms
	}
	latency := latencyStub(map[int]int64{61: 900, 62: 50})

	ranked, err := rankByLatency(items, nil, now, nil, latency)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{63, 62, 61}) {
		t.Fatalf("order = %v, want [63 62 61] (unknown optimistic, then fastest, slowest last)", got)
	}

	// 冷却中的成员压队尾（与最低成本/质量优先同一套过滤口径）。
	ranked, err = rankByLatency(items, map[int]int64{62: now + 60_000}, now, nil, latency)
	if err != nil {
		t.Fatalf("rank with cooldown: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{63, 61, 62}) {
		t.Fatalf("order = %v, want [63 61 62] (cooling member last)", got)
	}
}

// TestRankByLatencyNilProviderKeepsPriorityOrder provider 未接线时全部按 0，退化为 priority 定序。
func TestRankByLatencyNilProviderKeepsPriorityOrder(t *testing.T) {
	items := []model.GroupItem{
		strategyItem(9421, 71, 2, "second"),
		strategyItem(9421, 72, 1, "first"),
	}
	ranked, err := rankByLatency(items, nil, time.Now().UnixMilli(), nil, nil)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{72, 71}) {
		t.Fatalf("order = %v, want [72 71]", got)
	}
}

// TestPickGroupItemByModeLowestLatencyBeatsPriority 最低延迟真的改变选中的人：
// 同一批成员下 lowest_latency 选最近更快的成员（哪怕优先级更靠后），failover 仍按 priority。
func TestPickGroupItemByModeLowestLatencyBeatsPriority(t *testing.T) {
	gid := 9422
	items := []model.GroupItem{
		strategyItem(gid, 81, 1, "slow"), // priority 好但耗时 2s
		strategyItem(gid, 82, 2, "fast"), // 耗时 30ms
	}
	latency := latencyStub(map[int]int64{81: 2000, 82: 30})
	cfg := model.DefaultGroupRelayConfig()

	legacy := model.Group{ID: gid, Name: "l-legacy", Mode: model.GroupModeFailover, Items: items, RelayConfig: cfg}
	if got := pickGroupItemByMode(legacy, routeDeps{latency: latency}, false); got.ID != 81 {
		t.Fatalf("failover picked %d, want 81 (priority order unchanged by latency)", got.ID)
	}

	latGroup := model.Group{ID: gid, Name: "l", Mode: model.GroupModeLowestLatency, Items: items, RelayConfig: cfg}
	if got := pickGroupItemByMode(latGroup, routeDeps{latency: latency}, false); got.ID != 82 {
		t.Fatalf("lowest_latency picked %d, want 82 (faster member wins)", got.ID)
	}
	// 加权轮询开关打开也不影响 lowest_latency 的口径（模式优先于全局开关）。
	if got := pickGroupItemByMode(latGroup, routeDeps{latency: latency}, true); got.ID != 82 {
		t.Fatalf("lowest_latency with balance flag on picked %d, want 82", got.ID)
	}
	// 生产 provider（读进程内样本）同样生效：把 81 记成 5ms、82 记成 3s，选择随之反转。
	withFrozenClock(t, 6_000_000)
	recordMemberOutcome(81, true, 5)
	recordMemberOutcome(82, true, 3000)
	if got := pickGroupItemByMode(latGroup, routeDeps{latency: memberLatencyMs}, false); got.ID != 81 {
		t.Fatalf("after recording latencies picked %d, want 81 (now the fastest)", got.ID)
	}
}
