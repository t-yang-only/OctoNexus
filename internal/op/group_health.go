package op

import (
	"context"
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// T-insight-008 分组健康画像：哪些分组在坏，坏在哪个成员。
//
// ## 为什么既有的画像都不够
//
// 已有的画像都在回答「多快 / 多少 / 走了谁」：渠道延迟画像按渠道看快慢、
// 分组延迟画像按分组看快慢、用量分析按模型/客户端/渠道看流量、选路画像看模式与判定、
// 尝试链看单次请求试了几轮。但**没有一个回答「这个分组还能不能用」**：
//   - 分组延迟画像里「样本 3 条、中位 200ms」看着不错，其实那 3 条可能全是失败
//     —— 失败的行同样有耗时，延迟画像看不见它们失败了；
//   - 用量分析里「请求 0」既可能是没人用，也可能是刚坏掉没人敢用，界面长得一模一样。
//
// 生产实测动机：验收时反复撞到 `53HK/Auto-Model 无可用成员` —— 分组成员都在冷却
// 或全部不可用，请求必然 502。这类**分组级的结构性故障**在既有面板上完全不可见：
// 渠道故障统计只统计「走到了渠道」的请求，而这类请求根本没走到渠道（没有 target_channel），
// 于是它只在全局的 Unclassified 里留下一个影子，看不出是哪个分组。
//
// ## 状态判定的四条纪律
//
//  1. **配置问题优先于流量问题**：没有成员（empty）、成员全部不可用（no_member）
//     与「有没有被调用」无关 —— 一个没人用的空分组仍然是坏的，不该混进「没问题」。
//  2. **空闲不是健康**：窗口内没有请求的分组是 `idle`，绝不显示成 healthy。
//     把「没人用」读成「一切正常」是这类面板最危险的错觉。
//  3. **成功率的分母要能解释**：分母排除「请求本身非法」（那是客户端的错，
//     放进来会让所有分组一起变差、而它们与分组无关）与「客户端取消」（谁也没失败），
//     只保留「与这个分组能否服务有关」的样本。四档故障计数原样给出，界面可见。
//  4. **判定阈值随结果返回**：界面直接显示判定线，不靠前端各写一套（会漂移）。
type GroupHealthChannel struct {
	// Channel 与 Model 是失败落在的那个上游目标；两者可能为空
	// （分组存在但没走到渠道，如「无可用成员」），空值照常列出，不丢行。
	Channel       string `json:"channel"`
	Model         string `json:"model"`
	Failures      int64  `json:"failures"`
	LastFailureAt string `json:"last_failure_at"`
}

// GroupHealthRow 是单个分组的健康事实。
type GroupHealthRow struct {
	GroupID int    `json:"group_id"`
	Name    string `json:"name"`
	Mode    string `json:"mode"`
	// Deleted 标记「分组已被删除，但日志还在保留期内」——与分组延迟画像同一约定，
	// 单个布尔而不是让前端比对占位名字符串（改一次文案就失效）。
	Deleted bool `json:"deleted"`
	// MemberCount / AvailableCount 取**顶层成员**口径（界面上的成员列表就是这个粒度）：
	// 子分组成员的可用性已由 op.groupSnapshot 按子树回填，所以这里不必再展平一次。
	// 两者必须一起看 —— 「有 40 个成员」和「40 个里只剩 3 个能用」是完全不同的处境。
	MemberCount    int `json:"member_count"`
	AvailableCount int `json:"available_count"`
	// Requests 是窗口内该分组的全部请求数（含取消与非法请求），是下面各档的合计。
	Requests int64 `json:"requests"`
	// RelayLogFaultCounts 与全局故障画像同源（applyFaultCount），四档口径不分叉。
	RelayLogFaultCounts
	// SuccessRate 的分母是「与分组服务能力有关的样本」，见文件头纪律 3。
	SuccessRate float64 `json:"success_rate"`
	// SuccessSamples 是 SuccessRate 的分母，必须一起返回：没有它，
	// 「成功率 0%」无法区分「全都失败」与「一个相关样本都没有」。
	SuccessSamples int64  `json:"success_samples"`
	LastFailureAt  string `json:"last_failure_at"`
	State          string `json:"state"`
	// FailingChannels 是失败落在哪些上游上（按失败数降序，上限 groupHealthChannelLimit）。
	// 这是「换个成员就能好」与「整条链路都在坏」的分界线。
	FailingChannels []GroupHealthChannel `json:"failing_channels"`
}

// GroupHealthSummary 是分组健康画像的完整结果。
type GroupHealthSummary struct {
	Window int64              `json:"window"`
	Sample RelayLogSampleInfo `json:"sample"`
	// 判定线随结果返回（纪律 4）。
	FailingThreshold  float64 `json:"failing_threshold"`
	DegradedThreshold float64 `json:"degraded_threshold"`
	// 各状态的分组数：让「418 个分组里到底有几个该管」一眼可见。
	FailingCount  int              `json:"failing_count"`
	NoMemberCount int              `json:"no_member_count"`
	DegradedCount int              `json:"degraded_count"`
	EmptyCount    int              `json:"empty_count"`
	IdleCount     int              `json:"idle_count"`
	HealthyCount  int              `json:"healthy_count"`
	Groups        []GroupHealthRow `json:"groups"`
}

// 分组健康状态。取值刻意用英文枚举（与分组模式、故障归因一致），由前端映射文案。
const (
	// GroupStateFailing：窗口内有请求，成功率低于 FailingThreshold。
	GroupStateFailing = "failing"
	// GroupStateNoMember：有成员但一个都不可用 —— 请求必然失败，与流量无关。
	GroupStateNoMember = "no_member"
	// GroupStateDegraded：成功率低于 DegradedThreshold 但还没到 failing。
	GroupStateDegraded = "degraded"
	// GroupStateEmpty：分组没有任何成员（配置上就是空的）。
	GroupStateEmpty = "empty"
	// GroupStateIdle：窗口内没有请求。**不是健康**（纪律 2）。
	GroupStateIdle = "idle"
	// GroupStateHealthy：有请求且成功率达标。
	GroupStateHealthy = "healthy"
)

const (
	// groupHealthFailingRate：成功率低于它算故障。
	groupHealthFailingRate = 50.0
	// groupHealthDegradedRate：成功率低于它算退化。90% 是「十次里错一次」，
	// 对网关来说已经足以让人感觉到「这个分组最近不太对」。
	groupHealthDegradedRate = 90.0
	// groupHealthChannelLimit 是失败渠道下钻的条数上限：只让人看见主要矛盾，
	// 全量明细应当去日志页按分组筛（那里有完整的每一行）。
	groupHealthChannelLimit = 5
	// groupHealthDeletedName 是日志里出现、配置里已不存在的分组的占位名。
	groupHealthDeletedName = "(已删除的分组)"
)

// groupHealthTarget 是失败落点桶（内部累计，输出时转成 GroupHealthChannel）。
type groupHealthTarget struct {
	channel       string
	model         string
	failures      int64
	lastFailureAt time.Time
}

// groupHealthBucket 是单个分组在窗口内的流量账（内部累计）。
type groupHealthBucket struct {
	RelayLogFaultCounts
	requests      int64
	lastFailureAt time.Time
	targets       map[string]*groupHealthTarget
}

func (b *groupHealthBucket) targetFor(channel, model string) *groupHealthTarget {
	if b.targets == nil {
		b.targets = make(map[string]*groupHealthTarget)
	}
	key := channel + "\x00" + model
	target, ok := b.targets[key]
	if !ok {
		target = &groupHealthTarget{channel: channel, model: model}
		b.targets[key] = target
	}
	return target
}

// groupHealthMeta 是分组的配置侧事实（来自缓存快照，成员可用性已回填）。
type groupHealthMeta struct {
	groupID        int
	name           string
	mode           string
	deleted        bool
	memberCount    int
	availableCount int
}

// GroupHealthStats 汇总各分组的健康事实。
//
// 数据来源两处，各自负责一半事实，缺一不可：
//   - relay_logs（窗口内）：请求账与失败归因 —— 「最近实际发生了什么」；
//   - 分组缓存（op.GroupList）：成员数与可用成员数 —— 「配置上现在是什么样」。
//
// 只看日志会把「刚配好还没用」的分组当成没问题，只看配置则会把
// 「成员都启用着但上游一直在拒绝」的分组当成好的。
func GroupHealthStats(ctx context.Context, window int) (GroupHealthSummary, error) {
	rows, sample, err := relayLogWindow(ctx, window)
	if err != nil {
		return GroupHealthSummary{}, err
	}

	byGroup := make(map[int]*groupHealthBucket)
	for _, row := range rows {
		// 没有 group_id 的请求不进分组健康 —— 它们没有「哪个分组」可言。
		if row.GroupID <= 0 {
			continue
		}
		b, ok := byGroup[row.GroupID]
		if !ok {
			b = &groupHealthBucket{}
			byGroup[row.GroupID] = b
		}
		b.requests++
		applyFaultCount(&b.RelayLogFaultCounts, row)
		if !isRelayFailure(row) {
			continue
		}
		if row.StartedAt.After(b.lastFailureAt) {
			b.lastFailureAt = row.StartedAt
		}
		// 失败落在哪个上游：查不到 target_channel 的失败（「无可用成员」这类）
		// 也要留一行，否则最该被看见的那种失败会整类消失。
		target := b.targetFor(row.TargetChannel, row.TargetModel)
		target.failures++
		if row.StartedAt.After(target.lastFailureAt) {
			target.lastFailureAt = row.StartedAt
		}
	}

	summary := GroupHealthSummary{
		Window:            sample.Samples,
		Sample:            sample,
		FailingThreshold:  groupHealthFailingRate,
		DegradedThreshold: groupHealthDegradedRate,
		Groups:            make([]GroupHealthRow, 0, len(byGroup)+8),
	}

	// 配置侧：缓存快照里的全部分组（含从没被调用过的）。
	for _, group := range GroupList() {
		available := 0
		for _, item := range group.Items {
			if item.Available {
				available++
			}
		}
		meta := groupHealthMeta{
			groupID:        group.ID,
			name:           group.Name,
			mode:           string(group.Mode),
			memberCount:    len(group.Items),
			availableCount: available,
		}
		summary.Groups = append(summary.Groups, buildGroupHealthRow(meta, byGroup[group.ID]))
		delete(byGroup, group.ID)
	}
	// 日志侧残留：配置里已经没有、但日志还在保留期内的分组。
	for groupID, b := range byGroup {
		meta := groupHealthMeta{groupID: groupID, name: groupHealthDeletedName, deleted: true}
		summary.Groups = append(summary.Groups, buildGroupHealthRow(meta, b))
	}

	for index := range summary.Groups {
		switch summary.Groups[index].State {
		case GroupStateFailing:
			summary.FailingCount++
		case GroupStateNoMember:
			summary.NoMemberCount++
		case GroupStateDegraded:
			summary.DegradedCount++
		case GroupStateEmpty:
			summary.EmptyCount++
		case GroupStateIdle:
			summary.IdleCount++
		default:
			summary.HealthyCount++
		}
	}

	sortGroupHealthRows(summary.Groups)
	return summary, nil
}

// buildGroupHealthRow 把「配置事实 + 窗口流量账」合成一行。账可以为空（该分组没有请求）。
func buildGroupHealthRow(meta groupHealthMeta, b *groupHealthBucket) GroupHealthRow {
	if b == nil {
		b = &groupHealthBucket{}
	}
	row := GroupHealthRow{
		GroupID:             meta.groupID,
		Name:                meta.name,
		Mode:                meta.mode,
		Deleted:             meta.deleted,
		MemberCount:         meta.memberCount,
		AvailableCount:      meta.availableCount,
		Requests:            b.requests,
		RelayLogFaultCounts: b.RelayLogFaultCounts,
		LastFailureAt:       formatHealthTime(b.lastFailureAt),
		FailingChannels:     topFailingTargets(b),
	}
	row.SuccessSamples = row.Success + row.MemberFault + row.TransientFault + row.Unclassified
	row.SuccessRate = ratio(row.Success, row.SuccessSamples)
	row.State = groupHealthState(row.MemberCount, row.AvailableCount, row.Requests, row.SuccessRate)
	return row
}

// isRelayFailure 判定一行是否算「失败」。
// 与 applyFaultCount 同源：取消是客户端主动断开，既不算成功也不算失败。
func isRelayFailure(row model.RelayLog) bool {
	return row.Status != "success" && row.Status != "canceled"
}

// groupHealthState 判定分组状态。
//
// switch 的顺序就是优先级，前三项与流量无关（纪律 1），必须先判：
// 一个「没有成员」的分组不会因为没人调用而变得没问题。
func groupHealthState(memberCount, availableCount int, requests int64, successRate float64) string {
	switch {
	case memberCount == 0:
		return GroupStateEmpty
	case availableCount == 0:
		return GroupStateNoMember
	case requests == 0:
		return GroupStateIdle
	case successRate < groupHealthFailingRate:
		return GroupStateFailing
	case successRate < groupHealthDegradedRate:
		return GroupStateDegraded
	default:
		return GroupStateHealthy
	}
}

// topFailingTargets 取失败最多的几个上游目标（值拷贝，不暴露内部时刻字段）。
func topFailingTargets(b *groupHealthBucket) []GroupHealthChannel {
	out := make([]GroupHealthChannel, 0, len(b.targets))
	for _, target := range b.targets {
		out = append(out, GroupHealthChannel{
			Channel:       target.channel,
			Model:         target.model,
			Failures:      target.failures,
			LastFailureAt: formatHealthTime(target.lastFailureAt),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Failures != out[j].Failures {
			return out[i].Failures > out[j].Failures
		}
		if out[i].Channel != out[j].Channel {
			return out[i].Channel < out[j].Channel
		}
		return out[i].Model < out[j].Model
	})
	if len(out) > groupHealthChannelLimit {
		out = out[:groupHealthChannelLimit]
	}
	return out
}

// groupHealthFailureCount 是四档故障之和（不含成功，也不含取消）。
func groupHealthFailureCount(c RelayLogFaultCounts) int64 {
	return c.RequestFault + c.MemberFault + c.TransientFault + c.Unclassified
}

// groupHealthRank 是状态的展示优先级：越该被处理的越靠前。
//
// 顺序的取舍：`failing`（已经在出错）压过 `no_member`（一定出错、但可能没人用）——
// 前者正在伤害真实流量，后者可能只是没人碰。`no_member` 压过 `degraded`，
// 因为它是结构性的，重试不会自己好。`empty`/`idle` 排在后面：
// 它们要的是「清理或接入」，不是「抢修」。
func groupHealthRank(state string) int {
	switch state {
	case GroupStateFailing:
		return 0
	case GroupStateNoMember:
		return 1
	case GroupStateDegraded:
		return 2
	case GroupStateEmpty:
		return 3
	case GroupStateIdle:
		return 4
	default:
		return 5
	}
}

// sortGroupHealthRows 把最该被处理的分组排在最前面。
//
// 抽成接切片的纯函数：输入来自 map 遍历（顺序随机），直接端到端验证会让
// 「少写一个末位比较键」的实现偶发排对（本项目已踩过两次）。
func sortGroupHealthRows(rows []GroupHealthRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := groupHealthRank(rows[i].State), groupHealthRank(rows[j].State)
		if ri != rj {
			return ri < rj
		}
		fi := groupHealthFailureCount(rows[i].RelayLogFaultCounts)
		fj := groupHealthFailureCount(rows[j].RelayLogFaultCounts)
		if fi != fj {
			return fi > fj
		}
		if rows[i].Requests != rows[j].Requests {
			return rows[i].Requests > rows[j].Requests
		}
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].GroupID < rows[j].GroupID
	})
}

// formatHealthTime 把时刻格式化成 RFC3339；零值给空串而不是 0001-01-01
// —— 界面上的「从未失败过」不能长得像「1901 年失败过」。
func formatHealthTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}
