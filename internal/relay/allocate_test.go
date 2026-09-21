package relay

import (
	"net/http"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// 额度分压（mode=allocate, T-allocate-001）判据。
//
// 三条要固定住的机制（对应文件头四条口径里的前三条）:
//  1. 剩余请求数的三种来源与"不知道就说不知道";
//  2. 权重按剩余比例铺开、未知取中位数、健康折扣只削权不剔除;
//  3. 分配结果长期正比于权重（分压的核心承诺）。
//
// 第 4 条（限流/自限流）在 allocate_guard_test.go 里, 那里要动限流账与冷却表。

// allocateTestDeps 是可定制的分压依赖：与 weightedDeps 的差别是请求数与 token 数分开给,
// 便于构造"本分钟已经打满 RPM"的场景。
type allocateTestDeps struct {
	billing map[int]model.ChannelBilling
	cost    map[int]float64
	quality map[int]float64
	latency map[int]int64
	rpm     map[int]int
	tpm     map[int]int
}

func (d allocateTestDeps) deps() routeDeps {
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
		load: func(itemID int) (int, int, bool) {
			requests, seenRequests := d.rpm[itemID]
			tokens, seenTokens := d.tpm[itemID]
			return requests, tokens, seenRequests || seenTokens
		},
		billing: func(item model.GroupItem) (model.ChannelBilling, bool) {
			value, ok := d.billing[item.ID]
			return value, ok
		},
	}
}

// allocateItems 三成员分组（成员行 ID 11/22/33, 优先级 1/2/3）。
func allocateItems() []model.GroupItem {
	return []model.GroupItem{
		{ID: 11, Priority: 1, Available: true},
		{ID: 22, Priority: 2, Available: true},
		{ID: 33, Priority: 3, Available: true},
	}
}

func allocateTestInput(tokens int) allocateInput {
	return allocateInput{
		tokens:        tokens,
		pointsPerUnit: model.BalanceDefaultPointsPerUnit,
		settings:      allocateSettings{tokens: 1000, healthWeight: 0, minRequests: 1},
	}
}

func samplesOf(t *testing.T, items []model.GroupItem, deps routeDeps, in allocateInput) []allocateSample {
	t.Helper()
	samples := make([]allocateSample, 0, len(items))
	for _, item := range items {
		samples = append(samples, allocateSampleOf(item, deps, in))
	}
	return samples
}

// TestEstimateRemainingRequestsPrefersMonthly 包月额度是"还能发多少次"的直接答案:
// 剩余 = 额度 − 已用, 且此时不看余额（余额可能是另一笔钱）。
func TestEstimateRemainingRequestsPrefersMonthly(t *testing.T) {
	in := allocateTestInput(1000)
	billing := model.ChannelBilling{
		MonthlyQuota: 500,
		MonthlyUsed:  120,
		Balance:      9_000_000,
		BalanceKnown: true,
	}
	got, ok, source := estimateRemainingRequests(billing, 30, true, in)
	if !ok || source != allocateSourceMonth || got != 380 {
		t.Fatalf("包月口径 = (%v, %v, %q), want (380, true, monthly)", got, ok, source)
	}
}

// TestEstimateRemainingRequestsMonthlyNeverNegative 包月超额（已用 > 额度）按 0 处理, 不给负数。
func TestEstimateRemainingRequestsMonthlyNeverNegative(t *testing.T) {
	in := allocateTestInput(1000)
	got, ok, _ := estimateRemainingRequests(model.ChannelBilling{MonthlyQuota: 100, MonthlyUsed: 140}, 0, false, in)
	if !ok || got != 0 {
		t.Fatalf("包月超额 = (%v, %v), want (0, true)", got, ok)
	}
}

// TestEstimateRemainingRequestsByPerCallPrice 按次计费站点: 剩余请求数 = 余额 / 每次单价。
// 余额点先按 balance_points_per_unit 折成货币（500000 点 = 1 单位, 与余额页同口径）。
func TestEstimateRemainingRequestsByPerCallPrice(t *testing.T) {
	in := allocateTestInput(1000)
	// 2.5 个货币单位 ÷ 0.05 每请求 = 50 次。
	billing := model.ChannelBilling{PerCallPrice: 0.05, Balance: 1_250_000, BalanceKnown: true}
	got, ok, source := estimateRemainingRequests(billing, 0, false, in)
	if !ok || source != allocateSourceBalance || got != 50 {
		t.Fatalf("按次口径 = (%v, %v, %q), want (50, true, balance)", got, ok, source)
	}
}

// TestEstimateRemainingRequestsByUnitPrice 按量计费: 余额 ÷（价表单价 × token × 倍率）。
// 本次请求的 token 估算优先于设置里的兜底值 —— 这就是"按本次请求折算"与"按平均值折算"的区别。
func TestEstimateRemainingRequestsByUnitPrice(t *testing.T) {
	in := allocateTestInput(5000)
	// 单价 10（每百万 token 输入+输出）、倍率 2、本次 5000 token → 每次 0.1 货币单位;
	// 余额 5 个货币单位 → 50 次。
	billing := model.ChannelBilling{Multiplier: 2, Balance: 2_500_000, BalanceKnown: true}
	got, ok, source := estimateRemainingRequests(billing, 10, true, in)
	if !ok || source != allocateSourceBalance || got != 50 {
		t.Fatalf("按量口径 = (%v, %v, %q), want (50, true, balance)", got, ok, source)
	}
	// 同一余额, 请求放大十倍 → 剩余次数缩到十分之一（"按剩余请求数"必须随请求大小变化）。
	big := allocateInput{tokens: 50000, pointsPerUnit: model.BalanceDefaultPointsPerUnit, settings: in.settings}
	got, ok, _ = estimateRemainingRequests(billing, 10, true, big)
	if !ok || got != 5 {
		t.Fatalf("大请求口径 = (%v, %v), want (5, true)", got, ok)
	}
}

// TestEstimateRemainingRequestsUnknownStaysUnknown 余额知道但没有"一次请求花多少"的知识时,
// 必须报"不知道"（ok=false）而不是 0 —— 报 0 会被当成"钱花完了"直接剔除。
func TestEstimateRemainingRequestsUnknownStaysUnknown(t *testing.T) {
	in := allocateTestInput(0) // token 估算为 0, 走设置兜底
	in.settings.tokens = 0     // 兜底也关掉 → 折不出单价
	billing := model.ChannelBilling{Balance: 5_000_000, BalanceKnown: true}
	if got, ok, source := estimateRemainingRequests(billing, 0, false, in); ok || got != 0 || source != "" {
		t.Fatalf("无成本知识 = (%v, %v, %q), want (0, false, \"\")", got, ok, source)
	}
	// 余额从来没读到过: 同样是未知。
	if _, ok, _ := estimateRemainingRequests(model.ChannelBilling{}, 30, true, in); ok {
		t.Fatal("没读到过余额时不该给出已知的剩余请求数")
	}
}

// TestEstimateRemainingRequestsZeroBalanceIsKnownZero 余额读到且为 0 是**已知的 0**（与"未知"不同）:
// 上层据此剔除该成员（钱花完了）, 而不是给它乐观先验继续分流。
func TestEstimateRemainingRequestsZeroBalanceIsKnownZero(t *testing.T) {
	in := allocateTestInput(1000)
	got, ok, source := estimateRemainingRequests(model.ChannelBilling{Balance: 0, BalanceKnown: true}, 30, true, in)
	if !ok || got != 0 || source != allocateSourceBalance {
		t.Fatalf("余额归零 = (%v, %v, %q), want (0, true, balance)", got, ok, source)
	}
}

// TestAllocateWeightsProportionalToRemaining 分压的核心: 权重正比于剩余请求数,
// 剩余最多的成员拿满刻度（100），其余按比例（1/3 余量 → 约 33）。
func TestAllocateWeightsProportionalToRemaining(t *testing.T) {
	samples := []allocateSample{
		{item: model.GroupItem{ID: 11}, requests: 100, known: true},
		{item: model.GroupItem{ID: 22}, requests: 300, known: true},
		{item: model.GroupItem{ID: 33}, requests: 600, known: true},
	}
	weights := allocateWeights(samples, nil)
	want := []int{17, 50, 100}
	for index := range want {
		if weights[index] != want[index] {
			t.Fatalf("权重 = %v, want %v（剩余 100/300/600 应按比例铺到 100 满刻度）", weights, want)
		}
	}
}

// TestAllocateWeightsUnknownTakesMedian 剩余未知的成员取"已知权重的中位数":
// 不算它最优（那会吞掉所有人的流量）, 也不算它最差（那是惩罚未知, 与全仓口径不符）。
func TestAllocateWeightsUnknownTakesMedian(t *testing.T) {
	samples := []allocateSample{
		{item: model.GroupItem{ID: 11}, requests: 100, known: true},
		{item: model.GroupItem{ID: 22}, requests: 200, known: true},
		{item: model.GroupItem{ID: 33}, requests: 400, known: true},
		{item: model.GroupItem{ID: 44}}, // 未知
	}
	weights := allocateWeights(samples, nil)
	// 已知三个的权重 = 25 / 50 / 100 → 中位数 50; 未知者取 50。
	if weights[3] != 50 {
		t.Fatalf("未知成员权重 = %d, want 50（已知权重 25/50/100 的中位数）", weights[3])
	}
}

// TestAllocateWeightsAllUnknownIsEven 全员未知时人人等权（退化为平均分压）,
// 而不是谁 priority 高谁吃满 —— 那正是本模式要避免的贪心。
func TestAllocateWeightsAllUnknownIsEven(t *testing.T) {
	samples := []allocateSample{
		{item: model.GroupItem{ID: 11}},
		{item: model.GroupItem{ID: 22}},
		{item: model.GroupItem{ID: 33}},
	}
	weights := allocateWeights(samples, nil)
	for index, weight := range weights {
		if weight != allocateMinWeight {
			t.Fatalf("全员未知时权重 = %v, want 全员 %d", weights, allocateMinWeight)
		}
		_ = index
	}
}

// TestAllocateWeightsHealthDiscountShrinksButKeeps 健康折扣只削权、不剔除, 且下限是 1:
// 一个成功率很差的成员仍然会分到极少量流量（它还有救, 冷却才是"停用"的职责）。
func TestAllocateWeightsHealthDiscountShrinksButKeeps(t *testing.T) {
	samples := []allocateSample{
		{item: model.GroupItem{ID: 11}, requests: 100, known: true},
		{item: model.GroupItem{ID: 22}, requests: 100, known: true},
	}
	settings := allocateSettings{healthWeight: 100}
	deps := allocateTestDeps{quality: map[int]float64{11: 1.0, 22: 0.0}}.deps()
	nowMs := time.Now().UnixMilli()
	factors := []float64{
		memberHealthFactor(11, deps, settings, nowMs),
		memberHealthFactor(22, deps, settings, nowMs),
	}
	if factors[0] != 1 {
		t.Fatalf("满分成员健康系数 = %v, want 1", factors[0])
	}
	if factors[1] != 0 {
		t.Fatalf("零分成员健康系数 = %v, want 0", factors[1])
	}
	weights := allocateWeights(samples, factors)
	if weights[0] != allocateMaxWeight {
		t.Fatalf("健康成员权重 = %d, want %d", weights[0], allocateMaxWeight)
	}
	if weights[1] != allocateMinWeight {
		t.Fatalf("健康折扣打到底的成员权重 = %d, want %d（削权不剔除）", weights[1], allocateMinWeight)
	}
}

// TestAllocateStepDistributesProportionally 分压的长期承诺: 权重 1:3 时,
// 跑满一个周期（1+3=4 次）应恰好是 3 次 vs 1 次, 且不出现"永远选同一个"。
func TestAllocateStepDistributesProportionally(t *testing.T) {
	items := []model.GroupItem{{ID: 11}, {ID: 22}}
	weights := []int{1, 3}
	current := map[int]int{}
	counts := map[int]int{}
	for round := 0; round < 4; round++ {
		best := allocateStep(current, items, weights)
		if best < 0 {
			t.Fatalf("第 %d 轮没有当选者", round)
		}
		counts[items[best].ID]++
	}
	if counts[11] != 1 || counts[22] != 3 {
		t.Fatalf("一个周期内的分配 = %v, want 11:1 次 / 22:3 次（正比于权重）", counts)
	}
}

// TestAllocatePlanDropsExhaustedMembers 剩余请求数已知且不足一次（< min_requests）的成员被剔除;
// 未知成员不受影响（不拿"不知道"当"没钱"）。
func TestAllocatePlanDropsExhaustedMembers(t *testing.T) {
	deps := allocateTestDeps{billing: map[int]model.ChannelBilling{
		11: {MonthlyQuota: 10, MonthlyUsed: 10}, // 用尽 → 剔除
		22: {MonthlyQuota: 10, MonthlyUsed: 5},  // 剩 5 → 保留
		33: {},                                  // 未知 → 保留
	}}.deps()
	in := allocateTestInput(1000)
	samples, _, _, err := allocatePlan(allocateItems(), map[int]int64{}, 1000, nil, deps, in)
	if err != nil {
		t.Fatalf("allocatePlan: %v", err)
	}
	ids := make([]int, 0, len(samples))
	for _, sample := range samples {
		ids = append(ids, sample.item.ID)
	}
	if len(ids) != 2 || ids[0] != 22 || ids[1] != 33 {
		t.Fatalf("候选 = %v, want [22 33]（11 的包月已用尽）", ids)
	}
}

// TestAllocatePlanAllExhaustedReportsNoEligible 全员余量不足时明确报"没有可尝试的成员",
// 由调用方按无目标等待（而不是硬塞一个已经没钱的成员）。
func TestAllocatePlanAllExhaustedReportsNoEligible(t *testing.T) {
	deps := allocateTestDeps{billing: map[int]model.ChannelBilling{
		11: {MonthlyQuota: 10, MonthlyUsed: 10},
		22: {MonthlyQuota: 10, MonthlyUsed: 11},
	}}.deps()
	items := []model.GroupItem{{ID: 11, Available: true}, {ID: 22, Available: true}}
	_, _, _, err := allocatePlan(items, map[int]int64{}, 1000, nil, deps, allocateTestInput(1000))
	if err != ErrNoEligibleMember {
		t.Fatalf("全员余量不足 err = %v, want %v", err, ErrNoEligibleMember)
	}
}

// TestPickGroupItemAllocateSpreadsAcrossMembers 端到端: 剩余 1:9 的两个成员,
// 跑 10 轮应大致按 1:9 分配（这里取整刻度后权重是 11 与 100, 故按 1:9 附近的确定性序列断言）。
// 关键是否定式断言: **不是**十轮全给同一个成员（那正是贪心模式的既有行为）。
func TestPickGroupItemAllocateSpreadsAcrossMembers(t *testing.T) {
	defer ResetRouteState(9401)
	group := pickTestGroup(9401, model.GroupModeAllocate, 0, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      60,
		MemberAffinitySeconds:      0,
	})
	group.Items = availableLeaves(11, 22)
	deps := allocateTestDeps{billing: map[int]model.ChannelBilling{
		11: {MonthlyQuota: 100, MonthlyUsed: 90}, // 剩 10
		22: {MonthlyQuota: 100, MonthlyUsed: 10}, // 剩 90
	}}.deps()

	counts := map[int]int{}
	for round := 0; round < 10; round++ {
		item := pickGroupItemAllocate(group, deps, SmartFeatures{Tokens: 1000})
		if item.ID == 0 {
			t.Fatalf("第 %d 轮没有选出成员", round)
		}
		counts[item.ID]++
	}
	if counts[22] == 0 || counts[11] == 0 {
		t.Fatalf("分配 = %v, want 两个成员都分到流量（分压而非贪心）", counts)
	}
	if counts[22] < counts[11] {
		t.Fatalf("分配 = %v, want 剩余多的 22 至少不比 11 少", counts)
	}
}

// TestAllocateRankReadOnlyDoesNotAdvance 只读口径（竞速候选/监控预览）不得改变分配累加器:
// 否则"看一眼候选"就会把下一次当选者顶掉, 分配比例被自己的预演带偏。
func TestAllocateRankReadOnlyDoesNotAdvance(t *testing.T) {
	defer ResetRouteState(9402)
	group := pickTestGroup(9402, model.GroupModeAllocate, 0, model.GroupRelayConfig{})
	group.Items = availableLeaves(11, 22)
	deps := allocateTestDeps{billing: map[int]model.ChannelBilling{
		11: {MonthlyQuota: 100, MonthlyUsed: 90},
		22: {MonthlyQuota: 100, MonthlyUsed: 10},
	}}.deps()

	in := allocateInput{tokens: 1000, pointsPerUnit: model.BalanceDefaultPointsPerUnit,
		settings: allocateSettings{tokens: 1000, minRequests: 1}}
	for round := 0; round < 5; round++ {
		_ = allocateRank(group, deps, in, false)
	}
	routeMu.Lock()
	records := routes[9402]
	growth := 0
	if records != nil {
		for _, value := range records.allocCurrent {
			growth += value
		}
	}
	routeMu.Unlock()
	if growth != 0 {
		t.Fatalf("只读口径累积器 = %d, want 0（只读不得推进分配）", growth)
	}
}

// TestParseRetryAfterHeaderSecondsAndDate 上游的等待提示支持两种写法（RFC 7231）:
// delta-seconds 与 HTTP-date; 解析不出来时不猜（ok=false）。
func TestParseRetryAfterHeaderSecondsAndDate(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	if hint, ok := parseRetryAfterHeader("30", now); !ok || hint != 30*time.Second {
		t.Fatalf("delta-seconds = (%v, %v), want (30s, true)", hint, ok)
	}
	glue := now.Add(45 * time.Second).Format(http.TimeFormat)
	if hint, ok := parseRetryAfterHeader(glue, now); !ok || hint != 45*time.Second {
		t.Fatalf("HTTP-date = (%v, %v), want (45s, true)", hint, ok)
	}
	if _, ok := parseRetryAfterHeader("nonsense", now); ok {
		t.Fatal("解析不出来时不该给出等待提示")
	}
	if _, ok := parseRetryAfterHeader("0", now); ok {
		t.Fatal("0 秒提示不该当成等待提示（等于没有提示）")
	}
}

// TestRetryAfterFromHeaderFallsBackToResetHeader 有些中转站不给 Retry-After,
// 而是给 X-RateLimit-Reset-After（"还有多少秒"）; 两者都要认, 且 Retry-After 优先。
func TestRetryAfterFromHeaderFallsBackToResetHeader(t *testing.T) {
	now := time.Now()
	header := http.Header{}
	header.Set("X-RateLimit-Reset-After", "12")
	if hint := retryAfterFromHeader(header, now); hint != 12*time.Second {
		t.Fatalf("X-RateLimit-Reset-After = %v, want 12s", hint)
	}
	header.Set("Retry-After", "7")
	if hint := retryAfterFromHeader(header, now); hint != 7*time.Second {
		t.Fatalf("两种头都在时应以 Retry-After 为准, got %v", hint)
	}
}
