package relay

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm/httpclient"
)

// 限流与自限流（T-allocate-002 / T-allocate-003）判据。
//
// 三条要固定住的机制:
//  1. 上游 429 带 Retry-After 时冷却时长**取上游提示与分组配置的较长者**（上游说等更久就等更久）;
//  2. 拿不到提示的 429 保持既有可恢复语义（冷却 = 分组配置值）;
//  3. 自限流（成员级 RPM/TPM）到顶的成员在分压里让开一轮, 但**全员到顶时照常使用**
//     （宁可撞一下上游, 也不要让请求没人可用）。

func throttleTestGroup(t *testing.T, id int, cooldownSeconds int) model.Group {
	t.Helper()
	group := pickTestGroup(id, model.GroupModeFailover, 0, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      cooldownSeconds,
		MemberAffinitySeconds:      0,
	})
	group.Items = availableLeaves(11, 22)
	// 先走一次选路把分组的进程内路由状态建起来: 既有链路里 recordRouteFailure 只在状态已存在时
	// 打冷却（状态由第一次选路创建）, 这里复现同一条前置条件。
	if got := PickGroupItem(group, group.Items); got.ID != 11 {
		t.Fatalf("首次选路 = %d, want 11（测试前置条件）", got.ID)
	}
	return group
}

// cooldownOf 读某成员当前的冷却截止时刻（毫秒）; 无冷却返回 0。
func cooldownOf(groupID, itemID int) int64 {
	routeMu.Lock()
	defer routeMu.Unlock()
	route := routes[groupID]
	if route == nil {
		return 0
	}
	return route.Cooldowns[itemID]
}

// TestRecordRouteFailureHintExtendsCooldown 上游提示比分组配置长时, 冷却按上游提示走:
// 上游说"120 秒后再来"而我们只配了 5 秒, 若按 5 秒冷却, 下一次请求必然再吃一个 429。
func TestRecordRouteFailureHintExtendsCooldown(t *testing.T) {
	defer ResetRouteState(9501)
	defer resetMemberThrottleForTest()
	group := throttleTestGroup(t, 9501, 5)

	before := time.Now().UnixMilli()
	if !recordRouteFailureHint(group, 11, 1, 10, 120*time.Second) {
		t.Fatal("上游限流提示下该成员应立刻冷却")
	}
	deadline := cooldownOf(9501, 11)
	if deadline < before+120_000 {
		t.Fatalf("冷却截止 = %d, want >= %d（上游说等 120 秒, 配置只有 5 秒）", deadline, before+120_000)
	}
	// 限流账也要记上（分压选路与监控都读它）。
	record, ok := MemberThrottleOf(11)
	if !ok || record.Hits != 1 {
		t.Fatalf("限流账 = (%+v, %v), want 命中 1 次", record, ok)
	}
}

// TestRecordRouteFailureHintKeepsConfigWhenHintShorter 上游提示比分组配置短时听配置的:
// 配置 60 秒是运维刻意设的下限, 不该被一次"等 3 秒"削弱。
func TestRecordRouteFailureHintKeepsConfigWhenHintShorter(t *testing.T) {
	defer ResetRouteState(9502)
	defer resetMemberThrottleForTest()
	group := throttleTestGroup(t, 9502, 60)

	before := time.Now().UnixMilli()
	if !recordRouteFailureHint(group, 11, 1, 10, 3*time.Second) {
		t.Fatal("上游限流提示下该成员应立刻冷却")
	}
	deadline := cooldownOf(9502, 11)
	if deadline < before+60_000 {
		t.Fatalf("冷却截止 = %d, want >= %d（提示 3 秒短于配置 60 秒, 取配置）", deadline, before+60_000)
	}
	if deadline > before+65_000 {
		t.Fatalf("冷却截止 = %d, want ≈ 配置的 60 秒（不该被 3 秒的提示削短, 也不该被拉长）", deadline)
	}
}

// TestRecordRouteFailureWithoutHintMatchesLegacy 没有提示时行为与既有链路逐字一致:
// 冷却 = 分组配置的秒数, 且限流账不动（普通失败不是限流）。
func TestRecordRouteFailureWithoutHintMatchesLegacy(t *testing.T) {
	defer ResetRouteState(9503)
	defer resetMemberThrottleForTest()
	group := throttleTestGroup(t, 9503, 30)

	before := time.Now().UnixMilli()
	if !recordRouteFailure(group, 11, 1, 10) {
		t.Fatal("无提示的失败也该按既有语义冷却")
	}
	deadline := cooldownOf(9503, 11)
	if deadline < before+30_000 || deadline > before+35_000 {
		t.Fatalf("冷却截止 = %d, want ≈ %d（分组配置 30 秒）", deadline, before+30_000)
	}
	if _, ok := MemberThrottleOf(11); ok {
		t.Fatal("普通失败不该写限流账（限流账只记 429 一类）")
	}
}

// TestThrottleCooldownCappedBySetting 上游给一个很长的提示（一小时）时仍受设置项封顶
// （route_ratelimit_cooldown_max_seconds, 默认 300 秒）: 一个坏响应不能把成员冻住一小时。
func TestThrottleCooldownCappedBySetting(t *testing.T) {
	defer ResetRouteState(9504)
	defer resetMemberThrottleForTest()
	group := throttleTestGroup(t, 9504, 5)

	before := time.Now().UnixMilli()
	recordRouteFailureHint(group, 11, 1, 10, time.Hour)
	deadline := cooldownOf(9504, 11)
	// 默认封顶 300 秒（设置缓存未初始化时走缺省值）。
	if deadline > before+int64(defaultThrottleCapSeconds)*1000+1000 {
		t.Fatalf("冷却截止 = %d, want <= %d（默认封顶 %d 秒）", deadline, before+int64(defaultThrottleCapSeconds)*1000, defaultThrottleCapSeconds)
	}
	if deadline < before+120_000 {
		t.Fatalf("冷却截止 = %d, want 明显大于配置的 5 秒（提示一小时被夹到封顶值）", deadline)
	}
}

// TestRateLimitHintOnlyFor429 只有 429 才算限流提示:
// 500 带 Retry-After 属于上游故障, 沿用既有冷却语义, 不该被当成"这把钥匙超限了"。
func TestRateLimitHintOnlyFor429(t *testing.T) {
	now := time.Now()
	tooMany := &upstreamStatusError{status: http.StatusTooManyRequests, message: "429", retryHint: 20 * time.Second}
	if hint := rateLimitHint(tooMany, now); hint != 20*time.Second {
		t.Fatalf("429 提示 = %v, want 20s", hint)
	}
	serverErr := &upstreamStatusError{status: http.StatusInternalServerError, message: "500", retryHint: 20 * time.Second}
	if hint := rateLimitHint(serverErr, now); hint != 0 {
		t.Fatalf("500 不该被当成限流提示, got %v", hint)
	}
}

// TestRateLimitHintFromHTTPClientError 非流式路径的提示来自库的 httpclient.Error（它带着响应头）:
// 这条链断了的话, 非流式 429 就永远拿不到上游的等待时长。
func TestRateLimitHintFromHTTPClientError(t *testing.T) {
	header := http.Header{}
	header.Set("Retry-After", "25")
	err := fmt.Errorf("wrapped: %w", &httpclient.Error{
		StatusCode: http.StatusTooManyRequests,
		Headers:    header,
	})
	if hint := rateLimitHint(err, time.Now()); hint != 25*time.Second {
		t.Fatalf("非流式 429 提示 = %v, want 25s", hint)
	}
	// 没有提示头时返回 0（由调用方按分组配置冷却）。
	bare := fmt.Errorf("wrapped: %w", &httpclient.Error{StatusCode: http.StatusTooManyRequests})
	if hint := rateLimitHint(bare, time.Now()); hint != 0 {
		t.Fatalf("无头的 429 提示 = %v, want 0", hint)
	}
}

// TestAllocatePlanSkipsLimitedMembersByDefault 自限流: 本分钟已达 RPM 上限的成员让开,
// 其余成员照常分到流量。
func TestAllocatePlanSkipsLimitedMembersByDefault(t *testing.T) {
	deps := allocateTestDeps{
		billing: map[int]model.ChannelBilling{
			11: {MonthlyQuota: 100, MonthlyUsed: 10},
			22: {MonthlyQuota: 100, MonthlyUsed: 10},
		},
		rpm: map[int]int{11: 60}, // 11 本分钟已发 60 次
	}.deps()
	in := allocateTestInput(1000)
	in.settings.rpmLimit = 60

	items := []model.GroupItem{{ID: 11, Available: true, Priority: 1}, {ID: 22, Available: true, Priority: 2}}
	samples, _, _, err := allocatePlan(items, map[int]int64{}, time.Now().UnixMilli(), nil, deps, in)
	if err != nil {
		t.Fatalf("allocatePlan: %v", err)
	}
	if len(samples) != 1 || samples[0].item.ID != 22 {
		t.Fatalf("自限流下的候选 = %+v, want 只剩 22（11 本分钟已打满 RPM）", samples)
	}
}

// TestAllocatePlanKeepsAllWhenEveryoneLimited 全员打满自限流时**照常全部使用**:
// 宁可去撞一下上游（也许它已经恢复）, 也不要让请求没人可用。
func TestAllocatePlanKeepsAllWhenEveryoneLimited(t *testing.T) {
	deps := allocateTestDeps{
		billing: map[int]model.ChannelBilling{
			11: {MonthlyQuota: 100, MonthlyUsed: 10},
			22: {MonthlyQuota: 100, MonthlyUsed: 10},
		},
		rpm: map[int]int{11: 60, 22: 61},
	}.deps()
	in := allocateTestInput(1000)
	in.settings.rpmLimit = 60

	items := []model.GroupItem{{ID: 11, Available: true, Priority: 1}, {ID: 22, Available: true, Priority: 2}}
	samples, _, _, err := allocatePlan(items, map[int]int64{}, time.Now().UnixMilli(), nil, deps, in)
	if err != nil {
		t.Fatalf("allocatePlan: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("全员自限流时的候选 = %d 个, want 2 个（不能把请求变成无人可用）", len(samples))
	}
}

// TestAllocateLimitsOffByDefault 自限流默认关闭（0 = 不限）:
// 上游真实限额我们并不知道, 默认替用户设一个数会把请求无故挡下来。
func TestAllocateLimitsOffByDefault(t *testing.T) {
	settings := allocateSettingsOf()
	if settings.rpmLimit != 0 || settings.tpmLimit != 0 {
		t.Fatalf("自限流缺省 = RPM %d / TPM %d, want 双双为 0（默认不限）", settings.rpmLimit, settings.tpmLimit)
	}
}

// TestMemberHealthFactorPenalizesThrottled 刚被上游限流的成员健康系数直接打到底:
// 分压要在"限流还没冷却完"的窗口里主动让开它, 而不是等冷却到期又一头撞上去。
func TestMemberHealthFactorPenalizesThrottled(t *testing.T) {
	defer resetMemberThrottleForTest()
	settings := allocateSettings{healthWeight: 100}
	nowMs := time.Now().UnixMilli()
	recordMemberThrottle(11, nowMs, nowMs+60_000)

	if factor := memberHealthFactor(11, routeDeps{}, settings, nowMs); factor != 0 {
		t.Fatalf("限流中的成员健康系数 = %v, want 0", factor)
	}
	// 等待期过了就恢复：系数回到 1（否则限流账会变成永久惩罚）。
	if factor := memberHealthFactor(11, routeDeps{}, settings, nowMs+61_000); factor != 1 {
		t.Fatalf("限流等待期结束后的健康系数 = %v, want 1", factor)
	}
}

// TestThrottleRecordKeepsLongestDeadline 同一个成员连续被限流时, 截止时刻取较晚者:
// 第二次的提示比第一次短, 不该把已经等到的更晚期限提前。
func TestThrottleRecordKeepsLongestDeadline(t *testing.T) {
	defer resetMemberThrottleForTest()
	now := time.Now().UnixMilli()
	recordMemberThrottle(11, now, now+120_000)
	record := recordMemberThrottle(11, now, now+5_000)
	if record.untilMs != now+120_000 {
		t.Fatalf("截止时刻 = %d, want %d（取较晚者）", record.untilMs, now+120_000)
	}
	if record.hits != 2 {
		t.Fatalf("命中次数 = %d, want 2", record.hits)
	}
}

// TestRateLimitHintViaStatusErrorChain 提示要能穿过 %w 包裹链:
// 生产路径上错误会被 fmt.Errorf 包了好几层, errors.As 取不到就等于功能没接线。
func TestRateLimitHintViaStatusErrorChain(t *testing.T) {
	inner := &upstreamStatusError{status: 429, message: "429", retryHint: 9 * time.Second}
	wrapped := fmt.Errorf("round failed: %w", fmt.Errorf("upstream: %w", inner))
	if hint := rateLimitHint(wrapped, time.Now()); hint != 9*time.Second {
		t.Fatalf("穿过包裹链的提示 = %v, want 9s", hint)
	}
	if !errors.Is(wrapped, inner) {
		t.Fatal("包裹链应保持 errors.Is 可比（提示解析依赖它）")
	}
}
