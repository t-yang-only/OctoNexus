package relay

import (
	"math"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/looplj/axonhub/llm"
)

// 速度观测与速度应对（T-speed-001）。
//
// 用户口径：「还有一些限流和速度不是很好你都要给我做出应对的办法」。限流那条走 throttle.go，
// 速度这条走这里。三条口径：
//
//  1. **速度要用可观测事实描述，不用感觉**：两个事实就够——首帧耗时（TTFB，只有流式才有）
//     与输出吞吐（token/s，取上游 usage 的 completion tokens ÷ 本轮耗时）。这两件事决定了
//     「等多久才开始出字」和「出字多快」，正是用户嘴里"速度不好"的两半。
//  2. **样本不足就不下结论**：不足 memberSpeedMinSamples 次一律按"未知"处理（不打折、不放宽，
//     也不收紧看门狗）。否则一次抖动就能把好成员按下去，而且再也翻不了身。
//  3. **应对分两类，边界不同**：
//     - 折扣（memberSpeedFactor）只削减分压权重，永不剔除——剔除是冷却的职责（见 throttle.go 同一口径）；
//     - 看门狗（firstEventBudget）会真的提前放弃本轮：它只拿**该成员自己最近量出来的首帧**放大若干倍
//     当预算，结果永远**不大于**分组配置（只会更早放弃，绝不比用户配的更宽松）。
//
// 时间窗口与其它窗口一致：进程内、不落库、不开 goroutine（重启即归零，靠实测重新建立）。
const (
	// memberSpeedWindowMs 是速度窗口：默认 2 分钟。比质量窗口（5 分钟）短——速度是"最近状态"，
	// 上游限速/降级往往几分钟内就变；比负载窗口（1 分钟）长——首帧与吞吐都需要几次样本才稳。
	memberSpeedWindowMs int64 = 120_000
	// memberSpeedMinSamples 是"敢下结论"的最小样本数。
	memberSpeedMinSamples int64 = 3
)

// memberSpeedSample 是一个成员在当前窗口内的速度账。
type memberSpeedSample struct {
	samples  int64 // 窗口内成功尝试次数（含没有 usage 的）
	ttfbMs   int64 // 最近一次流式首帧耗时（<=0 表示还没量到：该成员本次窗口内没有流式成功样本）
	tokenOut int64 // 窗口内累计输出 token（只累计有 usage 的尝试）
	genMs    int64 // 与 tokenOut 同口径的耗时合计（毫秒）
	windowAt int64 // 窗口起点
}

var (
	memberSpeedMu      sync.Mutex
	memberSpeedSamples = make(map[int]memberSpeedSample)
)

// recordMemberSpeed 记一次速度观测。
//
// ttfbMs 只有流式首帧才有真值（非流式传 0：整轮耗时里混着生成时间，不能当首帧用）；
// outTokens/elapsedMs 在拿不到上游 usage 时传 0（此时只更新样本数与首帧，不污染吞吐）。
func recordMemberSpeed(itemID int, ttfbMs, outTokens, elapsedMs int64) {
	if itemID == 0 {
		return
	}
	now := relayNowMs()
	memberSpeedMu.Lock()
	defer memberSpeedMu.Unlock()

	sample := memberSpeedSamples[itemID]
	if sample.windowAt == 0 || now-sample.windowAt > memberSpeedWindowMs {
		sample = memberSpeedSample{windowAt: now}
	}
	sample.samples++
	if ttfbMs > 0 {
		sample.ttfbMs = ttfbMs
	}
	if outTokens > 0 && elapsedMs > 0 {
		sample.tokenOut += outTokens
		sample.genMs += elapsedMs
	}
	memberSpeedSamples[itemID] = sample
}

// speedReading 是一个成员此刻的速度读数（面板展示与选路共用同一份）。
type speedReading struct {
	Samples      int64   `json:"samples"`        // 窗口内成功样本数
	TtfbMs       int64   `json:"ttfb_ms"`        // 最近一次流式首帧耗时（0 = 还没量到）
	TokensPerSec float64 `json:"tokens_per_sec"` // 窗口内的输出吞吐（0 = 还没量到）
	Ready        bool    `json:"ready"`          // 样本是否够下结论（不足时选路按未知处理）
}

// memberSpeedReading 读一个成员的速度账；ok=false 表示窗口内没有任何样本。
// Ready 只在样本足够时为真——它是"敢不敢用这份读数下结论"的唯一开关。
func memberSpeedReading(itemID int) (speedReading, bool) {
	now := relayNowMs()
	memberSpeedMu.Lock()
	sample, seen := memberSpeedSamples[itemID]
	stale := !seen || sample.windowAt == 0 || now-sample.windowAt > memberSpeedWindowMs
	memberSpeedMu.Unlock()
	if stale {
		return speedReading{}, false
	}
	reading := speedReading{Samples: sample.samples, TtfbMs: sample.ttfbMs}
	if sample.tokenOut > 0 && sample.genMs > 0 {
		reading.TokensPerSec = float64(sample.tokenOut) * 1000 / float64(sample.genMs)
	}
	reading.Ready = sample.samples >= memberSpeedMinSamples
	return reading, true
}

// resetMemberSpeedForTest 清空速度账（单测专用，与其它窗口的 reset 同款）。
func resetMemberSpeedForTest() {
	memberSpeedMu.Lock()
	memberSpeedSamples = make(map[int]memberSpeedSample)
	memberSpeedMu.Unlock()
}

// speedSettings 是速度维度的设置项（缺省值与 model.DefaultSettings 一一对应）。
type speedSettings struct {
	weightPct  int     // 速度折扣强度 0..100（route_allocate_speed_weight）; 0 = 不看速度
	slowTtfbMs int64   // 首帧慢阈值毫秒（route_speed_slow_ttfb_ms）; 0 = 不按首帧判慢
	slowTps    float64 // 吞吐慢阈值 token/s（route_speed_slow_tps）; 0 = 不按吞吐判慢
	feMultiple int     // 自适应首帧看门狗倍数（route_speed_first_event_multiple）; 0 = 关闭
	feFloorMs  int64   // 自适应看门狗下限毫秒（route_speed_first_event_floor_ms）
}

const (
	defaultSpeedWeightPct  = 40
	defaultSpeedSlowTtfbMs = 3000
	defaultSpeedSlowTps    = 8.0
	defaultSpeedFeMultiple = 4
	defaultSpeedFeFloorMs  = 5000
)

// speedSettingsOf 读取速度设置；缺省/越界一律回落保守缺省（与allocateSettingsOf同一套兜底）。
func speedSettingsOf() speedSettings {
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
	return speedSettings{
		weightPct:  read(model.SettingKeyRouteSpeedWeight, defaultSpeedWeightPct, 0, 100),
		slowTtfbMs: int64(read(model.SettingKeyRouteSpeedSlowTtfb, defaultSpeedSlowTtfbMs, 0, 600_000)),
		slowTps:    float64(read(model.SettingKeyRouteSpeedSlowTPS, int(defaultSpeedSlowTps), 0, 100_000)),
		feMultiple: read(model.SettingKeyRouteSpeedFeMultiple, defaultSpeedFeMultiple, 0, 100),
		feFloorMs:  int64(read(model.SettingKeyRouteSpeedFeFloor, defaultSpeedFeFloorMs, 0, 600_000)),
	}
}

// memberSpeedFactor 返回 0..1 的速度折扣系数（1 = 不打折）。
//
// 两个信号取**最差**的那一个（与 memberHealthFactor 同款：分维取最差、不叠加，
// 叠加会让"又慢又不稳"的成员被乘两次而瞬间归零）：
//   - 首帧耗时超过 slowTtfbMs 的倍数；
//   - 输出吞吐低于 slowTps 的倍数（取倒数，与首帧同向）。
//
// 折扣用几何形式 1/slowness^w（w = 折扣强度 / 100）：slowness=2、w=0.4 时约 0.76；
// slowness=4 时约 0.57；再慢也只会趋近 0 而不是变成 0——权重还有下限兜底，成员不会被饿死。
func memberSpeedFactor(itemID int, s speedSettings) float64 {
	if s.weightPct <= 0 {
		return 1
	}
	reading, ok := memberSpeedReading(itemID)
	if !ok || !reading.Ready {
		return 1 // 样本不足：不打折（未知不惩罚，与本仓"未知按乐观先验参与"一致）
	}
	slowness := 1.0
	if s.slowTtfbMs > 0 && reading.TtfbMs > 0 {
		if ratio := float64(reading.TtfbMs) / float64(s.slowTtfbMs); ratio > slowness {
			slowness = ratio
		}
	}
	if s.slowTps > 0 && reading.TokensPerSec > 0 {
		if ratio := s.slowTps / reading.TokensPerSec; ratio > slowness {
			slowness = ratio
		}
	}
	if slowness <= 1 {
		return 1
	}
	factor := math.Pow(1/slowness, float64(s.weightPct)/100)
	if factor < 0 {
		return 0
	}
	if factor > 1 {
		return 1
	}
	return factor
}

// completionTokens 取一次上游用量的输出 token 数（nil 安全）。
//
// 只认 CompletionTokens：吞吐是"生成了多少 token ÷ 花了多久"，把 prompt token 算进来
// 会让长提示的请求看起来快得离谱（提示是并发处理的，不该计入解码速度）。
func completionTokens(usage *llm.Usage) int64 {
	if usage == nil {
		return 0
	}
	if usage.CompletionTokens < 0 {
		return 0
	}
	return usage.CompletionTokens
}

// firstEventBudget 返回本轮流式请求"等首帧"的预算。
//
// 逻辑只有一句话：拿该成员**自己最近量出来的首帧**放大 feMultiple 倍，且不超过分组配置。
// 三个边界条件都必须显式处理，否则会把好成员误杀：
//   - feMultiple <= 0 或分组配置 <= 0（关闭）：原样返回配置；
//   - 没有样本 / 样本不足 / 还没量到首帧：原样返回配置（第一次用该成员时不动它的预算）；
//   - 算出来低于下限：抬到 feFloorMs（防止"上次刚好 40ms"把预算压到几百毫秒）。
//
// 返回值永远 <= configuredMs：这个机制只会让"等不到首帧"更早被发现，绝不会比配置更宽松。
func firstEventBudget(configuredMs int64, itemID int, s speedSettings) int64 {
	if s.feMultiple <= 0 || configuredMs <= 0 {
		return configuredMs
	}
	reading, ok := memberSpeedReading(itemID)
	if !ok || !reading.Ready || reading.TtfbMs <= 0 {
		return configuredMs
	}
	budget := reading.TtfbMs * int64(s.feMultiple)
	if budget < s.feFloorMs {
		budget = s.feFloorMs
	}
	if budget > configuredMs {
		budget = configuredMs
	}
	return budget
}

// firstEventBudgetDuration 是 firstEventBudget 的时长版本（给 time.AfterFunc 用，避免毫秒→秒的取整误差）。
func firstEventBudgetDuration(configured time.Duration, itemID int, s speedSettings) time.Duration {
	budgetMs := firstEventBudget(configured.Milliseconds(), itemID, s)
	return time.Duration(budgetMs) * time.Millisecond
}
