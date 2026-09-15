package relay

import (
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 加权综合选路（R-weight-001 第一阶段）。
//
// 现有各模式各自只看一个维度（成本 / 质量 / 延迟 / 在途 / 近期消耗）。加权综合把它们归一化后按权重求和，
// 让「什么更重要」成为可调的设置项（route_weight_*），而不是写死在代码里。
//
// 两条口径（与仓库其它策略一致）：
//  1. 某维度**没有数据**的成员按中性分 0.5 参与, 不惩罚也不奖励 —— 新成员不会被已有指标永久压住,
//     试出真相后它的分数自然变化（与 quality_first 的中性先验、lowest_latency 的 0ms 乐观同一哲学）。
//  2. 排序并列时按 priority 再按 ID, 保证同输入同输出（可测）。
//
// 归一化在**本次候选集合内**做: 取每个维度的最小/最大值做线性映射, 全部相等时该维度记 0.5（等于弃权）。
// 好处是权重可以跨渠道比较（不受量纲影响）, 代价是候选集合变化时相对分随之变化 —— 这是刻意的,
// 因为选路本来就是"在当下这批成员里挑最好的"。

// 默认权重（保守版, 全部可用设置项覆盖）。用户若另有主张, 改设置即可, 不需要改代码。
const (
	defaultRouteWeightCost    = 30 // 成本: 价表 input+output
	defaultRouteWeightQuality = 30 // 质量: 成员近期成功率
	defaultRouteWeightLatency = 15 // 延迟: 成员最近一次尝试耗时
	defaultRouteWeightBusy    = 15 // 在途: 该成员此刻并发数
	defaultRouteWeightLoad    = 10 // 近期消耗: 60s 窗口内的 token/请求量

	// 计费类维度（R-weight-001 第二阶段）: 价表只反映模型基准价, 这四项补上"实际有多贵"。
	defaultRouteWeightMultiplier = 15 // 倍率: 渠道计价倍率, 越低越好
	defaultRouteWeightPerCall    = 10 // 按次单价: 每请求成本, 越低越好
	defaultRouteWeightBalance    = 15 // 余额: 最近一次扫描的剩余额度, 越多越好
	defaultRouteWeightMonthly    = 10 // 包月余量: 剩余比例, 越多越好
)

type weightSettings struct {
	cost       int
	quality    int
	latency    int
	busy       int
	load       int
	multiplier int
	perCall    int
	balance    int
	monthly    int
	// monthlyAction 决定包月额度用尽时的处置: "demote"（降权, 默认, 还能被选到）或 "exclude"（剔除）。
	// 这是用户点名的取舍点之一, 所以做成设置项而不是写死。
	monthlyAction string
}

func (settings weightSettings) total() int {
	return settings.cost + settings.quality + settings.latency + settings.busy + settings.load +
		settings.multiplier + settings.perCall + settings.balance + settings.monthly
}

// weightSettingsOf 读取权重设置; 缺省或非法值回落到上面的保守默认值, 并夹到 0..100。
func weightSettingsOf() weightSettings {
	read := func(key model.SettingKey, fallback int) int {
		value, err := op.SettingGetInt(key)
		if err != nil || value < 0 {
			value = fallback
		}
		if value > 100 {
			value = 100
		}
		return value
	}
	action, err := op.SettingGetString(model.SettingKeyRouteMonthlyAction)
	if err != nil || (action != "demote" && action != "exclude") {
		action = "demote" // 设置缺失/非法一律按"降权不剔除"（保守: 不悄悄让成员彻底不可用）
	}
	return weightSettings{
		cost:          read(model.SettingKeyRouteWeightCost, defaultRouteWeightCost),
		quality:       read(model.SettingKeyRouteWeightQuality, defaultRouteWeightQuality),
		latency:       read(model.SettingKeyRouteWeightLatency, defaultRouteWeightLatency),
		busy:          read(model.SettingKeyRouteWeightBusy, defaultRouteWeightBusy),
		load:          read(model.SettingKeyRouteWeightLoad, defaultRouteWeightLoad),
		multiplier:    read(model.SettingKeyRouteWeightMultiplier, defaultRouteWeightMultiplier),
		perCall:       read(model.SettingKeyRouteWeightPerCall, defaultRouteWeightPerCall),
		balance:       read(model.SettingKeyRouteWeightBalance, defaultRouteWeightBalance),
		monthly:       read(model.SettingKeyRouteWeightMonthly, defaultRouteWeightMonthly),
		monthlyAction: action,
	}
}

// weightedSample 是一条成员在五个维度上的原始取值（缺数据用 ok=false 表示）。
type weightedSample struct {
	item    model.GroupItem
	cost    float64
	costOK  bool
	quality float64
	qualOK  bool
	latency float64
	lateOK  bool
	busy    float64
	busyOK  bool
	load    float64
	loadOK  bool

	// 计费类维度（第二阶段）。
	multiplier float64
	multiOK    bool
	perCall    float64
	perCallOK  bool
	balance    float64
	balanceOK  bool
	// monthlyRatio 是包月剩余比例（0..1）; monthlyKnown=false 表示该渠道没填包月额度。
	monthlyRatio float64
	monthlyKnown bool
	// monthlyExhausted 表示包月额度已用尽（比例 <=0）, 由 monthlyAction 决定降权还是剔除。
	monthlyExhausted bool
}

// normalizedScore 把五维原始值归一化成 [0,1] 的加权分（越大越好）。
func (settings weightSettings) normalizedScore(samples []weightedSample, index int) float64 {
	total := settings.total()
	if total == 0 {
		return 0.5 // 权重全 0: 全员同分, 退化为 priority 顺序（由外层排序保证）
	}
	myself := samples[index]

	// lowerBetter 处理「越小越好」的维度: 成本 / 延迟 / 在途 / 近期消耗。
	//
	// 缺数据的成员按**乐观先验**处理（记最好值）: 与 lowest_latency 的「无耗时按 0ms 参与」
	// 和 quality_first 的「无样本按中性 1.0」同一哲学 —— 没试过的成员先给它一次机会, 试出真相后分数自然变化。
	// 若**全候选都没有该维度数据**, 该维度对所有人同分（0.5, 等于弃权）, 不影响排序。
	lowerBetter := func(value float64, ok bool, pick func(weightedSample) (float64, bool)) float64 {
		minValue, maxValue := 0.0, 0.0
		seen := false
		for _, sample := range samples {
			other, otherOK := pick(sample)
			if !otherOK {
				continue
			}
			if !seen {
				minValue, maxValue, seen = other, other, true
				continue
			}
			if other < minValue {
				minValue = other
			}
			if other > maxValue {
				maxValue = other
			}
		}
		if !seen {
			return 0.5
		}
		if !ok {
			return 1.0 // 无数据: 乐观先验, 先当成最好的
		}
		if maxValue <= minValue {
			return 0.5 // 有数据但全相等: 该维度弃权
		}
		return (maxValue - value) / (maxValue - minValue)
	}
	// higherBetter: 质量越高越好（缺数据同样按乐观先验记最好值）。
	higherBetter := func(value float64, ok bool, pick func(weightedSample) (float64, bool)) float64 {
		minValue, maxValue := 0.0, 0.0
		seen := false
		for _, sample := range samples {
			other, otherOK := pick(sample)
			if !otherOK {
				continue
			}
			if !seen {
				minValue, maxValue, seen = other, other, true
				continue
			}
			if other < minValue {
				minValue = other
			}
			if other > maxValue {
				maxValue = other
			}
		}
		if !seen {
			return 0.5
		}
		if !ok {
			return 1.0
		}
		if maxValue <= minValue {
			return 0.5
		}
		return (value - minValue) / (maxValue - minValue)
	}

	score := 0.0
	score += float64(settings.cost) * lowerBetter(myself.cost, myself.costOK,
		func(s weightedSample) (float64, bool) { return s.cost, s.costOK })
	// 倍率与按次单价都是"越低越好", 与成本同向但来源不同（成本来自价表, 倍率来自站点, 按次来自计费方式）。
	score += float64(settings.multiplier) * lowerBetter(myself.multiplier, myself.multiOK,
		func(s weightedSample) (float64, bool) { return s.multiplier, s.multiOK })
	score += float64(settings.perCall) * lowerBetter(myself.perCall, myself.perCallOK,
		func(s weightedSample) (float64, bool) { return s.perCall, s.perCallOK })
	// 余额越多越好; 包月余量比例越多越好（用尽时该维度记 0 分 —— 降权, 但不至于选不到）。
	score += float64(settings.balance) * higherBetter(myself.balance, myself.balanceOK,
		func(s weightedSample) (float64, bool) { return s.balance, s.balanceOK })
	score += float64(settings.monthly) * higherBetter(myself.monthlyRatio, myself.monthlyKnown,
		func(s weightedSample) (float64, bool) { return s.monthlyRatio, s.monthlyKnown })
	score += float64(settings.quality) * higherBetter(myself.quality, myself.qualOK,
		func(s weightedSample) (float64, bool) { return s.quality, s.qualOK })
	score += float64(settings.latency) * lowerBetter(myself.latency, myself.lateOK,
		func(s weightedSample) (float64, bool) { return s.latency, s.lateOK })
	score += float64(settings.busy) * lowerBetter(myself.busy, myself.busyOK,
		func(s weightedSample) (float64, bool) { return s.busy, s.busyOK })
	score += float64(settings.load) * lowerBetter(myself.load, myself.loadOK,
		func(s weightedSample) (float64, bool) { return s.load, s.loadOK })
	return score / float64(total)
}

// rankByWeighted 按加权综合分定序（分高在前; 并列按 priority、再按 ID; 冷却成员压队尾）。
func rankByWeighted(items []model.GroupItem, cooldowns map[int]int64, nowMs int64,
	zeroBalanceGrantIDs map[int]bool, deps routeDeps, settings weightSettings) ([]model.GroupItem, error) {
	eligible, cooling, err := partitionCandidates(items, cooldowns, nowMs, zeroBalanceGrantIDs)
	if err != nil {
		return nil, err
	}

	samples := make([]weightedSample, 0, len(eligible))
	for _, item := range eligible {
		sample := weightedSample{item: item}
		if deps.cost != nil {
			if price, ok := deps.cost(item); ok {
				sample.cost, sample.costOK = price, true
			}
		}
		if deps.quality != nil {
			if rate, ok := deps.quality(item.ID); ok {
				sample.quality, sample.qualOK = rate, true
			}
		}
		if deps.latency != nil {
			if ms, ok := deps.latency(item.ID); ok {
				sample.latency, sample.lateOK = float64(ms), true
			}
		}
		if deps.busy != nil {
			sample.busy, sample.busyOK = float64(deps.busy(item.ID)), true
		}
		if deps.billing != nil {
			if billing, ok := deps.billing(item); ok {
				if billing.Multiplier > 0 {
					sample.multiplier, sample.multiOK = billing.Multiplier, true
				}
				if billing.PerCallPrice > 0 {
					sample.perCall, sample.perCallOK = billing.PerCallPrice, true
				}
				if billing.BalanceKnown {
					sample.balance, sample.balanceOK = billing.Balance, true
				}
				if billing.MonthlyQuota > 0 {
					remaining := billing.MonthlyQuota - billing.MonthlyUsed
					if remaining < 0 {
						remaining = 0
					}
					sample.monthlyRatio = remaining / billing.MonthlyQuota
					sample.monthlyKnown = true
					sample.monthlyExhausted = remaining <= 0
				}
			}
		}
		if deps.load != nil {
			requests, tokens, ok := deps.load(item.ID)
			if ok {
				// 以 token 为主计量; 只有失败尝试没有 token 时退化为按请求数计（失败同样占上游配额）。
				if tokens <= 0 {
					tokens = requests
				}
				sample.load, sample.loadOK = float64(tokens), true
			}
		}
		samples = append(samples, sample)
	}

	if settings.monthlyAction == "exclude" && settings.monthly > 0 {
		kept := samples[:0]
		for _, sample := range samples {
			if sample.monthlyKnown && sample.monthlyExhausted {
				continue // 用户选择"包月用尽即剔除"
			}
			kept = append(kept, sample)
		}
		samples = kept
		if len(samples) == 0 {
			// 全员包月用尽且要求剔除: 按无人可用处理（外层等冷却/下一轮重试）, 不要悄悄破坏剔除语义。
			return nil, ErrNoEligibleMember
		}
	}

	type scored struct {
		item  model.GroupItem
		score float64
	}
	ranked := make([]scored, 0, len(samples))
	for index, sample := range samples {
		ranked = append(ranked, scored{item: sample.item, score: settings.normalizedScore(samples, index)})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		if ranked[i].item.Priority != ranked[j].item.Priority {
			return ranked[i].item.Priority < ranked[j].item.Priority
		}
		return ranked[i].item.ID < ranked[j].item.ID
	})

	ordered := make([]model.GroupItem, 0, len(ranked)+len(cooling))
	for _, entry := range ranked {
		ordered = append(ordered, entry.item)
	}
	return append(ordered, cooling...), nil
}

// pickGroupItemWeighted 是 weighted 模式的选路入口（与其它策略同一模板）。
func pickGroupItemWeighted(group model.Group, deps routeDeps) model.GroupItem {
	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	cooldowns := cloneCooldowns(route.Cooldowns)
	routeMu.Unlock()

	ranked, err := rankByWeighted(group.Items, cooldowns, time.Now().UnixMilli(), nil, deps, weightSettingsOf())
	if err != nil || len(ranked) == 0 {
		return model.GroupItem{}
	}
	return pickGroupItem(group.WithItems(ranked))
}
