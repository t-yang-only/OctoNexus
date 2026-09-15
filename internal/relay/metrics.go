package relay

import (
	"sync"
	"time"
)

// 成员指标（按质量 / 延迟选路的数据底座；NM-DS-004 + NM-DS-006 迭代）。
//
// 口径：
//  1. 只在成功/失败上报点旁路写计数与本次尝试耗时，不阻塞转发主路径，也不落库（进程内即可，重启清零）；
//  2. 窗口用"写入时衰减"实现——没有后台协程、没有环形缓冲：距本窗口起点超过 memberQualityWindowMs
//     就重置该成员样本，于是样本天然只反映最近一段时间的表现；
//  3. 无样本的成员一律按"乐观先验"参与排序（质量按 1.0、延迟按 0ms）：未知者先当成好的试一次，
//     试出真相后自然沉底，有界探索、不饿死任何成员；坏成员另有既有的冷却/探测链路兜底；
//  4. 样本按展平后的成员行（GroupItem.ID）聚合。同一授权挂在不同分组里各算一份——换来的是读写
//     都不需要额外关联；若要跨分组共享学习结果，应改为按 ChannelGrantID 聚合（见 ledger T-route-006）。

// memberQualityWindowMs 是成员样本的有效窗口（毫秒）。固定 300s（与设计稿
// T-research-003 的 StrategyWindowSeconds 默认值一致）；等真的需要按分组调再引入配置。
const memberQualityWindowMs int64 = 300_000

// relayNowMs 是取当前时间的唯一入口，便于单测注入时钟验证窗口衰减。
var relayNowMs = func() int64 { return time.Now().UnixMilli() }

type memberQualitySample struct {
	success  int64
	failure  int64
	latency  int64 // 最近一次尝试耗时（毫秒）；<=0 视为无延迟数据。
	windowAt int64 // 本窗口起点（毫秒）
}

var (
	memberQualityMu      sync.Mutex
	memberQualitySamples = make(map[int]memberQualitySample)
)

// recordMemberOutcome 记录一次成员尝试结果与耗时；窗口过期则开一个新窗口重新计数。
func recordMemberOutcome(itemID int, success bool, latencyMs int64) {
	if itemID == 0 {
		return
	}
	now := relayNowMs()
	memberQualityMu.Lock()
	defer memberQualityMu.Unlock()

	sample := memberQualitySamples[itemID]
	if sample.windowAt == 0 || now-sample.windowAt > memberQualityWindowMs {
		sample = memberQualitySample{windowAt: now}
	}
	if success {
		sample.success++
	} else {
		sample.failure++
	}
	if latencyMs > 0 {
		sample.latency = latencyMs
	}
	memberQualitySamples[itemID] = sample
}

// sampleLocked 取窗口内的样本；窗口过期或从未记录时 ok=false。
func sampleLocked(itemID int) (memberQualitySample, bool) {
	sample, seen := memberQualitySamples[itemID]
	if !seen || sample.windowAt == 0 || relayNowMs()-sample.windowAt > memberQualityWindowMs {
		return memberQualitySample{}, false
	}
	return sample, true
}

// MemberQualityProvider 返回成员最近窗口的成功率；ok=false 表示样本不足（排序按乐观先验处理）。
type MemberQualityProvider func(itemID int) (successRate float64, ok bool)

// memberSuccessRate 是生产用的质量实现。
func memberSuccessRate(itemID int) (float64, bool) {
	if itemID == 0 {
		return 0, false
	}
	memberQualityMu.Lock()
	defer memberQualityMu.Unlock()

	sample, ok := sampleLocked(itemID)
	if !ok {
		return 0, false
	}
	total := sample.success + sample.failure
	if total == 0 {
		return 0, false
	}
	return float64(sample.success) / float64(total), true
}

// memberLatencyMs 是生产用的延迟实现：返回窗口内最近一次尝试的耗时。
// 只记录耗时大于 0 的尝试，故"没测到耗时"与"没有样本"同为 ok=false。
func memberLatencyMs(itemID int) (int64, bool) {
	if itemID == 0 {
		return 0, false
	}
	memberQualityMu.Lock()
	defer memberQualityMu.Unlock()

	sample, ok := sampleLocked(itemID)
	if !ok || sample.latency <= 0 {
		return 0, false
	}
	return sample.latency, true
}

// resetMemberQualityForTest 清空样本并在用例结束后复位时钟；仅测试用。
func resetMemberQualityForTest() {
	memberQualityMu.Lock()
	memberQualitySamples = make(map[int]memberQualitySample)
	memberQualityMu.Unlock()
}
