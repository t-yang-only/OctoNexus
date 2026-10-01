package op

import (
	"context"
	"fmt"
	"sort"

	"github.com/t-yang-only/OctoNexus/internal/model"
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
// AttemptGroupBy 是尝试链聚合的**分类维度**。
//
// 三个维度的语义**刻意不同**，不是同一份数据换名：
//
//	channel  逐次尝试维度 —— 每次尝试落在哪个渠道（一次请求换过成员时，
//	         它的若干次尝试会分别落到不同渠道）。这是原有口径。
//	model    请求级维度 —— 客户端请求的模型名（= 分组名）。一次请求的
//	         所有尝试共享同一个值，回答"哪个分组在被反复试错"。
//	apikey   请求级维度 —— 调用方的 API Key 名，回答"谁在反复试错"。
//
// 请求级维度下，一次请求的 N 次尝试会全部计入同一分组的 N 次 attempts，
// 而不是只计一次 —— 否则"共尝试 N 次"这个数在三个维度之间就对不上了。
type AttemptGroupBy string

const (
	AttemptGroupByChannel AttemptGroupBy = "channel"
	AttemptGroupByModel   AttemptGroupBy = "model"
	AttemptGroupByAPIKey  AttemptGroupBy = "apikey"
)

// attemptGroupUnnamed 是请求级维度下"该字段为空"的归档名。
// 空值统一归到这里而不是丢掉 —— 丢掉会让各维度之间的"共尝试 N 次"对不上，
// 那种不一致比缺一个名字更难查。
const attemptGroupUnnamed = "(未记录)"

// AttemptGroupByValid 归一化并校验分类维度。
// 空值按 channel —— 让既有调用方（不传该参数）的行为逐字不变。
func AttemptGroupByValid(raw string) (AttemptGroupBy, bool) {
	switch AttemptGroupBy(raw) {
	case "":
		return AttemptGroupByChannel, true
	case AttemptGroupByChannel, AttemptGroupByModel, AttemptGroupByAPIKey:
		return AttemptGroupBy(raw), true
	default:
		return "", false
	}
}

// rowGroupKey 取**请求级**维度上这次请求所属的分组名。
//
// channel 维度不走这里（它按每次尝试自己的渠道分），返回 ok=false。
func rowGroupKey(row model.RelayLog, groupBy AttemptGroupBy) (string, bool) {
	switch groupBy {
	case AttemptGroupByModel:
		if row.Model == "" {
			return attemptGroupUnnamed, true
		}
		return row.Model, true
	case AttemptGroupByAPIKey:
		if row.APIKeyName == "" {
			return attemptGroupUnnamed, true
		}
		return row.APIKeyName, true
	default:
		return "", false
	}
}

// AttemptGroupStat 是某个分类取值下的尝试统计。
type AttemptGroupStat struct {
	// Name 是该维度上的取值（渠道名 / 模型名 / API Key 名）。
	Name string `json:"name"`
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
	// Sample 说明这批样本的来源（窗口原始条数、剔除的测试请求数、是否触限）。
	Sample RelayLogSampleInfo `json:"sample"`
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
	// GroupBy 是本次聚合用的分类维度，回带给界面以便显示"当前按什么分类"。
	GroupBy AttemptGroupBy `json:"group_by"`
	// Groups 按被尝试次数倒序，取值的含义由 GroupBy 决定。
	Groups []AttemptGroupStat `json:"groups"`
}

// lastErrorMaxRunes 限制落库错误原文的展示长度。
// 上游错误可能很长（有的站点返回整页 HTML），不截断会让面板撑爆。
const lastErrorMaxRunes = 200

// AttemptChainStats 聚合 window 内所有请求的尝试链。
func AttemptChainStats(ctx context.Context, window int, groupBy AttemptGroupBy) (AttemptChainSummary, error) {
	if _, ok := AttemptGroupByValid(string(groupBy)); !ok {
		return AttemptChainSummary{}, fmt.Errorf("invalid group_by: %s", groupBy)
	}
	rows, sample, err := relayLogWindow(ctx, window)
	if err != nil {
		return AttemptChainSummary{}, err
	}

	byGroup := make(map[string]*AttemptGroupStat)
	summary := AttemptChainSummary{Window: sample.Samples, Sample: sample, GroupBy: groupBy}

	// 按 id 升序遍历，让 LastError 稳定落在"最近一次"上
	// （Find 是 id DESC，这里反转 —— 否则最后一次写入的会是窗口内最早的那条）。
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if len(row.AttemptDetail) == 0 {
			continue
		}
		// 请求级维度（模型 / API Key）的归类名——一次请求的所有尝试共享同一个值，
		// 所以在这里算一次即可，不必进内层循环逐次重算。
		rowKey, rowLevel := rowGroupKey(row, groupBy)
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
			name := attempt.Channel
			if rowLevel {
				// 请求级维度：同一次请求的每次尝试都归到同一个名字下。
				name = rowKey
			}
			if name == "" {
				// 没有归类名的尝试不进任何分组维度 —— 但"这一轮失败过"这件事
				// 仍要计入 affected_requests，否则换人试错会被漏报。
				if attempt.Error != "" {
					hadFailure = true
				}
				continue
			}
			stat, ok := byGroup[name]
			if !ok {
				stat = &AttemptGroupStat{Name: name}
				byGroup[name] = stat
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

	summary.Groups = make([]AttemptGroupStat, 0, len(byGroup))
	for _, stat := range byGroup {
		summary.Groups = append(summary.Groups, *stat)
	}
	sort.Slice(summary.Groups, func(i, j int) bool {
		// 被尝试次数多的在前 —— 画像的用途是"谁在被反复试错"。
		if summary.Groups[i].Attempts != summary.Groups[j].Attempts {
			return summary.Groups[i].Attempts > summary.Groups[j].Attempts
		}
		return summary.Groups[i].Name < summary.Groups[j].Name
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
