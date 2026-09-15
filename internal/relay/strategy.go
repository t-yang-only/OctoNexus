package relay

import (
	"maps"
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/price"
)

// R-route-001 第一阶段：最低成本（lowest_cost）选路定序。
// 口径（与 balance.go 的加权轮询共用同一套过滤/冷却语义，只换排序键）：
//  1. 只给"候选定序"，不另起路由状态：亲和/冷却/探测/成员尝试上限仍归 pickGroupItem 既有链路，
//     本文件只决定"本轮先试谁"；
//  2. 过滤口径与 rankCandidates 完全一致（partitionCandidates）：渠道或凭据停用、剩余额度归零的成员
//     剔除，冷却中成员压到队尾（到期即回），可尝试成员为空时返回 ErrNoEligibleMember，
//     由调用方按"无目标"等待；
//  3. 排序：单位成本升序 → priority 升序 → ID 升序（稳定排序，同输入同输出）；
//  4. 无价格数据的成员（价表查不到，或价表条目为 0——本轮导入的 137 条价格里 48 条是 0）
//     一律视作"无数据"沉底，与 balance.go 延迟排序的"无数据沉底"口径一致：否则 0 价成员会永远当选，
//     把真实流量全推到没标价的成员上；
//  5. 单位成本口径 = 价表 Input + Output（每百万 token 单价之和）：即输入输出 1:1 混合时的两倍均价，
//     与单项单价同序，避免为"按真实配比加权"引入用量统计依赖。
//
// 不做（设计稿显式排除）：complexity/quality/adaptive 分级、tag 体系；
// 失败率/延迟入权重（lowest_latency、least_busy）留待后续迭代。

// CostProvider 返回成员的单位成本（每百万 token 的输入+输出单价之和）；
// ok=false 表示无数据，该成员沉底。
type CostProvider func(item model.GroupItem) (unitPrice float64, ok bool)

// unitPriceFromPrice 把价表条目折算成单位成本；nil（模型未标价）与 <=0（价表 0 价）都按无数据处理。
func unitPriceFromPrice(p *model.LLMPrice) (float64, bool) {
	if p == nil {
		return 0, false
	}
	total := p.Input + p.Output
	if total <= 0 {
		return 0, false
	}
	return total, true
}

// memberUnitPrice 是生产用的取价实现：按成员的上游模型名查价表。
func memberUnitPrice(item model.GroupItem) (float64, bool) {
	return unitPriceFromPrice(price.GetLLMPrice(item.ModelName))
}

// rankByLowestCost 对候选做最低成本定序：首个即"当前最省的成员"。
func rankByLowestCost(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalanceGrantIDs map[int]bool, cost CostProvider) ([]model.GroupItem, error) {
	eligible, cooling, err := partitionCandidates(items, cooldowns, nowMs, zeroBalanceGrantIDs)
	if err != nil {
		return nil, err
	}
	ranked := append([]model.GroupItem(nil), eligible...)
	sort.SliceStable(ranked, func(i, j int) bool {
		pi, oki := providerUnitPrice(cost, ranked[i])
		pj, okj := providerUnitPrice(cost, ranked[j])
		if oki != okj {
			return oki // 有价格数据的在前
		}
		if oki && pi != pj {
			return pi < pj // 便宜的在前
		}
		if ranked[i].Priority != ranked[j].Priority {
			return ranked[i].Priority < ranked[j].Priority
		}
		return ranked[i].ID < ranked[j].ID
	})
	return append(ranked, cooling...), nil
}

// providerUnitPrice 容错 nil provider（未接线或单测不关心价格时按"无数据"处理，自然退回 priority 定序）。
func providerUnitPrice(cost CostProvider, item model.GroupItem) (float64, bool) {
	if cost == nil {
		return 0, false
	}
	return cost(item)
}

// pickGroupItemLowestCost 在最低成本定序下选出本轮目标：先按 rankByLowestCost 定序，
// 再用重排后的成员表走原选路函数，首选即当前最省的可用成员。
// 定序失败（候选全部被剔除）时回退零值，调用方按无目标等待，与加权轮询路径语义一致。
func pickGroupItemLowestCost(group model.Group, cost CostProvider) model.GroupItem {
	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	cooldowns := maps.Clone(route.Cooldowns)
	routeMu.Unlock()

	ranked, err := rankByLowestCost(group.Items, cooldowns, time.Now().UnixMilli(), nil, cost)
	if err != nil || len(ranked) == 0 {
		return model.GroupItem{}
	}
	return pickGroupItem(group.WithItems(ranked))
}
