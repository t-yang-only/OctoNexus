package relay

import (
	"math"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
)

// 速度观测与速度应对的判据（T-speed-001）。
//
// 每条断言都对着文件头的三条口径：样本不足不下结论、折扣分维取最差不叠加、
// 看门狗只会更早放弃（结果永远不大于分组配置）。
func speedTestSettings() speedSettings {
	return speedSettings{
		weightPct:  40,
		slowTtfbMs: 3000,
		slowTps:    8,
		feMultiple: 4,
		feFloorMs:  5000,
	}
}

func TestRecordMemberSpeedComputesThroughputAndTtfb(t *testing.T) {
	resetMemberSpeedForTest()
	// 三次成功: 首帧 400ms, 每次 500 token / 5s → 吞吐 = 1500 token / 15000ms = 100 tok/s。
	for i := 0; i < 3; i++ {
		recordMemberSpeed(101, 400, 500, 5000)
	}
	reading, ok := memberSpeedReading(101)
	if !ok {
		t.Fatalf("三次样本后应能读到速度账")
	}
	if !reading.Ready {
		t.Fatalf("三次样本应达到可下结论的门槛, 实际 Samples=%d", reading.Samples)
	}
	if reading.TtfbMs != 400 {
		t.Fatalf("首帧 = %d, want 400", reading.TtfbMs)
	}
	if math.Abs(reading.TokensPerSec-100) > 0.5 {
		t.Fatalf("吞吐 = %.2f, want 100", reading.TokensPerSec)
	}
}

func TestSpeedSamplesBelowThresholdAreNotReady(t *testing.T) {
	resetMemberSpeedForTest()
	recordMemberSpeed(102, 200, 0, 0)
	recordMemberSpeed(102, 200, 0, 0)
	reading, ok := memberSpeedReading(102)
	if !ok || reading.Ready {
		t.Fatalf("两次样本不该达到可下结论门槛: ok=%v ready=%v", ok, reading.Ready)
	}
	// 关键推论: 样本不足时折扣必须为 1（未知不惩罚）。
	if factor := memberSpeedFactor(102, speedTestSettings()); factor != 1 {
		t.Fatalf("样本不足时折扣 = %v, want 1", factor)
	}
}

func TestSpeedWindowExpiry(t *testing.T) {
	resetMemberSpeedForTest()
	saved := relayNowMs
	defer func() { relayNowMs = saved }()

	now := int64(1_000_000)
	relayNowMs = func() int64 { return now }
	for i := 0; i < 3; i++ {
		recordMemberSpeed(103, 300, 100, 1000)
	}
	if _, ok := memberSpeedReading(103); !ok {
		t.Fatalf("窗口内应能读到")
	}
	// 越过窗口（2 分钟）之后必须当"没有样本"，而不是拿旧数据继续打分。
	now += memberSpeedWindowMs + 1
	if _, ok := memberSpeedReading(103); ok {
		t.Fatalf("窗口过期后不该再返回样本")
	}
	if factor := memberSpeedFactor(103, speedTestSettings()); factor != 1 {
		t.Fatalf("窗口过期后折扣 = %v, want 1", factor)
	}
}

func TestMemberSpeedFactorWorstOfTtfbAndThroughput(t *testing.T) {
	resetMemberSpeedForTest()
	// 成员 110: 首帧快(200ms)但吞吐低(2 tok/s) → 吞吐这一维决定折扣。
	for i := 0; i < 3; i++ {
		recordMemberSpeed(110, 200, 10, 5000) // 10 token / 5s = 2 tok/s
	}
	// 成员 111: 首帧慢(12s)但吞吐正常(100 tok/s) → 首帧这一维决定折扣。
	for i := 0; i < 3; i++ {
		recordMemberSpeed(111, 12000, 500, 5000)
	}
	// 成员 112: 两项都正常 → 不打折。
	for i := 0; i < 3; i++ {
		recordMemberSpeed(112, 300, 500, 5000)
	}

	settings := speedTestSettings()
	slowByThroughput := memberSpeedFactor(110, settings)
	slowByTtfb := memberSpeedFactor(111, settings)
	fast := memberSpeedFactor(112, settings)

	if fast != 1 {
		t.Fatalf("正常成员折扣 = %v, want 1", fast)
	}
	// slowness: 110 是 8/2 = 4 倍；111 是 12000/3000 = 4 倍。
	want := math.Pow(1.0/4.0, 0.4)
	if math.Abs(slowByThroughput-want) > 0.01 {
		t.Fatalf("吞吐慢成员折扣 = %.4f, want %.4f", slowByThroughput, want)
	}
	if math.Abs(slowByTtfb-want) > 0.01 {
		t.Fatalf("首帧慢成员折扣 = %.4f, want %.4f", slowByTtfb, want)
	}
	// 折扣必须只削权、永不归零也不放大（>0 且 <=1）。
	for name, factor := range map[string]float64{"吞吐慢": slowByThroughput, "首帧慢": slowByTtfb} {
		if factor <= 0 || factor > 1 {
			t.Fatalf("%s 的折扣 %v 越界（必须落在 (0,1]）", name, factor)
		}
	}
}

func TestMemberSpeedFactorDisabledByZeroWeight(t *testing.T) {
	resetMemberSpeedForTest()
	for i := 0; i < 3; i++ {
		recordMemberSpeed(120, 60000, 1, 60000) // 极慢
	}
	settings := speedTestSettings()
	settings.weightPct = 0
	if factor := memberSpeedFactor(120, settings); factor != 1 {
		t.Fatalf("强度 0 时折扣 = %v, want 1（关掉速度维度必须逐字回到旧行为）", factor)
	}
}

func TestMemberSpeedFactorIgnoresUnknownDimensions(t *testing.T) {
	resetMemberSpeedForTest()
	// 只有首帧、没有 usage（吞吐未知）: 不能因为"吞吐为 0"就判成大慢。
	for i := 0; i < 3; i++ {
		recordMemberSpeed(130, 400, 0, 0)
	}
	settings := speedTestSettings()
	if factor := memberSpeedFactor(130, settings); factor != 1 {
		t.Fatalf("缺吞吐数据时折扣 = %v, want 1（未知维度不参与打分）", factor)
	}
	// 阈值关掉时同理：首帧再慢也不打折。
	settings.slowTtfbMs = 0
	for i := 0; i < 3; i++ {
		recordMemberSpeed(131, 30000, 0, 0)
	}
	if factor := memberSpeedFactor(131, settings); factor != 1 {
		t.Fatalf("首帧阈值 0 时折扣 = %v, want 1", factor)
	}
}

func TestFirstEventBudgetTightensOnlyAfterEnoughSamples(t *testing.T) {
	resetMemberSpeedForTest()
	settings := speedTestSettings()
	configured := int64(30000)

	// 没有样本: 原样返回配置（第一次用某成员时行为与改造前逐字一致）。
	if budget := firstEventBudget(configured, 140, settings); budget != configured {
		t.Fatalf("无样本时预算 = %d, want %d", budget, configured)
	}
	// 两次样本仍不足: 原样。
	recordMemberSpeed(140, 200, 0, 0)
	recordMemberSpeed(140, 200, 0, 0)
	if budget := firstEventBudget(configured, 140, settings); budget != configured {
		t.Fatalf("样本不足时预算 = %d, want %d", budget, configured)
	}
	// 三次样本、首帧 200ms → 200×4=800 < 下限 5000 → 抬到 5000。
	recordMemberSpeed(140, 200, 0, 0)
	if budget := firstEventBudget(configured, 140, settings); budget != 5000 {
		t.Fatalf("首帧很快时预算 = %d, want 5000（下限兜底, 否则一个 40ms 的偶发样本会把预算压到几百毫秒）", budget)
	}
}

func TestFirstEventBudgetNeverExceedsConfigured(t *testing.T) {
	resetMemberSpeedForTest()
	settings := speedTestSettings()
	// 首帧 3s × 4 = 12s，配置只有 8s → 取配置（绝不能放宽用户配的上限）。
	for i := 0; i < 3; i++ {
		recordMemberSpeed(150, 3000, 0, 0)
	}
	if budget := firstEventBudget(8000, 150, settings); budget != 8000 {
		t.Fatalf("预算 = %d, want 8000（不得超过分组配置）", budget)
	}
	// 配置放宽到 30s → 这时才轮到"实测 × 倍数"生效: 12s。
	if budget := firstEventBudget(30000, 150, settings); budget != 12000 {
		t.Fatalf("预算 = %d, want 12000", budget)
	}
	// 关闭开关（倍数 0）→ 永远原样返回配置。
	settings.feMultiple = 0
	if budget := firstEventBudget(30000, 150, settings); budget != 30000 {
		t.Fatalf("关闭后预算 = %d, want 30000", budget)
	}
}

func TestFirstEventBudgetDurationKeepsMilliseconds(t *testing.T) {
	resetMemberSpeedForTest()
	settings := speedTestSettings()
	settings.feFloorMs = 1500
	for i := 0; i < 3; i++ {
		recordMemberSpeed(160, 100, 0, 0)
	}
	// 毫秒级下限不能被"秒"取整吃掉: 1.5s 就该是 1.5s。
	if got := firstEventBudgetDuration(30*time.Second, 160, settings); got != 1500*time.Millisecond {
		t.Fatalf("预算 = %v, want 1.5s", got)
	}
}

// TestAllocatePlanAppliesSpeedDiscount 是本轮的"接线判据": 上面那些用例都只证明
// memberSpeedFactor 算得对, 但折扣有没有真的接进分压（allocatePlan → allocateWeights）是另一回事。
// 装置: 两个成员剩余额度完全相同（包月 1000 已用 500 → 各剩 500 次）, 因此不给折扣时权重必须相等;
// 把成员 11 量成慢（首帧 15s / 吞吐 ~0.13 tok/s）后它的权重必须更低, 而关掉速度强度后两权重又必须相等。
// 反向对照证明差异只来自速度维度, 不是"两边本来就不一样"。
func TestAllocatePlanAppliesSpeedDiscount(t *testing.T) {
	resetMemberSpeedForTest()
	defer resetMemberSpeedForTest()
	in := allocateInput{
		tokens:        1000,
		pointsPerUnit: model.BalanceDefaultPointsPerUnit,
		settings:      allocateSettings{tokens: 1000, healthWeight: 0, minRequests: 1, speed: speedTestSettings()},
	}
	// 两个成员剩余请求数完全相同（都剩 500 次）。
	quota := model.ChannelBilling{MonthlyQuota: 1000, MonthlyUsed: 500, BalanceKnown: true}
	deps := allocateTestDeps{billing: map[int]model.ChannelBilling{11: quota, 22: quota}}.deps()
	items := []model.GroupItem{{ID: 11, Priority: 1, Available: true}, {ID: 22, Priority: 2, Available: true}}

	even, _, _, err := allocatePlan(items, nil, relayNowMs(), nil, deps, in)
	if err != nil {
		t.Fatalf("未量速度时应能算出权重: %v", err)
	}
	if weights := allocateWeights(even, []float64{1, 1}); weights[0] != weights[1] {
		t.Fatalf("同余额成员的基础权重应相等, got %v", weights)
	}

	// 把成员 11 量成慢: 首帧 15s（阈值 3s）、吞吐 2 token / 15s ≈ 0.13 tok/s（阈值 8）。
	for i := 0; i < 3; i++ {
		recordMemberSpeed(11, 15000, 2, 15000)
	}
	slow, factors, _, err := allocatePlan(items, nil, relayNowMs(), nil, deps, in)
	if err != nil {
		t.Fatalf("慢成员不该被剔除（只削权）: %v", err)
	}
	if len(slow) != 2 {
		t.Fatalf("慢成员仍应在分配里, got %d 个成员", len(slow))
	}
	weights := allocateWeights(slow, factors)
	if weights[0] >= weights[1] {
		t.Fatalf("慢成员权重应低于同余额的快成员, got %v (factors=%v)", weights, factors)
	}

	// 否定式对照: 速度强度归零 → 权重回到相等, 差异确实只来自速度维度。
	in.settings.speed.weightPct = 0
	off, offFactors, _, err := allocatePlan(items, nil, relayNowMs(), nil, deps, in)
	if err != nil {
		t.Fatalf("关掉速度维度后应能算出权重: %v", err)
	}
	if offWeights := allocateWeights(off, offFactors); offWeights[0] != offWeights[1] {
		t.Fatalf("关掉速度维度后权重应相等, got %v", offWeights)
	}
}

func TestCompletionTokensIgnoresPromptAndNil(t *testing.T) {
	if got := completionTokens(nil); got != 0 {
		t.Fatalf("nil usage = %d, want 0", got)
	}
	// 只有输出 token 计入吞吐: 长提示的请求不能因为 prompt 大而"看起来快"。
	usage := llm.Usage{PromptTokens: 100000, CompletionTokens: 42}
	if got := completionTokens(&usage); got != 42 {
		t.Fatalf("completionTokens = %d, want 42", got)
	}
	// 负数（上游脏数据）归零, 不能把吞吐算成负的。
	negative := llm.Usage{CompletionTokens: -5}
	if got := completionTokens(&negative); got != 0 {
		t.Fatalf("负数 completionTokens = %d, want 0", got)
	}
}
