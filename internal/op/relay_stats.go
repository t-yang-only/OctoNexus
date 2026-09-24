package op

import (
	"context"
	"sort"

	"github.com/bestruirui/octopus/internal/model"
)

// T-usability-008 真实通过率统计。
//
// ## 为什么不能直接算 success / total
//
// 实测：senseaudio 一度显示通过率 31.6%，看起来像渠道坏了 ——
// 实际上那 25 次失败全是「用 chat 接口去调 TTS/图像等专用模型」造成的**请求非法**，
// 跟渠道没有任何关系。把两类混在一起，用户会去修一个根本没坏的东西。
//
// 所以这里把失败按归因分桶（口径来自 relay 的失败处置分类，落库在 relay_logs.fault_kind）：
//
//	request   请求本身非法 —— **不计入渠道通过率**
//	member    成员自身问题 —— 计入
//	transient 可恢复       —— 计入
//
// 于是有两个数，它们回答不同的问题：
//
//	SuccessRate  成功 / 全部              —— "用户发起的请求有多少成功了"
//	ChannelRate  成功 / (成功+渠道故障)   —— "这个渠道本身健康吗"
//
// 两个都需要：前者是体验，后者是诊断。只给一个必然误导一半场景。
type RelayLogFaultCounts struct {
	// Success 是成功条数。
	Success int64 `json:"success"`
	// Canceled 是取消条数（客户端主动断开），既不算成功也不算失败。
	Canceled int64 `json:"canceled"`
	// RequestFault 是「请求本身非法」条数：换任何成员都会同样失败。
	RequestFault int64 `json:"request_fault"`
	// MemberFault 是「成员自身问题」条数（凭据无效、无权限、模型不存在）。
	MemberFault int64 `json:"member_fault"`
	// TransientFault 是可恢复失败条数（超时、限流、5xx、网络）。
	TransientFault int64 `json:"transient_fault"`
	// Unclassified 是失败但未归类的条数（升级前的存量行没有 fault_kind）。
	// 单独列出来而不是并进某一类：**不知道的不能猜**，
	// 否则历史数据会把某个渠道的通过率拉低，而原因根本不在它身上。
	Unclassified int64 `json:"unclassified"`
}

// Total 是全部计入统计的条数（含取消）。
func (c RelayLogFaultCounts) Total() int64 {
	return c.Success + c.Canceled + c.RequestFault + c.MemberFault + c.TransientFault + c.Unclassified
}

// ChannelFaults 是某个渠道的失败构成与两个通过率。
type ChannelFaults struct {
	Channel string `json:"channel"`
	RelayLogFaultCounts
	// SuccessRate 成功 / 全部（含请求非法与取消）—— 用户视角的体验。
	SuccessRate float64 `json:"success_rate"`
	// ChannelRate 成功 / (成功 + 渠道故障) —— **渠道健康度**，排除了请求本身的问题。
	// 分母为 0（没有可归因于渠道的结果）时为 0，由调用方按 Total 判断是否有意义。
	ChannelRate float64 `json:"channel_rate"`
}

// RelayLogFaultsSummary 是跨渠道汇总。
type RelayLogFaultsSummary struct {
	RelayLogFaultCounts
	// Window 是统计覆盖的条数（等于 Total，单独给出便于前端直接展示）。
	Window int64 `json:"window"`
	// Sample 说明这批样本的来源（窗口原始条数、剔除的测试请求数、是否触限）。
	Sample RelayLogSampleInfo `json:"sample"`
	// Channels 按总条数倒序。
	Channels []ChannelFaults `json:"channels"`
}

// RelayLogFaultsStats 统计失败构成与通过率。
//
// window 取最近 N 条日志（按 id 倒序）—— 用条数而不是天数：
// 部署后流量差异极大，按天取会让"最近一天只有 3 条"的渠道得出没有意义的比例。
func RelayLogFaultsStats(ctx context.Context, window int) (RelayLogFaultsSummary, error) {
	rows, sample, err := relayLogWindow(ctx, window)
	if err != nil {
		return RelayLogFaultsSummary{}, err
	}

	var summary RelayLogFaultsSummary
	summary.Sample = sample
	byChannel := make(map[string]*ChannelFaults)
	for _, row := range rows {
		target := &summary.RelayLogFaultCounts
		if row.TargetChannel != "" {
			if _, ok := byChannel[row.TargetChannel]; !ok {
				byChannel[row.TargetChannel] = &ChannelFaults{Channel: row.TargetChannel}
			}
			// 渠道维度与全局维度分别累计：同一个渠道的行既要进它的桶，也要进总数。
			applyFaultCount(target, row)
			applyFaultCount(&byChannel[row.TargetChannel].RelayLogFaultCounts, row)
			continue
		}
		// 没有目标渠道（分组不存在、未发起上游请求）只进全局。
		applyFaultCount(target, row)
	}

	summary.Window = summary.Total()
	summary.Channels = make([]ChannelFaults, 0, len(byChannel))
	for _, ch := range byChannel {
		ch.SuccessRate = ratio(ch.Success, ch.Total())
		ch.ChannelRate = ratio(ch.Success, ch.Success+ch.MemberFault+ch.TransientFault)
		summary.Channels = append(summary.Channels, *ch)
	}
	sort.Slice(summary.Channels, func(i, j int) bool {
		if summary.Channels[i].Total() != summary.Channels[j].Total() {
			return summary.Channels[i].Total() > summary.Channels[j].Total()
		}
		return summary.Channels[i].Channel < summary.Channels[j].Channel
	})
	return summary, nil
}

func applyFaultCount(c *RelayLogFaultCounts, row model.RelayLog) {
	switch row.Status {
	case "success":
		c.Success++
		return
	case "canceled":
		c.Canceled++
		return
	}
	// 其余按失败处理 —— 用 fault_kind 归因，未分类的单独记。
	switch row.FaultKind {
	case "request":
		c.RequestFault++
	case "member":
		c.MemberFault++
	case "transient":
		c.TransientFault++
	default:
		c.Unclassified++
	}
}

// ratio 返回百分比（0..100）。分母为 0 时返回 0 —— 而不是 NaN：
// NaN 会让 JSON 序列化失败或在前端显示成 "NaN%"，那比 0 更难排查。
func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) * 100 / float64(denominator)
}
