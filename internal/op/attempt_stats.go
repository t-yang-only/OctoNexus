package op

import (
	"context"
	"sort"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-trace-002 尝试链的聚合视图。
//
// ## 补的是哪个盲区
//
// 现有的所有统计都只看**最终结果**：
//
//	relay_stats（fault-stats）  只看最终失败的请求，按 target_channel 归因
//	channel_latency / group_latency  只看最终走到哪个渠道/分组的耗时
//
// 于是有一整类事实**在任何视图里都不存在**：一次**成功**的请求里，
// 成员 A 失败、成员 B 接手成功了 —— A 的那次失败不进 fault-stats（那条日志 status=success），
// 也不进任何渠道画像（画像记的是 B）。用户看到的是"成功率 100%"，而实际上
// 每次请求都在某个成员上白等一次。
//
// 生产实测里这不是理论问题：分组 `Max-flash` 有 40 个成员，正常请求走了 1 轮，
// 而一旦某个成员凭据失效，它会**每次都被试一遍再跳过** —— 用户只能看到延迟
// 从 1.5s 变成 5.5s，看不出是"有个成员在每次都被重试"。
//
// ## 口径
//
// 逐个解析 window 内的日志的 attempt_detail（JSON），把**每一轮**都算一次：
//
//	Attempts      该渠道被尝试的次数
//	Failures      其中失败的次数
//	RequestFault  失败里「请求本身非法」—— 换个成员也一样结局
//	MemberFault   失败里「成员自身问题」（凭据/权限/模型不存在）
//	TransientFault 失败里「可恢复」（超时/限流/5xx/网络）
//	LastError     最近一次失败的原文（截断，便于定位）
//
// **成功的轮次也计入 Attempts**：只统计失败轮会让"某个成员成功率 100%"
// 和"从没被试过"长得一样。分母是尝试次数，不是请求数。
type AttemptChannelStat struct {
	Channel string `json:"channel"`
	// Attempts 是被尝试的轮数（含成功的轮）。
	Attempts int64 `json:"attempts"`
	// Failures 是失败的轮数。
	Failures int64 `json:"failures"`
	// Successes 是成功的轮数（正常每请求最多 1 次，因为成功即结束）。
	Successes int64 `json:"successes"`
	// 三类失败归因，口径与 relay_logs.fault_kind 一致。
	RequestFault   int64 `json:"request_fault"`
	MemberFault    int64 `json:"member_fault"`
	TransientFault int64 `json:"transient_fault"`
	// UnclassifiedFault 是失败但没带归因的轮次（升级前的存量行）。
	// 单独计而不是并进某一类：**不知道的不能猜**。
	UnclassifiedFault int64 `json:"unclassified_fault"`
	// LastError 是最近一次失败的原文（已截断，只用于定位）。
	LastError string `json:"last_error,omitempty"`
	// LastErrorLogID 是最近一次失败轮所在日志的 id，便于回溯到具体那条日志。
	LastErrorLogID uint64 `json:"last_error_log_id,omitempty"`
}

// AttemptChainSummary 是尝试链聚合的整体结果。
type AttemptChainSummary struct {
	// Window 是统计扫过的日志条数。
	Window int64 `json:"window"`
	// Scanned 是其中**带尝试链**的日志条数。
	// 与 Window 的差是升级前的存量行（没有 attempt_detail），
	// 单独给出是为了让"统计为空"和"没有数据"能被区分开。
	Scanned int64 `json:"scanned"`
	// Truncated 是链被截断过的日志条数 —— 这类日志的轮次不完整，
	// 聚合值会偏低，必须让读者知道。
	Truncated int64 `json:"truncated"`
	// MultiRoundRequests 是发生过换人（>1 轮）的请求数。
	MultiRoundRequests int64 `json:"multi_round_requests"`
	// AffectedRequests 是有过**失败轮**的请求数。
	//
	// 这个数字与 fault-stats 的失败数**刻意不同**：这里的请求最终可能是成功的。
	// 两者相减就是"试错但最终成功"的请求量 —— 正是这个视图存在的理由。
	AffectedRequests int64 `json:"affected_requests"`
	// Channels 按被尝试次数倒序。
	Channels []AttemptChannelStat `json:"channels"`
}

// lastErrorMaxRunes 限制落库错误原文的展示长度。
// 上游错误可能很长（有的站点返回整页 HTML），不截断会让面板撑爆。
const lastErrorMaxRunes = 200

// AttemptChainStats 聚合 window 内所有请求的尝试链。
func AttemptChainStats(ctx context.Context, window int) (AttemptChainSummary, error) {
	if window <= 0 {
		window = 500
	}
	if window > 20000 {
		window = 20000
	}
	conn := db.GetDB()

	var rows []model.RelayLog
	if err := conn.WithContext(ctx).
		Order("id DESC").Limit(window).
		Find(&rows).Error; err != nil {
		return AttemptChainSummary{}, err
	}

	byChannel := make(map[string]*AttemptChannelStat)
	summary := AttemptChainSummary{Window: int64(len(rows))}

	// 按 id 升序遍历，让 LastError 稳定落在"最近一次"上
	// （Find 是 id DESC，这里反转 —— 否则最后一次写入的会是窗口内最早的那条）。
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if len(row.AttemptDetail) == 0 {
			continue
		}
		summary.Scanned++
		if row.AttemptsTruncated {
			summary.Truncated++
		}
		if len(row.AttemptDetail) > 1 {
			summary.MultiRoundRequests++
		}
		hadFailure := false
		for _, attempt := range row.AttemptDetail {
			// 没写渠道名的轮次不进渠道维度（无法归因到某个渠道），
			// 但仍要参与「这个请求有没有失败过」的判断 ——
			// 判据是**有没有错误**，不是有没有归因（同下面空归因分支的理由）。
			if attempt.Channel == "" {
				if attempt.Error != "" {
					hadFailure = true
				}
				continue
			}
			stat, ok := byChannel[attempt.Channel]
			if !ok {
				stat = &AttemptChannelStat{Channel: attempt.Channel}
				byChannel[attempt.Channel] = stat
			}
			stat.Attempts++
			if attempt.FaultKind == "" {
				// 空归因要分两种，**不能一律当成成功**：
				//   无 Error —— 这一轮成功了（正常结束，没有错误）。
				//   有 Error —— 失败但没有归因（升级前的存量轮，或归因写入缺席）。
				//
				// 把后者当成成功会让"归因缺失的失败轮"从统计里凭空蒸发 ——
				// 用户看到的总轮次少于实际发生数，且失败率被压低。
				// 它们归入 UnclassifiedFault（不知道的不能猜成某一类），
				// 但必须计进 Failures 与 hadFailure。
				if attempt.Error == "" {
					stat.Successes++
					continue
				}
				hadFailure = true
				stat.Failures++
				stat.UnclassifiedFault++
				stat.LastError = truncateRunes(attempt.Error, lastErrorMaxRunes)
				stat.LastErrorLogID = row.ID
				continue
			}
			hadFailure = true
			stat.Failures++
			switch attempt.FaultKind {
			case "request":
				stat.RequestFault++
			case "member":
				stat.MemberFault++
			case "transient":
				stat.TransientFault++
			default:
				// 未来新增归因取值时走这里：不认识的一律记未分类，
				// 而不是悄悄并进 transient 把渠道故障率算错。
				stat.UnclassifiedFault++
			}
			stat.LastError = truncateRunes(attempt.Error, lastErrorMaxRunes)
			stat.LastErrorLogID = row.ID
		}
		if hadFailure {
			summary.AffectedRequests++
		}
	}

	summary.Channels = make([]AttemptChannelStat, 0, len(byChannel))
	for _, stat := range byChannel {
		summary.Channels = append(summary.Channels, *stat)
	}
	sort.Slice(summary.Channels, func(i, j int) bool {
		// 被尝试次数多的在前 —— 画像的用途是"谁在被反复试错"。
		if summary.Channels[i].Attempts != summary.Channels[j].Attempts {
			return summary.Channels[i].Attempts > summary.Channels[j].Attempts
		}
		return summary.Channels[i].Channel < summary.Channels[j].Channel
	})
	return summary, nil
}

// truncateRunes 按字符（而非字节）截断，避免把中文/emoji 切成半个字符。
func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
