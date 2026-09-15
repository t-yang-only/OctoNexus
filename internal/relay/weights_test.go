package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// weightedDeps 用桩构造各维信号, 便于断言"换权重就换选择"这一核心行为。
type weightedDeps struct {
	cost    map[int]float64
	quality map[int]float64
	latency map[int]int64
	busy    map[int]int
	load    map[int]int
	// billing 是第二阶段的渠道计费事实（倍率/按次/包月/余额）, 按成员 ID 给。
	billing map[int]model.ChannelBilling
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
		billing: func(item model.GroupItem) (model.ChannelBilling, bool) {
			value, ok := d.billing[item.ID]
			return value, ok
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

// ---------- 第二阶段：倍率 / 按次单价 / 余额 / 包月余量 ----------

// TestWeightedMultiplierDominates 倍率权重拉满时选倍率最低的成员（同样价格的站, 倍率 2 比倍率 1 贵一倍）。
func TestWeightedMultiplierDominates(t *testing.T) {
	deps := weightedDeps{billing: map[int]model.ChannelBilling{
		11: {Multiplier: 2.0},
		22: {Multiplier: 1.0},
		33: {Multiplier: 1.5},
	}}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{multiplier: 100, monthlyAction: "demote"})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] != 22 {
		t.Fatalf("倍率权重拉满应选倍率最低的 22, got %v", got)
	}
}

// TestWeightedPerCallDominates 按次单价权重拉满时选每请求最便宜的成员。
func TestWeightedPerCallDominates(t *testing.T) {
	deps := weightedDeps{billing: map[int]model.ChannelBilling{
		11: {Mode: "per_call", PerCallPrice: 0.05},
		22: {Mode: "per_call", PerCallPrice: 0.01},
	}}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{perCall: 100, monthlyAction: "demote"})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] != 22 {
		t.Fatalf("按次权重拉满应选单次最便宜的 22, got %v", got)
	}
}

// TestWeightedBalanceDominates 余额权重拉满时选余额最多的成员（余额来自配额扫描的最近一次读数）。
func TestWeightedBalanceDominates(t *testing.T) {
	deps := weightedDeps{billing: map[int]model.ChannelBilling{
		11: {Balance: 3, BalanceKnown: true},
		22: {Balance: 90, BalanceKnown: true},
	}}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{balance: 100, monthlyAction: "demote"})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] != 22 {
		t.Fatalf("余额权重拉满应选余额最多的 22, got %v", got)
	}
}

// TestWeightedMonthlyDemoteKeepsMemberAvailable 包月用尽的成员在 demote（默认）下只是降权, 仍可能被选到。
func TestWeightedMonthlyDemoteKeepsMemberAvailable(t *testing.T) {
	deps := weightedDeps{billing: map[int]model.ChannelBilling{
		11: {MonthlyQuota: 100, MonthlyUsed: 100}, // 用尽
		22: {MonthlyQuota: 100, MonthlyUsed: 10},  // 还剩 90%
	}}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{monthly: 100, monthlyAction: "demote"})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	got := order(t, ranked)
	if len(got) != 3 {
		t.Fatalf("demote 不应剔除任何成员, got %v", got)
	}
	if got[0] != 22 {
		t.Fatalf("包月余量多的应排在前面, got %v", got)
	}
	if got[len(got)-1] != 11 {
		t.Fatalf("包月用尽的应被降权到后面（但仍在候选里）, got %v", got)
	}
}

// TestWeightedMonthlyExcludeDropsMember 选择 exclude 时包月用尽的成员不再参与。
func TestWeightedMonthlyExcludeDropsMember(t *testing.T) {
	deps := weightedDeps{billing: map[int]model.ChannelBilling{
		11: {MonthlyQuota: 100, MonthlyUsed: 100},
		22: {MonthlyQuota: 100, MonthlyUsed: 10},
	}}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{monthly: 100, monthlyAction: "exclude"})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	for _, item := range ranked {
		if item.ID == 11 {
			t.Fatalf("exclude 下包月用尽的 11 不该出现在候选里, got %v", order(t, ranked))
		}
	}
	// 剩下的两个: 22 有包月数据（还剩 90%）, 33 没有包月数据 → 按乐观先验 33 在前。
	// 这正是「没填过的成员先给一次机会」的口径（与 lowest_latency 的无耗时按 0ms、quality_first 的无样本中性 1.0 一致）。
	if got := order(t, ranked); got[0] != 33 || got[1] != 22 {
		t.Fatalf("剩下的应选 33（无包月数据, 乐观先验）再 22, got %v", got)
	}
}

// TestWeightedMonthlyExcludeAllExhaustedReportsNoMember 全员包月用尽 + exclude 时报无人可用（外层等下一轮）。
func TestWeightedMonthlyExcludeAllExhaustedReportsNoMember(t *testing.T) {
	deps := weightedDeps{billing: map[int]model.ChannelBilling{
		11: {MonthlyQuota: 10, MonthlyUsed: 10},
		22: {MonthlyQuota: 10, MonthlyUsed: 10},
		33: {MonthlyQuota: 10, MonthlyUsed: 10},
	}}
	if _, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{monthly: 100, monthlyAction: "exclude"}); err == nil {
		t.Fatal("全员包月用尽 + exclude 时应返回 ErrNoEligibleMember")
	}
}

// TestWeightedBillingUnknownIsOptimistic 没填计费信息的成员按乐观先验参与: 已知倍率很差的成员排在它之后。
func TestWeightedBillingUnknownIsOptimistic(t *testing.T) {
	deps := weightedDeps{billing: map[int]model.ChannelBilling{
		11: {Multiplier: 5.0}, // 贵得离谱
	}}
	ranked, err := rankByWeighted(weightedItems(), map[int]int64{}, 1000, nil, deps.deps(),
		weightSettings{multiplier: 100, monthlyAction: "demote"})
	if err != nil {
		t.Fatalf("rankByWeighted: %v", err)
	}
	if got := order(t, ranked); got[0] == 11 {
		t.Fatalf("已知倍率很差的 11 不该继续排第一, got %v", got)
	}
}

// TestHotRouteDepsCoversEveryDimension 守卫生产热路径的 provider 接线。
//
// 新增维度时如果只在 hotRouteDeps 里接线、或只在 pickGroupItemHot 的副本里接线, 本测试会红:
// 该测试盯着 hotRouteDeps（热路径现在唯一使用的依赖集合）。
func TestHotRouteDepsCoversEveryDimension(t *testing.T) {
	deps := hotRouteDeps()
	if deps.cost == nil {
		t.Error("hotRouteDeps.cost 未接线")
	}
	if deps.quality == nil {
		t.Error("hotRouteDeps.quality 未接线")
	}
	if deps.latency == nil {
		t.Error("hotRouteDeps.latency 未接线")
	}
	if deps.busy == nil {
		t.Error("hotRouteDeps.busy 未接线")
	}
	if deps.load == nil {
		t.Error("hotRouteDeps.load 未接线")
	}
	if deps.billing == nil {
		t.Error("hotRouteDeps.billing 未接线（倍率/按次/包月/余额四个维度会静默失效）")
	}
}
