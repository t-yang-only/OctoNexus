package relay

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// strategyItem 构造一个可转发的成员: Available 由 op.groupSnapshot 回填, 内存构造须自带。
func strategyItem(groupID, id, priority int, modelName string) model.GroupItem {
	grant := id
	return model.GroupItem{
		ID: id, GroupID: groupID, Priority: priority, ModelName: modelName,
		Available: true, ChannelGrantID: &grant,
	}
}

// strategyCost 用生产同款折算函数构造 stub：nil（查不到）与 0 价都按"无数据"处理，
// 避免单测里自己发明一套"0 价算便宜"的口径。
func strategyCost(prices map[string]*model.LLMPrice) CostProvider {
	return func(item model.GroupItem) (float64, bool) {
		return unitPriceFromPrice(prices[item.ModelName])
	}
}

// llmPrice 便捷构造价表条目（单位成本 = Input + Output）。
func llmPrice(in, out float64) *model.LLMPrice {
	return &model.LLMPrice{Input: in, Output: out}
}

func itemIDs(items []model.GroupItem) []int {
	out := make([]int, 0, len(items))
	for _, item := range items {
		out = append(out, item.ID)
	}
	return out
}

// TestRankByLowestCostOrdersByPrice 便宜的在前; 同价按 priority 升序, 再按 ID 升序。
func TestRankByLowestCostOrdersByPrice(t *testing.T) {
	items := []model.GroupItem{
		strategyItem(9301, 11, 1, "pricey"),
		strategyItem(9301, 12, 3, "cheap"),
		strategyItem(9301, 13, 2, "mid"),
		strategyItem(9301, 14, 9, "cheap"), // 与 12 同价, priority 更大 → 排在 12 之后
	}
	cost := strategyCost(map[string]*model.LLMPrice{
		"pricey": llmPrice(1.0, 2.0),  // 3.00
		"cheap":  llmPrice(0.15, 0.6), // 0.75
		"mid":    llmPrice(0.5, 1.0),  // 1.50
	})

	ranked, err := rankByLowestCost(items, nil, time.Now().UnixMilli(), nil, cost)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{12, 14, 13, 11}) {
		t.Fatalf("order = %v, want [12 14 13 11]", got)
	}
}

// TestRankByLowestCostSinksMissingPrice 查不到价格与价表 0 价都按"无数据"沉底（仍保留在表内可兜底），
// 否则 0 价成员会永远当选, 把流量全推到没标价的成员上。
func TestRankByLowestCostSinksMissingPrice(t *testing.T) {
	items := []model.GroupItem{
		strategyItem(9302, 21, 1, "unknown"),
		strategyItem(9302, 22, 1, "zeropriced"),
		strategyItem(9302, 23, 5, "cheap"),
		strategyItem(9302, 24, 6, "mid"),
	}
	cost := strategyCost(map[string]*model.LLMPrice{
		"zeropriced": llmPrice(0, 0), // 价表里有条目但写 0 → 视作无数据
		"cheap":      llmPrice(0.05, 0.05),
		"mid":        llmPrice(0.1, 0.1),
	})

	ranked, err := rankByLowestCost(items, nil, time.Now().UnixMilli(), nil, cost)
	if err != nil {
		t.Fatalf("rank: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{23, 24, 21, 22}) {
		t.Fatalf("order = %v, want [23 24 21 22] (no-data members last, by priority/ID)", got)
	}
}

// TestRankByLowestCostCooldownTailAndExclusions 冷却成员压队尾（到期即回）、不可用与归零剔除、
// 可尝试成员为空时报 ErrNoEligibleMember（冷却成员不算可尝试）。
func TestRankByLowestCostCooldownTailAndExclusions(t *testing.T) {
	now := time.Now().UnixMilli()
	cost := strategyCost(map[string]*model.LLMPrice{
		"cheap":  llmPrice(0.05, 0.05),
		"mid":    llmPrice(0.1, 0.1),
		"pricey": llmPrice(0.15, 0.15),
	})

	unavailable := strategyItem(9303, 34, 1, "cheap")
	unavailable.Available = false
	items := []model.GroupItem{
		strategyItem(9303, 31, 1, "cheap"),
		strategyItem(9303, 32, 2, "mid"),
		strategyItem(9303, 33, 3, "pricey"),
		unavailable,
	}

	ranked, err := rankByLowestCost(items, map[int]int64{32: now + 60000}, now, nil, cost)
	if err != nil {
		t.Fatalf("rank with cooldown: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{31, 33, 32}) {
		t.Fatalf("order = %v, want [31 33 32] (cooldown last, unavailable dropped)", got)
	}

	ranked, err = rankByLowestCost(items, nil, now, map[int]bool{31: true}, cost)
	if err != nil {
		t.Fatalf("rank with zero balance: %v", err)
	}
	if got := itemIDs(ranked); !reflect.DeepEqual(got, []int{32, 33}) {
		t.Fatalf("order = %v, want [32 33] (zero-balance and unavailable dropped)", got)
	}

	if _, err := rankByLowestCost([]model.GroupItem{unavailable}, nil, now, nil, cost); !errors.Is(err, ErrNoEligibleMember) {
		t.Fatalf("err = %v, want ErrNoEligibleMember", err)
	}
	if _, err := rankByLowestCost(items, map[int]int64{31: now + 1000, 32: now + 1000, 33: now + 1000}, now, nil, cost); !errors.Is(err, ErrNoEligibleMember) {
		t.Fatalf("all-cooling err = %v, want ErrNoEligibleMember", err)
	}
}

// TestUnitPriceFromPrice 单位成本折算: nil（未标价）与 <=0（价表 0 价）都算无数据。
func TestUnitPriceFromPrice(t *testing.T) {
	if _, ok := unitPriceFromPrice(nil); ok {
		t.Fatalf("nil price must be treated as no data")
	}
	if _, ok := unitPriceFromPrice(&model.LLMPrice{}); ok {
		t.Fatalf("zero price must be treated as no data")
	}
	if _, ok := unitPriceFromPrice(&model.LLMPrice{Input: -1, Output: 0.5}); ok {
		t.Fatalf("negative sum must be treated as no data")
	}
	got, ok := unitPriceFromPrice(&model.LLMPrice{Input: 0.15, Output: 0.6})
	if !ok || got != 0.75 {
		t.Fatalf("unit price = %v ok=%v, want 0.75 true", got, ok)
	}
}

// TestPickGroupItemByModeLowestCostBeatsPriority 走完整选路链路: lowest_cost 下便宜但优先级靠后的成员当选,
// 同一批成员在 failover(flag 关) 下仍按 priority 首选 —— 证明模式的显式选择真的改变了顺序。
func TestPickGroupItemByModeLowestCostBeatsPriority(t *testing.T) {
	gid := 9310
	items := []model.GroupItem{
		strategyItem(gid, 101, 1, "pricey"), // priority 最小 = 原路径首选
		strategyItem(gid, 102, 2, "cheap"),
	}
	cost := strategyCost(map[string]*model.LLMPrice{"pricey": llmPrice(1.0, 2.0), "cheap": llmPrice(0.15, 0.6)})
	cfg := model.DefaultGroupRelayConfig()

	legacyGroup := model.Group{ID: gid, Name: "cost-legacy", Mode: model.GroupModeFailover, Items: items, RelayConfig: cfg}
	if got := pickGroupItemByMode(legacyGroup, cost, false); got.ID != 101 {
		t.Fatalf("failover+flag off picked %d, want 101 (priority order unchanged)", got.ID)
	}

	costGroup := model.Group{ID: gid, Name: "cost", Mode: model.GroupModeLowestCost, Items: items, RelayConfig: cfg}
	if got := pickGroupItemByMode(costGroup, cost, false); got.ID != 102 {
		t.Fatalf("lowest_cost picked %d, want 102 (cheaper member wins despite lower priority)", got.ID)
	}
	// 加权轮询开关打开也不影响 lowest_cost 的定序口径（模式优先于全局开关）。
	if got := pickGroupItemByMode(costGroup, cost, true); got.ID != 102 {
		t.Fatalf("lowest_cost with balance flag on picked %d, want 102", got.ID)
	}
}

// TestPickGroupItemByModeKeepsManualPath manual 恒按 ActiveItemID 选成员, 策略接线不改变手动语义。
func TestPickGroupItemByModeKeepsManualPath(t *testing.T) {
	gid := 9311
	items := []model.GroupItem{
		strategyItem(gid, 111, 1, "pricey"),
		strategyItem(gid, 112, 2, "cheap"),
	}
	group := model.Group{ID: gid, Name: "manual", Mode: model.GroupModeManual, ActiveItemID: 111, Items: items,
		RelayConfig: model.DefaultGroupRelayConfig()}
	cost := strategyCost(map[string]*model.LLMPrice{"pricey": llmPrice(1.0, 2.0), "cheap": llmPrice(0.15, 0.6)})

	if got := pickGroupItemByMode(group, cost, false); got.ID != 111 {
		t.Fatalf("manual picked %d, want the active item 111", got.ID)
	}
	if got := pickGroupItemByMode(group, cost, true); got.ID != 111 {
		t.Fatalf("manual with balance flag on picked %d, want 111", got.ID)
	}
}
