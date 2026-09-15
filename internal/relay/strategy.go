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

// routeDeps 是选路需要的外部数据来源（成本 / 质量 / 延迟 / 在途 / 近期负载）。分组模式决定用其中哪几个：
// 热路径传生产实现，单测传 stub；字段为 nil 表示该维度不参与（各种模式都有明确的"无数据"口径）。
type routeDeps struct {
	cost    CostProvider
	quality MemberQualityProvider
	latency LatencyProvider
	busy    BusyProvider
	load    LoadProvider
}

// NM-DS-004 迭代：质量优先（quality_first）选路定序。
// 与最低成本共用同一套过滤/冷却口径（partitionCandidates），只换排序键：
// 最近窗口成功率降序 → priority 升序 → ID 升序。
// "无样本"按中性先验 1.0 参与排序（乐观口径）：新成员照样会被尝试，失败过的成员自然沉到
// 已验证成员之后；坏成员另有既有冷却/探测链路兜底，不靠这里做惩罚。

// 中性先验：无样本（含未接线的 provider）视作与"全成功"同档，靠 priority 定序。
const memberQualityNeutralPrior = 1.0

// rankByQuality 对候选做质量优先定序：首个即"最近表现最好的可用成员"。
func rankByQuality(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalanceGrantIDs map[int]bool, quality MemberQualityProvider) ([]model.GroupItem, error) {
	eligible, cooling, err := partitionCandidates(items, cooldowns, nowMs, zeroBalanceGrantIDs)
	if err != nil {
		return nil, err
	}
	ranked := append([]model.GroupItem(nil), eligible...)
	sort.SliceStable(ranked, func(i, j int) bool {
		si := memberQualityScore(quality, ranked[i])
		sj := memberQualityScore(quality, ranked[j])
		if si != sj {
			return si > sj // 成功率高的在前
		}
		if ranked[i].Priority != ranked[j].Priority {
			return ranked[i].Priority < ranked[j].Priority
		}
		return ranked[i].ID < ranked[j].ID
	})
	return append(ranked, cooling...), nil
}

// memberQualityScore 取成员成功率；provider 未接线或无样本时返回中性先验。
func memberQualityScore(quality MemberQualityProvider, item model.GroupItem) float64 {
	if quality == nil {
		return memberQualityNeutralPrior
	}
	rate, ok := quality(item.ID)
	if !ok {
		return memberQualityNeutralPrior
	}
	return rate
}

// pickGroupItemQualityFirst 在质量优先定序下选出本轮目标：只改"本轮先试谁"的顺序,
// 亲和/冷却/探测/上限语义仍归 pickGroupItem 既有链路；定序失败时返回零值由调用方等待。
func pickGroupItemQualityFirst(group model.Group, quality MemberQualityProvider) model.GroupItem {
	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	cooldowns := maps.Clone(route.Cooldowns)
	routeMu.Unlock()

	ranked, err := rankByQuality(group.Items, cooldowns, time.Now().UnixMilli(), nil, quality)
	if err != nil || len(ranked) == 0 {
		return model.GroupItem{}
	}
	return pickGroupItem(group.WithItems(ranked))
}

// NM-DS-006 迭代：最低延迟（lowest_latency）选路定序。
// 与最低成本 / 质量优先共用同一套过滤与冷却口径（partitionCandidates），只换排序键：
// 最近一次尝试耗时升序 → priority 升序 → ID 升序。
// "没有耗时数据"的成员按 0ms 参与排序（乐观口径，与 quality_first 的中性先验同哲学）：
// 未知者先当成快的试一次、试出真相后自然沉底；否则新成员会被已知的慢成员永久压住。

// rankByLatency 对候选做最低延迟定序：首个即"最近最快的可用成员"。
func rankByLatency(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalanceGrantIDs map[int]bool, latency LatencyProvider) ([]model.GroupItem, error) {
	eligible, cooling, err := partitionCandidates(items, cooldowns, nowMs, zeroBalanceGrantIDs)
	if err != nil {
		return nil, err
	}
	ranked := append([]model.GroupItem(nil), eligible...)
	sort.SliceStable(ranked, func(i, j int) bool {
		li := memberLatencyScore(latency, ranked[i])
		lj := memberLatencyScore(latency, ranked[j])
		if li != lj {
			return li < lj // 耗时短的在前
		}
		if ranked[i].Priority != ranked[j].Priority {
			return ranked[i].Priority < ranked[j].Priority
		}
		return ranked[i].ID < ranked[j].ID
	})
	return append(ranked, cooling...), nil
}

// memberLatencyScore 取成员最近耗时；provider 未接线或无数据时按 0 参与排序（乐观先验）。
func memberLatencyScore(latency LatencyProvider, item model.GroupItem) int64 {
	if latency == nil {
		return 0
	}
	value, ok := latency(item.ID)
	if !ok || value < 0 {
		return 0
	}
	return value
}

// pickGroupItemLowestLatency 在最低延迟定序下选出本轮目标：只改"本轮先试谁"的顺序,
// 亲和/冷却/探测/上限语义仍归 pickGroupItem 既有链路；定序失败时返回零值由调用方等待。
func pickGroupItemLowestLatency(group model.Group, latency LatencyProvider) model.GroupItem {
	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	cooldowns := maps.Clone(route.Cooldowns)
	routeMu.Unlock()

	ranked, err := rankByLatency(group.Items, cooldowns, time.Now().UnixMilli(), nil, latency)
	if err != nil || len(ranked) == 0 {
		return model.GroupItem{}
	}
	return pickGroupItem(group.WithItems(ranked))
}

// NM-DS-006 迭代：最空闲（least_busy）选路定序。
// 与其它策略共用同一套过滤与冷却口径（partitionCandidates），只换排序键：
// 在途请求数升序 → priority 升序 → ID 升序。
// 在途 = 该成员"正在被等待响应"的请求数（见 state.go 的 memberBusyCount）：并发打进来时，
// 已有一个请求压在某个成员上就不再叠加第二个，把并发摊到空闲成员上；
// provider 未接线（nil）时全部按 0 参与，于是退化为 priority 定序。

// rankByBusy 对候选做最空闲定序：首个即"当前最闲的可用成员"。
func rankByBusy(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalanceGrantIDs map[int]bool, busy BusyProvider) ([]model.GroupItem, error) {
	eligible, cooling, err := partitionCandidates(items, cooldowns, nowMs, zeroBalanceGrantIDs)
	if err != nil {
		return nil, err
	}
	ranked := append([]model.GroupItem(nil), eligible...)
	sort.SliceStable(ranked, func(i, j int) bool {
		bi := memberBusyScore(busy, ranked[i])
		bj := memberBusyScore(busy, ranked[j])
		if bi != bj {
			return bi < bj // 在途少的在前
		}
		if ranked[i].Priority != ranked[j].Priority {
			return ranked[i].Priority < ranked[j].Priority
		}
		return ranked[i].ID < ranked[j].ID
	})
	return append(ranked, cooling...), nil
}

// memberBusyScore 取成员在途数；provider 未接线时按 0（全部平手，退化为 priority 定序）。
func memberBusyScore(busy BusyProvider, item model.GroupItem) int {
	if busy == nil {
		return 0
	}
	count := busy(item.ID)
	if count < 0 {
		return 0
	}
	return count
}

// pickGroupItemLeastBusy 在最空闲定序下选出本轮目标；只改"本轮先试谁"的顺序,
// 亲和/冷却/探测/上限语义仍归 pickGroupItem 既有链路。
func pickGroupItemLeastBusy(group model.Group, busy BusyProvider) model.GroupItem {
	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	cooldowns := maps.Clone(route.Cooldowns)
	routeMu.Unlock()

	ranked, err := rankByBusy(group.Items, cooldowns, time.Now().UnixMilli(), nil, busy)
	if err != nil || len(ranked) == 0 {
		return model.GroupItem{}
	}
	return pickGroupItem(group.WithItems(ranked))
}

// NM-DS-014 迭代：按最近一分钟的消耗（lowest_tpm_rpm）选路定序。
// 与其它策略共用同一套过滤与冷却口径（partitionCandidates），只换排序键：
// 窗口内 token 数升序 → 请求数升序 → priority 升序 → ID 升序。
//
// 两处与 least_busy 的关键区别（避免用户以为它们一样）：
//   - least_busy 看的是**此刻在途**（进程内活动请求数），窗口为 0；
//   - lowest_tpm_rpm 看的是**最近 60 秒已经消耗掉的量**（请求数与 token 数），
//     所以一个刚把大请求做完的成员会在这 60 秒内被让开，哪怕它此刻没有在途请求。
//
// "无记录"的成员按 0 消耗参与排序（乐观口径，与 quality_first 的中性先验、lowest_latency 的 0ms 同哲学）：
// 未知者先当成空闲的试一次，试出消耗后自然被让开。
// 需要说明的是：上游真实的 RPM/TPM 限额我们并不知道，这里排的是"我们自己的近期消耗"，
// 效果是摊平负载、降低撞限流的概率，而不是严格意义上的"剩余额度最多者"。

// rankByRecentLoad 对候选做近期负载定序：首个即"最近一分钟消耗最少的可用成员"。
func rankByRecentLoad(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalanceGrantIDs map[int]bool, load LoadProvider) ([]model.GroupItem, error) {
	eligible, cooling, err := partitionCandidates(items, cooldowns, nowMs, zeroBalanceGrantIDs)
	if err != nil {
		return nil, err
	}
	ranked := append([]model.GroupItem(nil), eligible...)
	sort.SliceStable(ranked, func(i, j int) bool {
		ri, ti := memberLoadScore(load, ranked[i])
		rj, tj := memberLoadScore(load, ranked[j])
		if ti != tj {
			return ti < tj // token 消耗少的在前（TPM）
		}
		if ri != rj {
			return ri < rj // 请求数少的在前（RPM）
		}
		if ranked[i].Priority != ranked[j].Priority {
			return ranked[i].Priority < ranked[j].Priority
		}
		return ranked[i].ID < ranked[j].ID
	})
	return append(ranked, cooling...), nil
}

// memberLoadScore 取成员窗口内的消耗（请求数, token 数）；provider 未接线或无记录时都按 0（乐观先验）。
func memberLoadScore(load LoadProvider, item model.GroupItem) (int, int) {
	if load == nil {
		return 0, 0
	}
	requests, tokens, ok := load(item.ID)
	if !ok {
		return 0, 0
	}
	if requests < 0 {
		requests = 0
	}
	if tokens < 0 {
		tokens = 0
	}
	return requests, tokens
}

// pickGroupItemLowestTpmRpm 在近期负载定序下选出本轮目标；只改"本轮先试谁"的顺序,
// 亲和/冷却/探测/上限语义仍归 pickGroupItem 既有链路；定序失败时返回零值由调用方等待。
func pickGroupItemLowestTpmRpm(group model.Group, load LoadProvider) model.GroupItem {
	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	cooldowns := maps.Clone(route.Cooldowns)
	routeMu.Unlock()

	ranked, err := rankByRecentLoad(group.Items, cooldowns, time.Now().UnixMilli(), nil, load)
	if err != nil || len(ranked) == 0 {
		return model.GroupItem{}
	}
	return pickGroupItem(group.WithItems(ranked))
}
