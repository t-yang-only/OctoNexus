package relay

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 额度分压选路（mode=allocate, T-allocate-001）。
//
// 用户口径（逐字）："每个 key 可能只有一点钱，所以要进行分压，按照剩余请求数，api 进行最优分配"。
//
// 与既有各模式的根本区别：其它模式都是"挑当下最好的那一个"——贪心，流量会全部压在同一个成员上，
// 直到它被榨干、被限流或被冷却才换人；成员之间钱多钱少、还能干多少次活都不影响"谁先被用完"。
// 本模式是"按各自还能干多少活，按比例把请求分出去"：谁剩下的请求数多，谁就多承担一点流量，
// 但每个还有余量的成员都会持续分到流量（权重下限 1），于是几条小余额的 key 可以一起顶上一个大的。
//
// 四条口径：
//  1. **剩余请求数**（estimateRemainingRequests）：能算就算，算不出来就说不知道——
//     包月额度（quota-used，单位就是次数）优先；否则用余额折算：剩余货币 = 额度点 / balance_points_per_unit，
//     除以"这一次请求要花多少钱"（按次单价优先；否则价表单价 × 本次请求估算 token × 倍率）。
//     两个来源都没有 → unknown（**不做任何惩罚**, 按已知成员权重的中位数参与, 与全仓"未知不惩罚"一致）。
//  2. **权重**：以候选里剩余最多的成员为满刻度（allocateMaxWeight=100），其余按比例，四舍五入后夹到 [1,100]。
//     剩余请求数已知但不足一次（< min_requests，默认 1）→ 剔除：钱花完了就别再打它了。
//  3. **健康折扣**：成功率低、最近一次耗时超过慢阈值、或刚被上游限流过（429）的成员，权重按
//     route_allocate_health_weight（0..100，默认 40）打折，最低仍保 1 —— 削权而不是剔除，
//     剔除是冷却的职责（否则一个慢成员会被永久排除，无从恢复）。
//  4. **分配**：平滑加权轮询（smooth weighted round-robin）的单步累加器挂在分组 RouteState 上，
//     每步只花 O(成员数)。之所以不用"按 round 重放整周期"的纯函数版：权重随余额与健康一直在变，
//     累加器能自然跟着变，而重放版每轮都要按当前权重从零重算，长期分配比例会被抹平。
//
// 与既有机制的关系（改这块时的交叉点清单）：候选过滤/冷却/探测/亲和仍归 pickGroupItem 既有链路，
// 本文件只决定"本轮先试谁"；竞速候选走 rankedAllocateCandidates（与选路同一套权重与定序）；
// smart 档位与加权轮询开关都不参与本模式（模式自身即是显式选择）。

// 分压相关常量。
const (
	allocateMaxWeight     = 100 // 单成员权重满刻度：候选里剩余请求数最多者拿它。
	allocateMinWeight     = 1   // 权重下限：还有余量的成员不会被饿死。
	allocateSourceMonth   = "monthly"
	allocateSourceBalance = "balance"
	allocateTokensPerM    = 1_000_000.0
)

// allocateSettings 是分压所需的设置项（全部有缺省值，缺省即"保守但不失效"）。
type allocateSettings struct {
	tokens        int           // 请求没给出估算时的单次 token 兜底（route_allocate_estimate_tokens）
	healthWeight  int           // 健康折扣强度 0..100（route_allocate_health_weight）
	slowMs        int64         // 慢成员阈值毫秒, 0=不按延迟打折（route_allocate_slow_latency_ms）
	minRequests   float64       // 剩余请求数低于它即剔除（route_allocate_min_requests）
	rpmLimit      int           // 成员每分钟请求上限, 0=不限（route_member_rpm_limit）
	tpmLimit      int           // 成员每分钟 token 上限, 0=不限（route_member_tpm_limit）
	throttleCapMs int64         // 429 冷却上限（route_ratelimit_cooldown_max_seconds）
	speed         speedSettings // 速度维度（T-speed-001）: 首帧与吞吐的折扣强度与阈值
}

// 缺省值：与设置项的默认值表保持一致（设置缺失时用同一组数）。
const (
	defaultAllocateTokens      = 1000
	defaultAllocateHealthPct   = 40
	defaultAllocateSlowMs      = 0
	defaultAllocateMinRequests = 1
	defaultMemberRPMLimit      = 0
	defaultMemberTPMLimit      = 0
	defaultThrottleCapSeconds  = 300
)

// allocateSettingsOf 读取分压设置；缺省或越界一律回落到保守缺省（不让一个手滑的设置让选路失效）。
func allocateSettingsOf() allocateSettings {
	read := func(key model.SettingKey, fallback, minValue, maxValue int) int {
		value, err := op.SettingGetInt(key)
		if err != nil || value < minValue {
			value = fallback
		}
		if value > maxValue {
			value = maxValue
		}
		return value
	}
	return allocateSettings{
		tokens:        read(model.SettingKeyRouteAllocateTokens, defaultAllocateTokens, 1, 2_000_000),
		healthWeight:  read(model.SettingKeyRouteAllocateHealth, defaultAllocateHealthPct, 0, 100),
		slowMs:        int64(read(model.SettingKeyRouteAllocateSlowMs, defaultAllocateSlowMs, 0, 600_000)),
		minRequests:   float64(read(model.SettingKeyRouteAllocateMinReq, defaultAllocateMinRequests, 0, 1_000_000)),
		rpmLimit:      read(model.SettingKeyRouteMemberRPMLimit, defaultMemberRPMLimit, 0, 1_000_000),
		tpmLimit:      read(model.SettingKeyRouteMemberTPMLimit, defaultMemberTPMLimit, 0, 1_000_000_000),
		throttleCapMs: int64(read(model.SettingKeyRouteThrottleCapSecond, defaultThrottleCapSeconds, 0, 86_400)) * 1000,
		speed:         speedSettingsOf(),
	}
}

// allocateInput 是一次分压分配用到的请求侧事实。
type allocateInput struct {
	tokens        int     // 本次请求的估算输入 token（<=0 时用设置里的兜底值）
	pointsPerUnit float64 // 额度点 → 货币 的换算口径（与余额页同一口径）
	settings      allocateSettings
}

// allocateSample 是一个成员在本次分配里的事实与结论。
type allocateSample struct {
	item     model.GroupItem
	requests float64 // 估算的剩余请求数
	known    bool    // false 表示无从估算（按未知参与，不惩罚）
	source   string  // "monthly" / "balance" / ""
	limited  bool    // 本分钟已达自限流上限（RPM/TPM），本轮让开
	weight   int     // 最终权重（1..allocateMaxWeight）
}

// estimateRemainingRequests 估算一个成员"还能服务多少次请求"，返回 (次数, 是否已知, 来源)。
// 口径见文件头第 1 条；算不出来时 ok=false（调用方按未知处理，而不是当 0）。
func estimateRemainingRequests(billing model.ChannelBilling, unitPrice float64, unitPriceOK bool, in allocateInput) (float64, bool, string) {
	// 包月额度最直接：单位本来就是"次"。
	if billing.MonthlyQuota > 0 {
		remaining := billing.MonthlyQuota - billing.MonthlyUsed
		if remaining < 0 {
			remaining = 0
		}
		return remaining, true, allocateSourceMonth
	}
	if !billing.BalanceKnown {
		return 0, false, ""
	}
	// 余额是"额度点"，先按与余额页同一口径折成货币；归零的成员是明确的"不能再打了"。
	currency := model.ConvertBalancePoints(billing.Balance, in.pointsPerUnit)
	if currency <= 0 {
		return 0, true, allocateSourceBalance
	}
	// ① 按次计费：一次请求的价格是站点自己写明的。
	if billing.PerCallPrice > 0 {
		return currency / billing.PerCallPrice, true, allocateSourceBalance
	}
	// ② 按量计费：用价表单价 × 本次请求估算 token × 倍率折出一次请求的价钱。
	if unitPriceOK && unitPrice > 0 {
		tokens := in.tokens
		if tokens <= 0 {
			tokens = in.settings.tokens
		}
		if tokens <= 0 {
			return 0, false, ""
		}
		multiplier := billing.Multiplier
		if multiplier <= 0 {
			multiplier = 1
		}
		perRequest := unitPrice * multiplier * float64(tokens) / allocateTokensPerM
		if perRequest > 0 {
			return currency / perRequest, true, allocateSourceBalance
		}
	}
	// 余额知道、但"一次请求花多少"不知道（没价表也没按次单价）→ 按未知处理，不猜。
	return 0, false, ""
}

// allocateSampleOf 组装一个成员的分压事实：剩余请求数（估算）+ 自限流是否已到顶。
func allocateSampleOf(item model.GroupItem, deps routeDeps, in allocateInput) allocateSample {
	sample := allocateSample{item: item}
	if deps.billing != nil {
		if billing, ok := deps.billing(item); ok {
			var unitPrice float64
			var unitPriceOK bool
			if deps.cost != nil {
				unitPrice, unitPriceOK = deps.cost(item)
			}
			sample.requests, sample.known, sample.source = estimateRemainingRequests(billing, unitPrice, unitPriceOK, in)
		}
	}
	// 自限流：本分钟已经打到上限的成员先让开一轮。上游 429 比我们提前刹车贵得多
	// （一次 429 会连带整条重试链路），所以宁可自己先停一停。
	if deps.load != nil && (in.settings.rpmLimit > 0 || in.settings.tpmLimit > 0) {
		requests, tokens, ok := deps.load(item.ID)
		if ok {
			if in.settings.rpmLimit > 0 && requests >= in.settings.rpmLimit {
				sample.limited = true
			}
			if in.settings.tpmLimit > 0 && tokens >= in.settings.tpmLimit {
				sample.limited = true
			}
		}
	}
	return sample
}

// memberHealthFactor 返回 0..1 的健康折扣系数（1 = 不打折）。
// 三个信号取最差的一个：成功率、最近一次耗时是否超过慢阈值、是否刚被上游限流（等待期内直接打到底）。
// 折扣只体现在权重上，不做剔除：坏成员另有冷却/探测链路兜底，这里只让它在分压里少拿一点。
func memberHealthFactor(itemID int, deps routeDeps, settings allocateSettings, nowMs int64) float64 {
	penalty := 0.0
	if deps.quality != nil {
		if rate, ok := deps.quality(itemID); ok {
			if loss := 1 - rate; loss > penalty {
				penalty = loss
			}
		}
	}
	if settings.slowMs > 0 && deps.latency != nil {
		if ms, ok := deps.latency(itemID); ok && ms > settings.slowMs {
			over := float64(ms-settings.slowMs) / float64(settings.slowMs)
			if over > 1 {
				over = 1
			}
			if over > penalty {
				penalty = over
			}
		}
	}
	if _, throttled := memberThrottleState(itemID, nowMs); throttled {
		penalty = 1
	}
	factor := 1 - float64(settings.healthWeight)/100*penalty
	if factor < 0 {
		factor = 0
	}
	return factor
}

// clampAllocateWeight 把权重夹到 [allocateMinWeight, allocateMaxWeight]。
func clampAllocateWeight(weight int) int {
	if weight < allocateMinWeight {
		return allocateMinWeight
	}
	if weight > allocateMaxWeight {
		return allocateMaxWeight
	}
	return weight
}

// scaleAllocateWeight 把剩余请求数按刻度折成整数权重。
func scaleAllocateWeight(requests, unit float64) int {
	if unit <= 0 {
		return allocateMinWeight
	}
	return clampAllocateWeight(int(math.Round(requests / unit)))
}

// allocateWeights 把样本的剩余请求数与健康系数算成整数权重。
//
// 刻度取候选内最大剩余请求数，让最大者拿满 allocateMaxWeight；未知剩余数的成员取**已知权重的中位数**
// （不奖也不罚；全员未知时人人等权，于是退化成平均分压）——与全仓"未知按乐观先验参与"的口径一致，
// 区别只是这里用中位数而不是"最好": 分压里"最好"会让未知成员把所有人的流量都吞掉。
func allocateWeights(samples []allocateSample, factors []float64) []int {
	maxRequests := 0.0
	knownRequests := make([]float64, 0, len(samples))
	for _, sample := range samples {
		if !sample.known {
			continue
		}
		knownRequests = append(knownRequests, sample.requests)
		if sample.requests > maxRequests {
			maxRequests = sample.requests
		}
	}
	unit := maxRequests / float64(allocateMaxWeight)
	medianWeight := allocateMinWeight
	if len(knownRequests) > 0 {
		knownWeights := make([]int, 0, len(knownRequests))
		for _, requests := range knownRequests {
			knownWeights = append(knownWeights, scaleAllocateWeight(requests, unit))
		}
		sort.Ints(knownWeights)
		medianWeight = knownWeights[len(knownWeights)/2]
	}

	weights := make([]int, len(samples))
	for index, sample := range samples {
		weight := medianWeight
		if sample.known {
			weight = scaleAllocateWeight(sample.requests, unit)
		}
		factor := 1.0
		if index < len(factors) {
			factor = factors[index]
		}
		weights[index] = clampAllocateWeight(int(math.Round(float64(weight) * factor)))
	}
	return weights
}

// allocateStep 是平滑加权轮询的单步：每个成员累加自己的权重，取当前值最大者当选，
// 当选者减去权重总和（于是长期看每个成员被选中的次数正比于它的权重）。
//
// current 以成员行 ID 为键跨请求保留（挂在 RouteState 上）。与 balance.go 的 smoothWeightedOrder 的区别：
// 那个是纯函数版（按 round 重放整周期），本函数保留累加器——权重随余额、健康、限流一直在变，
// 累加器让分配自动跟着变，且每步只花 O(成员数)。同分时取成员行 ID 较小者，保证同输入同输出。
func allocateStep(current map[int]int, items []model.GroupItem, weights []int) int {
	total := 0
	for _, weight := range weights {
		total += weight
	}
	if total <= 0 || len(items) == 0 {
		return -1
	}
	best := 0
	for index, item := range items {
		current[item.ID] += weights[index]
		if current[item.ID] > current[items[best].ID] {
			best = index
			continue
		}
		// 同分：按成员行 ID 定序，保证同输入同输出。
		if current[item.ID] == current[items[best].ID] && item.ID < items[best].ID {
			best = index
		}
	}
	current[items[best].ID] -= total
	return best
}

// allocatePlanPlan 的"计划"一步：候选分区 → 组装样本 → 算权重。
// 冷却中的成员压到队尾（沿用既有语义），返回的 samples 与 weights 一一对应（权重同时写回 sample.weight）。
func allocatePlan(items []model.GroupItem, cooldowns map[int]int64, nowMs int64, zeroBalance map[int]bool,
	deps routeDeps, in allocateInput) (samples []allocateSample, factors []float64, cooling []model.GroupItem, err error) {
	eligible, cooling, err := partitionCandidates(items, cooldowns, nowMs, zeroBalance)
	if err != nil {
		return nil, nil, cooling, err
	}

	samples = make([]allocateSample, 0, len(eligible))
	factors = make([]float64, 0, len(eligible))
	for _, item := range eligible {
		sample := allocateSampleOf(item, deps, in)
		// 剩余请求数已知但不足一次：钱/次数已经花完了，剔除（未知不剔除）。
		if sample.known && sample.requests < in.settings.minRequests {
			continue
		}
		samples = append(samples, sample)
		// 折扣分两维（健康、速度）取**最差**的那一个，不叠加：
		// 叠加会让"又慢又不稳"的成员被乘两次而瞬间掉到底，也会让任何一维的调整都牵动另一维的口径。
		// 两维都只削权、不剔除（剔除是冷却/限流/余量用尽的职责）。
		healthFactor := memberHealthFactor(item.ID, deps, in.settings, nowMs)
		speedFactor := memberSpeedFactor(item.ID, in.settings.speed)
		factors = append(factors, math.Min(healthFactor, speedFactor))
	}
	if len(samples) == 0 {
		return nil, nil, cooling, ErrNoEligibleMember
	}

	// 自限流：有非受限成员时先让开已达上限的成员；全部达上限时照常使用
	// （宁可撞一下上游也不要让请求无人可用）。
	if in.settings.rpmLimit > 0 || in.settings.tpmLimit > 0 {
		freeSamples := make([]allocateSample, 0, len(samples))
		freeFactors := make([]float64, 0, len(factors))
		for index, sample := range samples {
			if sample.limited {
				continue
			}
			freeSamples = append(freeSamples, sample)
			freeFactors = append(freeFactors, factors[index])
		}
		if len(freeSamples) > 0 && len(freeSamples) < len(samples) {
			samples, factors = freeSamples, freeFactors
		}
	}

	weights := allocateWeights(samples, factors)
	for index := range samples {
		samples[index].weight = weights[index]
	}
	return samples, factors, cooling, nil
}

// orderAllocateSamples 把样本表按"本轮当选者优先、其余按权重降序"排成成员表。
// 之所以要给出完整顺序：故障转移要用它（当选者失败后按权重换下一个），竞速候选也用它。
// 并列按 priority、再按成员行 ID，保证同输入同输出。
func orderAllocateSamples(samples []allocateSample, winner int, cooling []model.GroupItem) []model.GroupItem {
	rest := make([]allocateSample, 0, len(samples))
	for index, sample := range samples {
		if index == winner {
			continue
		}
		rest = append(rest, sample)
	}
	sort.SliceStable(rest, func(i, j int) bool {
		if rest[i].weight != rest[j].weight {
			return rest[i].weight > rest[j].weight
		}
		if rest[i].item.Priority != rest[j].item.Priority {
			return rest[i].item.Priority < rest[j].item.Priority
		}
		return rest[i].item.ID < rest[j].item.ID
	})

	ordered := make([]model.GroupItem, 0, len(samples)+len(cooling))
	if winner >= 0 {
		ordered = append(ordered, samples[winner].item)
	}
	for _, sample := range rest {
		ordered = append(ordered, sample.item)
	}
	return append(ordered, cooling...)
}

// pickGroupItemAllocate 是分压模式的选路入口：算计划 → 推进本分组的分配累加器 → 定序 → 走既有选路链路。
// 分配累加器与冷却/亲和同挂在顶层 RouteState 上，随分组状态重置归零（改模式、删分组时一并丢弃）。
func pickGroupItemAllocate(group model.Group, deps routeDeps, features SmartFeatures) model.GroupItem {
	settings := allocateSettingsOf()
	in := allocateInput{tokens: features.Tokens, pointsPerUnit: balancePointsPerUnit(), settings: settings}
	ranked := allocateRank(group, deps, in, true)
	if len(ranked) == 0 {
		return model.GroupItem{}
	}
	return pickGroupItem(group.WithItems(ranked))
}

// allocateRank 取一次分压定序结果。advance=true 时推进分配累加器（热路径选路用），
// false 时只读（竞速候选、监控预览用——它们不该改变下一次当选者）。
func allocateRank(group model.Group, deps routeDeps, in allocateInput, advance bool) []model.GroupItem {
	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	cooldowns := cloneCooldowns(route.Cooldowns)
	if route.allocCurrent == nil {
		route.allocCurrent = make(map[int]int)
	}
	current := route.allocCurrent
	routeMu.Unlock()

	samples, _, cooling, err := allocatePlan(group.Items, cooldowns, time.Now().UnixMilli(), nil, deps, in)
	if err != nil || len(samples) == 0 {
		return nil
	}

	weights := make([]int, len(samples))
	items := make([]model.GroupItem, len(samples))
	for index, sample := range samples {
		weights[index] = sample.weight
		items[index] = sample.item
	}

	winner := -1
	routeMu.Lock()
	if advance {
		winner = allocateStep(current, items, weights)
	} else {
		// 只读口径：不推进累加器，直接取当前权重最大的成员（并列按 priority、再按成员行 ID）。
		winner = 0
		for index, item := range items {
			if weights[index] > weights[winner] {
				winner = index
				continue
			}
			if weights[index] == weights[winner] && item.Priority < items[winner].Priority {
				winner = index
			}
		}
	}
	routeMu.Unlock()

	return orderAllocateSamples(samples, winner, cooling)
}

// rankedAllocateCandidates 是分压模式的竞速候选（只读口径，不推进分配累加器）。
func rankedAllocateCandidates(group model.Group, deps routeDeps, features SmartFeatures) []model.GroupItem {
	settings := allocateSettingsOf()
	in := allocateInput{tokens: features.Tokens, pointsPerUnit: balancePointsPerUnit(), settings: settings}
	return allocateRank(group, deps, in, false)
}

// balancePointsPerUnit 读"多少额度点 = 1 个货币单位"，缺省/非法时用与余额页一致的默认值。
// 读设置失败不阻断选路：分压用不到它就按默认口径算（与余额页同一套 NormalizeBalanceUnit，不另立第二套）。
func balancePointsPerUnit() float64 {
	value, _ := op.SettingGetString(model.SettingKeyBalancePointsPerUnit)
	currency, _ := op.SettingGetString(model.SettingKeyBalanceCurrency)
	points, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		points = 0
	}
	normalized, _ := model.NormalizeBalanceUnit(points, currency)
	return normalized
}
