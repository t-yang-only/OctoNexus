package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// weightedDeps 用桩构造五维信号, 便于断言"换权重就换选择"这一核心行为。
type weightedDeps struct {
	cost    map[int]float64
	quality map[int]float64
	latency map[int]int64
	busy    map[int]int
	load    map[int]int
}

func (d weightedDeps) deps() routeDeps {
	return routeDeps{
		cost: func(item model.GroupItem) (float64, bool) {
			value, ok := d.cost[item.ID]
			return value, ok
		},
		quality: func(itemID int) (float64, bool) {
			value, ok := d.quality[itemID]
			return value, ok
		},
		latency: func(itemID int) (int64, bool) {
			value, ok := d.latency[itemID]
			return value, ok
		},
		busy: func(itemID int) int { return d.busy[itemID] },
		load: func(itemID int) (int, int, bool) {
			value, ok := d.load[itemID]
			return 1, value, ok
		},
	}
}

func weightedItems() []model.GroupItem {
	return []model.GroupItem{
		{ID: 11, Priority: 1, Available: true},
		{ID: 22, Priority: 2, Available: true},
		{ID: 33, Priority: 3, Available: true},
	}
}

func order(t *testing.T, ranked []model.GroupItem) []int {
	t.Helper()
	ids := make([]int, 0, len(ranked))
	for _, item := range ranked {
		ids = append(ids, item.ID)
	}
	return ids
}

// TestWeightedCostDominatesPicksCheapest 成本权重压倒其它维度时, 选最便宜的成员（哪怕它 priority 最靠后）。
func TestWeightedCostDominatesPicksCheapest(t *testing.T) {
	deps := weightedDeps{
		cost:    map[int]float64{11: 9, 22: 1, 33: 5},
		quality: map[int]float64{11: 0.99, 22: 0.5, 33: 0.8},
		latency: map[int]int64{11: 10, 22: 900, 33: 100},
	}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil,
		deps.deps(), weightSettings{cost: 100})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] != 22 {
		t.Fatalf("成本权重全给成本时应选最便宜的 22, got %v", got)
	}
}

// TestWeightedQualityDominatesBeatsCheaper 质量权重压倒成本时, "贵但稳"可以压过"便宜但常错"
// —— 这正是需要用户拍板的取舍点, 这里只固定"权重真的生效"这一机制。
func TestWeightedQualityDominatesBeatsCheaper(t *testing.T) {
	deps := weightedDeps{
		cost:    map[int]float64{11: 9, 22: 1, 33: 5},
		quality: map[int]float64{11: 0.99, 22: 0.1, 33: 0.5},
	}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil,
		deps.deps(), weightSettings{quality: 100})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] != 11 {
		t.Fatalf("质量权重全给质量时应选成功率最高的 11, got %v", got)
	}
}

// TestWeightedMissingDataIsOptimistic 缺数据的成员按乐观先验参与:
// 只有部分成员有该维度数据时, 没数据的那个记最好值（先给它一次机会, 试出真相后自然变化）。
func TestWeightedMissingDataIsOptimistic(t *testing.T) {
	deps := weightedDeps{latency: map[int]int64{11: 15_000}} // 只有 11 有耗时数据（很慢）
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{latency: 100})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] == 11 {
		t.Fatalf("已知很慢的 11 不该继续排在无数据的成员之前, got %v", got)
	}
}

// TestWeightedNoDataAtAllFallsBackToPriority 所有候选都没有任何维度数据时, 全员同分 → priority 顺序。
func TestWeightedNoDataAtAllFallsBackToPriority(t *testing.T) {
	deps := weightedDeps{}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{cost: 30, quality: 30, latency: 15, busy: 15, load: 10})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] != 11 || got[1] != 22 || got[2] != 33 {
		t.Fatalf("没有任何数据时应保持 priority 顺序, got %v", got)
	}
}

// TestWeightedZeroWeightsFallsBackToPriority 权重全 0（用户显式关掉所有维度）时也退化为 priority 顺序, 不报错。
func TestWeightedZeroWeightsFallsBackToPriority(t *testing.T) {
	deps := weightedDeps{
		cost:    map[int]float64{11: 9, 22: 1, 33: 5},
		quality: map[int]float64{11: 0.1, 22: 0.9, 33: 0.5},
	}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(), weightSettings{})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] != 11 {
		t.Fatalf("权重全 0 时应按 priority 取 11, got %v", got)
	}
}

// TestWeightedCooldownGoesToTailAndAllCoolingFails 冷却成员压队尾; 全员冷却时报无人可用（由外层等待冷却到期）。
func TestWeightedCooldownGoesToTailAndAllCoolingFails(t *testing.T) {
	deps := weightedDeps{cost: map[int]float64{11: 9, 22: 1, 33: 5}}
	now := int64(10_000)
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{22: now + 60_000}, now, nil,
		deps.deps(), weightSettings{cost: 100})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[len(got)-1] != 22 {
		t.Fatalf("冷却成员应压队尾, got %v", got)
	}
	cooldowns := map[int]int64{11: now + 60_000, 22: now + 60_000, 33: now + 60_000}
	if _, err := rankByWeighted(weightedItems(), cooldowns, now, nil, deps.deps(), weightSettings{cost: 100}); err == nil {
		t.Fatalf("全员冷却时应返回 ErrNoEligibleMember")
	}
}

// TestWeightedTiesBreakByPriorityAndID 并列时按 priority、再按 ID, 保证同输入同输出。
func TestWeightedTiesBreakByPriorityAndID(t *testing.T) {
	deps := weightedDeps{latency: map[int]int64{11: 100, 22: 100, 33: 100}}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{latency: 100})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] != 11 || got[1] != 22 || got[2] != 33 {
		t.Fatalf("全等时应按 priority/ID 定序, got %v", got)
	}
}
