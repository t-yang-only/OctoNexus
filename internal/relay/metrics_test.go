package relay

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// qualityStub 构造一个质量 provider：只给列出的成员成功率，其余视作无样本。
func qualityStub(rates map[int]float64) MemberQualityProvider {
	return func(itemID int) (float64, bool) {
		rate, ok := rates[itemID]
		return rate, ok
	}
}

// withFrozenClock 冻结 relayNowMs 并在用例结束后复位（质量窗口衰减靠它验证）。
func withFrozenClock(t *testing.T, startMs int64) func(int64) {
	t.Helper()
	original := relayNowMs
	now := startMs
	relayNowMs = func() int64 { return now }
	resetMemberQualityForTest()
	t.Cleanup(func() {
		relayNowMs = original
		resetMemberQualityForTest()
	})
	return func(delta int64) { now += delta }
}

// TestMemberQualityWindowDecays 质量样本只反映窗口内的表现：窗口过期后按"无样本"处理,
// 过期之后新记的结果开启新窗口。
func TestMemberQualityWindowDecays(t *testing.T) {
	advance := withFrozenClock(t, 1_000_000)

	recordMemberOutcome(11, true, 0)
	recordMemberOutcome(11, false, 0)
	recordMemberOutcome(11, false, 0)
	recordMemberOutcome(11, false, 0)

	rate, ok := memberSuccessRate(11)
	if !ok || rate != 0.25 {
		t.Fatalf("rate = %v ok=%v, want 0.25 true (1 success / 4 attempts)", rate, ok)
	}
	if _, ok := memberSuccessRate(999); ok {
		t.Fatalf("unknown member must report no samples")
	}
	// itemID 0 是"无成员"哨兵：不得被统计。
	recordMemberOutcome(0, true, 0)
	if _, ok := memberSuccessRate(0); ok {
		t.Fatalf("sentinel itemID 0 must not collect samples")
	}

	advance(memberQualityWindowMs + 1)
	if _, ok := memberSuccessRate(11); ok {
		t.Fatalf("window expired but samples survived")
	}

	recordMemberOutcome(11, true, 0)
	rate, ok = memberSuccessRate(11)
	if !ok || rate != 1.0 {
		t.Fatalf("after expiry the new window must start clean: rate=%v ok=%v", rate, ok)
	}
}

// TestRankByQualityOrdersBySuccessRate 成功率高的在前；无样本按中性先验 1.0 参与排序。
func TestRankByQualityOrdersBySuccessRate(t *testing.T) {
	items := []model.GroupItem{
		strategyItem(9401, 11, 1, "bad"),     // 0.2
		strategyItem(9401, 12, 2, "good"),    // 0.9
		strategyItem(9401, 13, 3, "unknown"), // 无样本 → 中性先验 1.0
	}
	quality := qualityStub(map[int]float64{11: 0.2, 12: 0.9})

	ranked, err := rankByQuality(items, nil, time.Now().UnixMilli(), nil, quality)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{13, 12, 11}) {
		t.Fatalf("order = %v, want [13 12 11] (unknown is neutral, then best rate, worst last)", got)
	}
}

// TestRankByQualityNilProviderKeepsPriorityOrder provider 未接线时全部按中性先验,
// 顺序退化为 priority 定序——接线漏了也不会把成员顺序打乱。
func TestRankByQualityNilProviderKeepsPriorityOrder(t *testing.T) {
	items := []model.GroupItem{
		strategyItem(9402, 21, 2, "second"),
		strategyItem(9402, 22, 1, "first"),
		strategyItem(9402, 23, 3, "third"),
	}
	ranked, err := rankByQuality(items, nil, time.Now().UnixMilli(), nil, nil)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{22, 21, 23}) {
		t.Fatalf("order = %v, want [22 21 23] (priority order preserved)", got)
	}
}

// TestRankByQualityKeepsCooldownAndExclusions 与最低成本同一套过滤口径：
// 冷却成员压队尾（到期即回）、不可用与归零剔除、可尝试成员为空时报错。
func TestRankByQualityKeepsCooldownAndExclusions(t *testing.T) {
	now := time.Now().UnixMilli()
	quality := qualityStub(map[int]float64{31: 1.0, 32: 0.5, 33: 0.9})

	unavailable := strategyItem(9403, 34, 1, "unavailable")
	unavailable.Available = false
	items := []model.GroupItem{
		strategyItem(9403, 31, 1, "perfect"),
		strategyItem(9403, 32, 2, "half"),
		strategyItem(9403, 33, 3, "good"),
		unavailable,
	}

	ranked, err := rankByQuality(items, map[int]int64{31: now + 60000}, now, nil, quality)
	if err != nil {
		t.Fatalf("rank with cooldown: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{33, 32, 31}) {
		t.Fatalf("order = %v, want [33 32 31] (best rate first, cooling member last, unavailable dropped)", got)
	}

	ranked, err = rankByQuality(items, nil, now, map[int]bool{33: true}, quality)
	if err != nil {
		t.Fatalf("rank with zero balance: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{31, 32}) {
		t.Fatalf("order = %v, want [31 32] (zero-balance and unavailable dropped)", got)
	}

	if _, err := rankByQuality([]model.GroupItem{unavailable}, nil, now, nil, quality); !errors.Is(err, ErrNoEligibleMember) {
		t.Fatalf("err = %v, want ErrNoEligibleMember", err)
	}
}

// TestPickGroupItemByModeQualityFirstBeatsPriority 质量优先真的改变选中的人：
// 同一批成员下 quality_first 选近期成功率高的成员（哪怕它优先级更靠后），
// 而 failover（开关关）仍按 priority 选首个。
func TestPickGroupItemByModeQualityFirstBeatsPriority(t *testing.T) {
	gid := 9410
	items := []model.GroupItem{
		strategyItem(gid, 111, 1, "flaky"), // priority 最好但近期成功率低
		strategyItem(gid, 112, 2, "solid"),
	}
	quality := qualityStub(map[int]float64{111: 0.25, 112: 1.0})
	cfg := model.DefaultGroupRelayConfig()

	legacy := model.Group{ID: gid, Name: "q-legacy", Mode: model.GroupModeFailover, Items: items, RelayConfig: cfg}
	if got := pickGroupItemByMode(legacy, routeDeps{quality: quality}, false); got.ID != 111 {
		t.Fatalf("failover picked %d, want 111 (priority order unchanged by quality)", got.ID)
	}

	qGroup := model.Group{ID: gid, Name: "q", Mode: model.GroupModeQualityFirst, Items: items, RelayConfig: cfg}
	if got := pickGroupItemByMode(qGroup, routeDeps{quality: quality}, false); got.ID != 112 {
		t.Fatalf("quality_first picked %d, want 112 (higher success rate wins)", got.ID)
	}
	// 加权轮询开关打开也不影响 quality_first 的定序口径（模式优先于全局开关）。
	if got := pickGroupItemByMode(qGroup, routeDeps{quality: quality}, true); got.ID != 112 {
		t.Fatalf("quality_first with balance flag on picked %d, want 112", got.ID)
	}
}

// TestRecordMemberOutcomeFeedsPicking 写入点到选路端到端：坏成员失败若干次后,
// 质量优先会把请求直接交给表现好的成员（这正是"按质量自动切换"的最小闭环）。
func TestRecordMemberOutcomeFeedsPicking(t *testing.T) {
	withFrozenClock(t, 2_000_000)
	gid := 9411
	items := []model.GroupItem{
		strategyItem(gid, 121, 1, "flaky"),
		strategyItem(gid, 122, 2, "solid"),
	}
	group := model.Group{ID: gid, Name: "q2", Mode: model.GroupModeQualityFirst, Items: items,
		RelayConfig: model.DefaultGroupRelayConfig()}

	// 双方都无样本：按 priority 先试 flaky。
	if got := pickGroupItemByMode(group, routeDeps{quality: memberSuccessRate}, false); got.ID != 121 {
		t.Fatalf("cold start picked %d, want 121 (priority order when no samples)", got.ID)
	}
	// flaky 连吃两次失败、solid 成功一次。
	recordMemberOutcome(121, false, 0)
	recordMemberOutcome(121, false, 0)
	recordMemberOutcome(122, true, 0)

	if got := pickGroupItemByMode(group, routeDeps{quality: memberSuccessRate}, false); got.ID != 122 {
		t.Fatalf("after failures picked %d, want 122 (quality switches away from the flaky member)", got.ID)
	}
}
