package op

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-insight-007 选路判定与终止原因画像。
//
// ## 补的是哪个盲区
//
// 项目现在有四层「为什么」，但都只回答各自那一格：
//
//	Decision      走了哪个成员（逐行文本，没有分布）
//	FaultKind     这次失败算谁的账（有分布，只看失败行）
//	AttemptChain  试了几轮、哪轮换了谁（看的是"轮"）
//	ModelChain    模型名换没换（看的是"名"）
//
// 唯独没有一面镜子回答「**我的分流到底是谁在做主**」与「**我是怎么停下来的**」。
// 这两个问题各自的证据早就躺在 relay_logs 里：decision 从 T-decision-001 起、
// stop_reason 从 T-trace-003 起就在落库，日志页看得到、导出有列 —— 却没有任何
// 统计读过它们，是典型的"生产了没人消费"。
//
// 具体答不上来的问题：
//
//	亲和会不会把请求全粘在同一个成员上？   → reasons 里 affinity 的占比
//	智能路由的决策档/执行档实际各吃多少？   → tiers 分布（只对 mode=smart 有意义）
//	命中得最多的是组内第几个成员？         → slot 分布；首位占比高说明排序几乎没起作用
//	失败是"试到预算耗尽"还是"上游在集体拒绝我"？ → stops 里两个 reason 的占比
//
// 最后一条尤其要紧：**这两种在日志上都长成一次失败**，但一个要查上游、一个要改请求。
//
// ## 三个比例的口径都不一样（这是本文件最容易写错的地方）
//
//	reasons/modes 的分母是 DecisionRecorded —— 没记录的行不该压低任何机制占比
//	tiers         的分母是 SmartCount       —— 非智能路由的请求没有档位可言
//	stops         的分母是 StopRecorded     —— 历史行的空值与漏标同样不该进分母
//
// 三个分母如果都图省事写成 Window，界面上所有比例会被"没记录的行"稀释成同一
// 个偏小的数，而且**看起来完全正常**。

// RoutingReasonStat 是「哪个机制决定了这次选择」的分布（decision 的 reason 段）。
//
// reason 的取值由 relay 包的 decisionReason* 常量定义（manual/affinity/probe/
// priority/ranked）。统计侧**不枚举白名单**：出现新机制时按原值单列，而不是
// 塞进"其他"——"其他"会让新机制上线后无声无息地消失。
type RoutingReasonStat struct {
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
	// Ratio 的分母是 DecisionRecorded（不是 Window）。
	Ratio    float64 `json:"ratio"`
	Success  int64   `json:"success"`
	Canceled int64   `json:"canceled"`
	Failed   int64   `json:"failed"`
}

// RoutingModeStat 是分组模式的分布（decision 的 mode 段）。
type RoutingModeStat struct {
	Mode  string  `json:"mode"`
	Count int64   `json:"count"`
	Ratio float64 `json:"ratio"`
}

// RoutingTierStat 是智能路由档位的分布（decision 的 tier 段）。
// 只在 mode=smart 的行上有意义，分母是 SmartCount。
type RoutingTierStat struct {
	Tier  string  `json:"tier"`
	Count int64   `json:"count"`
	Ratio float64 `json:"ratio"`
}

// RoutingSlotStat 是「命中的是组内第几个成员」的分布（decision 的 slot 段）。
//
// **这个数字不能读成"成员负载占比"**：slot 是**组内序号**，不同分组的同一
// slot 是不同的成员，分组大小也不同。它回答的是"**排名在前面的成员被选中的
// 频率**"——首位占比高说明亲和/优先级在主导，排序机制几乎没起作用。
type RoutingSlotStat struct {
	Slot  int     `json:"slot"`
	Count int64   `json:"count"`
	Ratio float64 `json:"ratio"`
}

// RoutingStopStat 是终止原因的分布，按 (reason, source) 成对分桶。
//
// 必须成对：同一个 reason 来自不同 source 时处置动作完全不同 ——
// all_members_rejected 在 failover 下是"上游集体拒绝"（改请求），
// 在 failfast 下是"用户设置要求立即停"（改设置）。只按 reason 分桶会把
// 这两种糊成一个，用户照着数字去查会查错方向。
type RoutingStopStat struct {
	Reason string `json:"reason"`
	Source string `json:"source"`
	Count  int64  `json:"count"`
	// Ratio 的分母是 StopRecorded（不是 Window）。
	Ratio    float64 `json:"ratio"`
	Success  int64   `json:"success"`
	Canceled int64   `json:"canceled"`
	Failed   int64   `json:"failed"`
}

// RoutingProfile 是窗口内选路判定与终止原因的完整画像。
//
// 守恒关系（单测逐条断言，任一被破坏都说明有行被静默丢弃）：
//
//	DecisionRecorded + DecisionUnrecorded + DecisionMalformed == Window
//	Σ reasons.Count == DecisionRecorded
//	Σ modes.Count   == DecisionRecorded
//	Σ tiers.Count   == SmartCount ≤ DecisionRecorded
//	Σ slot_distribution.Count == SlotSamples ≤ DecisionRecorded
//	StopRecorded + StopUnrecorded == Window
//	Σ stops.Count == StopRecorded
type RoutingProfile struct {
	Window int64 `json:"window"`

	// 三态可见性。decision 的空值同时来自三种完全不同的原因，必须分开数：
	// 这个字段上线之前的历史行、非选路路径（如自定义协议）没写、以及真漏标。
	// 糊成一个数字就没人能判断"要不要去补代码"。
	DecisionRecorded   int64 `json:"decision_recorded"`
	DecisionUnrecorded int64 `json:"decision_unrecorded"`
	DecisionMalformed  int64 `json:"decision_malformed"`

	Reasons []RoutingReasonStat `json:"reasons"`
	Modes   []RoutingModeStat   `json:"modes"`
	// SmartCount 是 mode=smart 的行数，作为 tiers 分布的分母与守恒基准。
	SmartCount int64             `json:"smart_count"`
	Tiers      []RoutingTierStat `json:"tiers"`

	// SlotSamples 是有顶层序号的行数（序号算不出时为 0，那种行不进样本）。
	SlotSamples int64 `json:"slot_samples"`
	// SlotAvg = Σ(slot×count) / SlotSamples，与 SlotDistribution 同源同分母
	// （两者互为交叉校验，单测会断言一致）。
	SlotAvg          float64           `json:"slot_avg"`
	SlotDistribution []RoutingSlotStat `json:"slot_distribution"`

	StopRecorded   int64             `json:"stop_recorded"`
	StopUnrecorded int64             `json:"stop_unrecorded"`
	Stops          []RoutingStopStat `json:"stops"`
}

// 缺失项在桶里的显示名。三种缺失的处置动作不同，所以名字也不同：
// 原因缺失要去补代码，模式缺失说明文本只写了半截。
const (
	routingReasonMissing = "(原因缺失)"
	routingModeMissing   = "(模式缺失)"
	routingTierMissing   = "(档位缺失)"
)

// knownDecisionFields 是 Decision.Text() 会写出的全部键。
// 用来区分「格式坏」（一个键都不认识）与「正常缺字段」（非 smart 没有 tier、
// 序号算不出没有 slot）—— 这两种都表现为"某个键取不到"，但含义相反。
var knownDecisionFields = []string{"mode", "tier", "reason", "slot", "attempt"}

// RoutingProfileStats 汇总窗口内 relay_logs 的选路判定与终止原因分布。
//
// 与 AnalyticsOverviewStats 一样读明细、在 Go 侧聚合（不写 SQL 聚合），
// 差异只在聚合维度：那边按模型/客户端/渠道切片，这里按"谁决定的""怎么停的"切片。
func RoutingProfileStats(ctx context.Context, window int) (RoutingProfile, error) {
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
		return RoutingProfile{}, err
	}

	out := RoutingProfile{
		Window:           int64(len(rows)),
		Reasons:          []RoutingReasonStat{},
		Modes:            []RoutingModeStat{},
		Tiers:            []RoutingTierStat{},
		SlotDistribution: []RoutingSlotStat{},
		Stops:            []RoutingStopStat{},
	}
	if len(rows) == 0 {
		// 空窗口返回空结果而不是错误：新装的实例本来就一条都没有。
		return out, nil
	}

	reasons := make(map[string]*RoutingReasonStat)
	modes := make(map[string]*RoutingModeStat)
	tiers := make(map[string]*RoutingTierStat)
	slots := make(map[int]int64)
	stops := make(map[string]*RoutingStopStat)
	var slotSum int64

	for _, row := range rows {
		applyRoutingDecision(&out, row, reasons, modes, tiers, slots, &slotSum)
		applyRoutingStop(&out, row, stops)
	}

	out.Reasons = finishRoutingReasons(reasons, out.DecisionRecorded)
	out.Modes = finishRoutingModes(modes, out.DecisionRecorded)
	out.Tiers = finishRoutingTiers(tiers, out.SmartCount)
	if out.SlotSamples > 0 {
		out.SlotAvg = float64(slotSum) / float64(out.SlotSamples)
	}
	out.SlotDistribution = finishRoutingSlots(slots, out.SlotSamples)
	out.Stops = finishRoutingStops(stops, out.StopRecorded)
	return out, nil
}

// applyRoutingDecision 把一行日志的 decision 归入三态之一并按段计数。
//
// **坏数据不进任何分布桶**：一个连 mode 都不认识的行，它的 reason 段同样不可信，
// 让它进 reasons 会把解析失败伪装成一个真实的机制——那比不统计更危险。
func applyRoutingDecision(
	out *RoutingProfile,
	row model.RelayLog,
	reasons map[string]*RoutingReasonStat,
	modes map[string]*RoutingModeStat,
	tiers map[string]*RoutingTierStat,
	slots map[int]int64,
	slotSum *int64,
) {
	text := strings.TrimSpace(row.Decision)
	if text == "" {
		out.DecisionUnrecorded++
		return
	}
	fields := parseRoutingFields(text)
	if !hasKnownDecisionField(fields) {
		out.DecisionMalformed++
		return
	}
	out.DecisionRecorded++

	reason := fields["reason"]
	if reason == "" {
		reason = routingReasonMissing
	}
	bucket, ok := reasons[reason]
	if !ok {
		bucket = &RoutingReasonStat{Reason: reason}
		reasons[reason] = bucket
	}
	bucket.Count++
	addRoutingStatus(row.Status, &bucket.Success, &bucket.Canceled, &bucket.Failed)

	mode := fields["mode"]
	if mode == "" {
		mode = routingModeMissing
	}
	modeBucket, ok := modes[mode]
	if !ok {
		modeBucket = &RoutingModeStat{Mode: mode}
		modes[mode] = modeBucket
	}
	modeBucket.Count++

	// 档位只对智能路由有意义：其它模式**结构上**不会写 tier，
	// 把这些行算进分母会让"决策档占多少"永远偏小。
	if mode == string(model.GroupModeSmart) {
		out.SmartCount++
		tier := fields["tier"]
		if tier == "" {
			tier = routingTierMissing
		}
		tierBucket, ok := tiers[tier]
		if !ok {
			tierBucket = &RoutingTierStat{Tier: tier}
			tiers[tier] = tierBucket
		}
		tierBucket.Count++
	}

	// 顶层序号算不出时 Text() 会整段跳过；那种行不是 0 号成员，不能计入样本。
	if raw := fields["slot"]; raw != "" {
		if slot, err := strconv.Atoi(raw); err == nil && slot > 0 {
			out.SlotSamples++
			*slotSum += int64(slot)
			slots[slot]++
		}
	}
}

// applyRoutingStop 把一行日志的 stop_reason 归入已记录/未记录两态。
//
// 与 decision 的三态不同，这里两态够用：StopReason.Text() 的契约是
// "reason 为空就返回空串"，所以**非空文本必然带 reason 与 source**；
// 若仍取不到 reason，那是文本被外部拼过，用 (原因缺失) 桶显影即可。
func applyRoutingStop(out *RoutingProfile, row model.RelayLog, stops map[string]*RoutingStopStat) {
	text := strings.TrimSpace(row.StopReason)
	if text == "" {
		out.StopUnrecorded++
		return
	}
	out.StopRecorded++
	fields := parseRoutingFields(text)
	reason := fields["reason"]
	if reason == "" {
		reason = routingReasonMissing
	}
	// source 缺失时留空串单独成桶，不编默认值：编默认值会把"没记来源"
	// 显示成"来源是 system"，那是在替代码圆谎。
	source := fields["source"]
	key := reason + "\x00" + source
	bucket, ok := stops[key]
	if !ok {
		bucket = &RoutingStopStat{Reason: reason, Source: source}
		stops[key] = bucket
	}
	bucket.Count++
	addRoutingStatus(row.Status, &bucket.Success, &bucket.Canceled, &bucket.Failed)
}

// parseRoutingFields 拆解 Decision.Text() / StopReason.Text() 的 "k=v;k=v" 文本。
//
// 三种必须容忍的真实形态：
//   - **键可以缺失**：Text() 对空字段整段跳过（非 smart 没有 tier、序号算不出
//     没有 slot），所以"取不到某个键"是正常情况而不是损坏；
//   - **将来可能新增键**：未知键原样保留，统计侧只挑自己认识的用，
//     绝不因为多了一个键就把整行判成坏数据；
//   - **值里可能含等号**：只按第一个等号切分，未来的自由文本值不会被切坏。
func parseRoutingFields(text string) map[string]string {
	fields := make(map[string]string, 5)
	for _, part := range strings.Split(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		index := strings.Index(part, "=")
		if index <= 0 {
			// 没有键名（"k" 或 "=v"）的段跳过，不编造键名。
			continue
		}
		key := strings.TrimSpace(part[:index])
		value := strings.TrimSpace(part[index+1:])
		if key == "" {
			continue
		}
		// 同一个键出现两次时保留**第一个**：Text() 结构上不会重复，
		// 重复说明文本被外部拼过，此时先出现的是更靠近判定源头的那个。
		if _, exists := fields[key]; exists {
			continue
		}
		fields[key] = value
	}
	return fields
}

// hasKnownDecisionField 报告这段文本里是否至少有一个 Decision 认识的键。
func hasKnownDecisionField(fields map[string]string) bool {
	for _, key := range knownDecisionFields {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}

// addRoutingStatus 把一次终态计入三个格子。
// 口径与 applyFaultCount 一致：除 success/canceled 之外的一切都按失败处理，
// 包括将来新增的终态 —— 新增终态时它先出现在 Failed 里（可见），而不是无处可去。
func addRoutingStatus(status string, success, canceled, failed *int64) {
	switch status {
	case "success":
		*success++
	case "canceled":
		*canceled++
	default:
		*failed++
	}
}

// finishRoutingReasons 给分布算比例并排序。
// 排序键：计数倒序 → 名称升序（名称兜底保证 map 遍历顺序不影响输出）。
func finishRoutingReasons(store map[string]*RoutingReasonStat, total int64) []RoutingReasonStat {
	out := make([]RoutingReasonStat, 0, len(store))
	for _, bucket := range store {
		bucket.Ratio = ratio(bucket.Count, total)
		out = append(out, *bucket)
	}
	sortRoutingReasons(out)
	return out
}

// sortRoutingReasons 独立成纯函数（而不是埋在聚合里）：排序输入来自 map 遍历，
// 顺序随机，只有喂确定性输入才能验证"同数条目也有确定的次序"——
// 埋在聚合里会出现"实现少写一个排序键、但恰好偶发排对"的假绿。
func sortRoutingReasons(out []RoutingReasonStat) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Reason < out[j].Reason
	})
}

func finishRoutingModes(store map[string]*RoutingModeStat, total int64) []RoutingModeStat {
	out := make([]RoutingModeStat, 0, len(store))
	for _, bucket := range store {
		bucket.Ratio = ratio(bucket.Count, total)
		out = append(out, *bucket)
	}
	sortRoutingModes(out)
	return out
}

func sortRoutingModes(out []RoutingModeStat) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Mode < out[j].Mode
	})
}

func finishRoutingTiers(store map[string]*RoutingTierStat, total int64) []RoutingTierStat {
	out := make([]RoutingTierStat, 0, len(store))
	for _, bucket := range store {
		bucket.Ratio = ratio(bucket.Count, total)
		out = append(out, *bucket)
	}
	sortRoutingTiers(out)
	return out
}

func sortRoutingTiers(out []RoutingTierStat) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Tier < out[j].Tier
	})
}

// finishRoutingSlots 输出按序号升序的分布（序号天然有序，不用计数排序：
// 这个分布要回答的是"第几位被选得多"，顺序本身就是信息）。
func finishRoutingSlots(store map[int]int64, total int64) []RoutingSlotStat {
	out := make([]RoutingSlotStat, 0, len(store))
	for slot, count := range store {
		out = append(out, RoutingSlotStat{Slot: slot, Count: count, Ratio: ratio(count, total)})
	}
	sortRoutingSlots(out)
	return out
}

func sortRoutingSlots(out []RoutingSlotStat) {
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
}

func finishRoutingStops(store map[string]*RoutingStopStat, total int64) []RoutingStopStat {
	out := make([]RoutingStopStat, 0, len(store))
	for _, bucket := range store {
		bucket.Ratio = ratio(bucket.Count, total)
		out = append(out, *bucket)
	}
	sortRoutingStops(out)
	return out
}

// sortRoutingStops 的末位键是 source：同一个 reason 可能来自多个 source，
// 只按 reason 排会让这两行在界面上左右互换（同数时不稳定）。
func sortRoutingStops(out []RoutingStopStat) {
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Reason != out[j].Reason {
			return out[i].Reason < out[j].Reason
		}
		return out[i].Source < out[j].Source
	})
}
