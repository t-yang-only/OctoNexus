package relay

import (
	"sync"
	"time"
)

// 按质量自动切换（NM-DS-004 迭代）：成员最近窗口内的成败样本。
//
// 口径：
//  1. 只在成功/失败上报点旁路写一个计数，不阻塞转发主路径，也不落库（进程内即可，重启清零）；
//  2. 窗口用"写入时衰减"实现——没有后台协程、没有环形缓冲：距本窗口起点超过
//     memberQualityWindowMs 就重置该成员样本，于是样本天然只反映最近一段时间的表现；
//  3. 排序时"无样本"按中性先验 1.0 处理（乐观）：新成员照样会被尝试，失败过的成员
//     自然沉到已验证成员之后；坏成员另由既有的冷却/探测链路兜底；
//  4. 样本只按成员（GroupItem.ID，即展平后的成员行）聚合，不区分分组：同一成员在多个分组里
//     的表现是同一件事，分开统计只会让样本更稀。

// memberQualityWindowMs 是质量样本的有效窗口（毫秒）。固定 300s（与设计稿
// T-research-003 的 StrategyWindowSeconds 默认值一致）；等真的需要按分组调再引入配置。
const memberQualityWindowMs int64 = 300_000

// relayNowMs 是取当前时间的唯一入口，便于单测注入时钟验证窗口衰减。
var relayNowMs = func() int64 { return time.Now().UnixMilli() }

type memberQualitySample struct {
	success  int64
	failure  int64
	windowAt int64 // 本窗口起点（毫秒）
}

var (
	memberQualityMu      sync.Mutex
	memberQualitySamples = make(map[int]memberQualitySample)
)

// recordMemberOutcome 记录一次成员尝试结果；窗口过期则开一个新窗口重新计数。
func recordMemberOutcome(itemID int, success bool) {
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
	memberQualitySamples[itemID] = sample
}

// MemberQualityProvider 返回成员最近窗口的成功率；ok=false 表示样本不足（排序按中性先验处理）。
type MemberQualityProvider func(itemID int) (successRate float64, ok bool)

// memberSuccessRate 是生产用的质量实现。
func memberSuccessRate(itemID int) (float64, bool) {
	if itemID == 0 {
		return 0, false
	}
	now := relayNowMs()
	memberQualityMu.Lock()
	defer memberQualityMu.Unlock()

	sample, seen := memberQualitySamples[itemID]
	if !seen || sample.windowAt == 0 || now-sample.windowAt > memberQualityWindowMs {
		return 0, false
	}
	total := sample.success + sample.failure
	if total == 0 {
		return 0, false
	}
	return float64(sample.success) / float64(total), true
}

// resetMemberQualityForTest 清空样本并在用例结束后复位时钟；仅测试用。
func resetMemberQualityForTest() {
	memberQualityMu.Lock()
	memberQualitySamples = make(map[int]memberQualitySample)
	memberQualityMu.Unlock()
}
