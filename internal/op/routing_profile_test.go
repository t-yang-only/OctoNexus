package op

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-insight-007 选路判定与终止原因画像的判据。
//
// 本文件的判据围绕一件事：**分布里的每个数字都必须能对上它的分母**。
// 这个功能的失败模式不是报错，而是"比例看着正常但口径错了"——
// 例如拿 Window 当分母，界面上一片偏小的百分比毫无违和感。

type routingLogSeed struct {
	status     string
	decision   string
	stopReason string
}

func seedRoutingLog(t *testing.T, conn *gorm.DB, s routingLogSeed) {
	t.Helper()
	row := model.RelayLog{
		Status:     s.status,
		Decision:   s.decision,
		StopReason: s.stopReason,
		StartedAt:  analyticsBase(),
	}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatalf("seed routing log: %v", err)
	}
}

func routingProfileOf(t *testing.T, conn *gorm.DB, window int) RoutingProfile {
	t.Helper()
	out, err := RoutingProfileStats(context.Background(), window)
	if err != nil {
		t.Fatalf("RoutingProfileStats: %v", err)
	}
	return out
}

// routingReasonAt 取指定 reason 的桶，取不到直接失败（不用裸索引，避免 panic
// 终止整个测试进程让后面的判据静默不执行）。
func routingReasonAt(t *testing.T, out RoutingProfile, reason string) RoutingReasonStat {
	t.Helper()
	for _, item := range out.Reasons {
		if item.Reason == reason {
			return item
		}
	}
	t.Fatalf("Reasons 里没有 %q，现有：%v", reason, routingReasonNames(out))
	return RoutingReasonStat{}
}

func routingReasonNames(out RoutingProfile) []string {
	names := make([]string, 0, len(out.Reasons))
	for _, item := range out.Reasons {
		names = append(names, item.Reason)
	}
	return names
}

func routingStopAt(t *testing.T, out RoutingProfile, reason, source string) RoutingStopStat {
	t.Helper()
	for _, item := range out.Stops {
		if item.Reason == reason && item.Source == source {
			return item
		}
	}
	t.Fatalf("Stops 里没有 (%q,%q)，现有：%v", reason, source, out.Stops)
	return RoutingStopStat{}
}

func sumRoutingReasonCounts(out RoutingProfile) int64 {
	var total int64
	for _, item := range out.Reasons {
		total += item.Count
	}
	return total
}

func sumRoutingModeCounts(out RoutingProfile) int64 {
	var total int64
	for _, item := range out.Modes {
		total += item.Count
	}
	return total
}

func sumRoutingTierCounts(out RoutingProfile) int64 {
	var total int64
	for _, item := range out.Tiers {
		total += item.Count
	}
	return total
}

func sumRoutingSlotCounts(out RoutingProfile) int64 {
	var total int64
	for _, item := range out.SlotDistribution {
		total += item.Count
	}
	return total
}

func sumRoutingStopCounts(out RoutingProfile) int64 {
	var total int64
	for _, item := range out.Stops {
		total += item.Count
	}
	return total
}

// 判据 1：三态守恒。decision 的三种形态必须把窗口完整分完，一格不剩也一格不重。
//
// 这条是全部统计的地基：只要有一类行既不算记录、也不算未记录、也不算坏数据，
// 它就凭空消失了 —— 而"总数比明细加起来多"这种提示在界面上几乎没人会去核对。
func TestRoutingProfileThreeStatesAreConserved(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=smart;tier=decision;reason=affinity;slot=1;attempt=1"})
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", decision: "mode=failover;reason=priority;slot=2"})
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", decision: ""})             // 未记录
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", decision: "   "})          // 只有空白也是未记录
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", decision: "totally junk"}) // 有内容但一个键都不认识
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", decision: ";;; =x ;  "})   // 段落无键名

	out := routingProfileOf(t, conn, 100)

	if out.Window != 6 {
		t.Fatalf("Window = %d，期望 6", out.Window)
	}
	if out.DecisionRecorded != 2 {
		t.Errorf("DecisionRecorded = %d，期望 2", out.DecisionRecorded)
	}
	if out.DecisionUnrecorded != 2 {
		t.Errorf("DecisionUnrecorded = %d，期望 2（空串与纯空白各一条）", out.DecisionUnrecorded)
	}
	if out.DecisionMalformed != 2 {
		// 两条坏数据：「totally junk」整段没有等号；「;;; =x ;  」里唯一的
		// 等号出现在段首（"=x"），没有键名 —— 两种都不是"记录到了"。
		t.Errorf("DecisionMalformed = %d，期望 2", out.DecisionMalformed)
	}
	if sum := out.DecisionRecorded + out.DecisionUnrecorded + out.DecisionMalformed; sum != out.Window {
		t.Errorf("三态合计 %d != Window %d", sum, out.Window)
	}
}

// 判据 2：各分布的计数合计必须等于它们各自的分母，且终态拆分也守恒。
func TestRoutingDistributionsTotalsMatchRecorded(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=smart;tier=decision;reason=affinity;slot=1"})
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=smart;tier=execution;reason=ranked;slot=1"})
	seedRoutingLog(t, conn, routingLogSeed{status: "canceled", decision: "mode=failover;reason=priority;slot=2"})
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", decision: "mode=failover;reason=probe;slot=3"})
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", decision: ""})

	out := routingProfileOf(t, conn, 100)

	if got := sumRoutingReasonCounts(out); got != out.DecisionRecorded {
		t.Errorf("reasons 合计 %d != DecisionRecorded %d", got, out.DecisionRecorded)
	}
	if got := sumRoutingModeCounts(out); got != out.DecisionRecorded {
		t.Errorf("modes 合计 %d != DecisionRecorded %d", got, out.DecisionRecorded)
	}
	if got := sumRoutingTierCounts(out); got != out.SmartCount {
		t.Errorf("tiers 合计 %d != SmartCount %d", got, out.SmartCount)
	}
	if out.SmartCount != 2 {
		t.Errorf("SmartCount = %d，期望 2（只有两行是 smart）", out.SmartCount)
	}

	// 终态拆分：每个桶的三格之和必须等于桶的计数。
	for _, item := range out.Reasons {
		if item.Success+item.Canceled+item.Failed != item.Count {
			t.Errorf("reason %q 的三态 %d/%d/%d 合计 %d != Count %d",
				item.Reason, item.Success, item.Canceled, item.Failed,
				item.Success+item.Canceled+item.Failed, item.Count)
		}
	}
	// affinity 的两条都是 success，probe 的那条是 failed。
	if item := routingReasonAt(t, out, "affinity"); item.Success != 1 {
		t.Errorf("affinity.Success = %d，期望 1", item.Success)
	}
	if item := routingReasonAt(t, out, "probe"); item.Failed != 1 {
		t.Errorf("probe.Failed = %d，期望 1", item.Failed)
	}
	if item := routingReasonAt(t, out, "priority"); item.Canceled != 1 {
		t.Errorf("priority.Canceled = %d，期望 1", item.Canceled)
	}
}

// 判据 3（隔离）：档位分布只统计智能路由的行。
//
// 非智能路由的请求**结构上**不会写 tier，把它们算进分母会让"决策档占多少"
// 永远偏小；把它们算进 (档位缺失) 桶则会把"不该有档位"和"该有却漏了"糊成一个数。
// 隔离方式：本用例里 3 条 failover + 1 条 smart，若实现按 DecisionRecorded 当分母，
// smart 那条的比例会从 100% 掉到 25%。
func TestRoutingTiersOnlyCountSmartMode(t *testing.T) {
	conn := withAnalyticsDB(t)
	for i := 0; i < 3; i++ {
		seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority;slot=1"})
	}
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=smart;tier=decision;reason=ranked;slot=1"})

	out := routingProfileOf(t, conn, 100)

	if out.SmartCount != 1 {
		t.Fatalf("SmartCount = %d，期望 1", out.SmartCount)
	}
	if len(out.Tiers) != 1 {
		t.Fatalf("tiers 应有 1 个桶（非 smart 行不得进入），实际 %v", out.Tiers)
	}
	if out.Tiers[0].Tier != "decision" || out.Tiers[0].Ratio != 100 {
		t.Errorf("tier 桶 = %+v，期望 decision / 100%%", out.Tiers[0])
	}
}

// 判据 4（隔离）：解析失败的行不得进入任何分布。
//
// 坏数据的 reason 段同样不可信。让它进 reasons 是把"解析失败"伪装成一个
// 真实存在的机制 —— 那比不统计更危险，因为界面上它会显示成一个正常占比，
// 用户会照着去查一个根本不存在的机制。
//
// 隔离方式：坏行的键名**全部带前缀**（notmode/notreason/notslot），值却是
// 真实机制名与合法序号（failover/affinity/9）。这样只有「精确匹配键名」的实现
// 才能正确排除它 —— 任何做子串/前缀模糊匹配、或直接扫值域的实现在这里露出来。
// （第一版隔离用例里塞了 slot=9 这个**合法**键，结果那行其实是正常记录，
// 用例根本没能隔离任何东西 —— 隔离用例必须让目标只受被测守卫保护。）
func TestRoutingMalformedRowsStayOutOfDistributions(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority;slot=1"})
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "reason=affinity"}) // 认得的键 → 记录
	seedRoutingLog(t, conn, routingLogSeed{
		status:   "success",
		decision: "notmode=failover;notreason=affinity;notslot=9", // 键名全带前缀 → 一个都不认得
	})

	out := routingProfileOf(t, conn, 100)

	if out.DecisionMalformed != 1 {
		t.Fatalf("DecisionMalformed = %d，期望 1（%v）", out.DecisionMalformed, out.Reasons)
	}
	want := int64(2) // priority + 那条只写 reason 的
	if got := sumRoutingReasonCounts(out); got != want {
		t.Errorf("reasons 合计 %d，期望 %d（坏行不得计入）", got, want)
	}
	if item := routingReasonAt(t, out, "affinity"); item.Count != 1 {
		t.Errorf("affinity.Count = %d，期望 1（只有那条单键行算数，坏行里的值不得被捡进来）", item.Count)
	}
	// notslot=9 在坏行里，不得进样本。
	if out.SlotSamples != 1 {
		t.Errorf("SlotSamples = %d，期望 1（坏行的序号不可信）", out.SlotSamples)
	}
}

// 判据 5（隔离）：比例的分母是"记录到的行数"，不是窗口行数。
//
// 这是本功能最容易写错、且**错了完全看不出来**的地方：混入大量未记录行后，
// 所有机制占比会一致地被稀释成偏小的值，界面上一片正常。
// 隔离方式：造 1 条 affinity + 99 条未记录，占比必须是 100% 而不是 1%。
func TestRoutingReasonRatioUsesRecordedDenominator(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=affinity;slot=1"})
	for i := 0; i < 99; i++ {
		seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: ""})
	}

	out := routingProfileOf(t, conn, 100)

	if out.Window != 100 || out.DecisionRecorded != 1 {
		t.Fatalf("装置不对：Window=%d Recorded=%d", out.Window, out.DecisionRecorded)
	}
	item := routingReasonAt(t, out, "affinity")
	if item.Ratio != 100 {
		t.Errorf("affinity.Ratio = %v，期望 100（分母是 Recorded=1，不是 Window=100）", item.Ratio)
	}
}

// 判据 6（隔离）：终止原因必须按 (reason, source) 成对分桶。
//
// 同一个 reason 来自不同 source 时处置动作完全不同：all_members_rejected 在
// failover 下是"上游集体拒绝"（改请求），在 failfast 下是"设置要求立即停"（改设置）。
// 只按 reason 合并会把这两种糊成一格，用户照着数字去查会查错方向。
func TestRoutingStopPairsReasonWithSource(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{
		status:     "failed",
		stopReason: "action=stop;reason=all_members_rejected;source=upstream",
	})
	seedRoutingLog(t, conn, routingLogSeed{
		status:     "failed",
		stopReason: "action=stop;reason=all_members_rejected;source=config",
	})

	out := routingProfileOf(t, conn, 100)

	if len(out.Stops) != 2 {
		t.Fatalf("Stops 应有 2 个桶（同 reason 不同 source 不得合并），实际 %v", out.Stops)
	}
	upstream := routingStopAt(t, out, "all_members_rejected", "upstream")
	config := routingStopAt(t, out, "all_members_rejected", "config")
	if upstream.Count != 1 || config.Count != 1 {
		t.Errorf("两桶计数应为 1/1，实际 %d/%d", upstream.Count, config.Count)
	}
	if upstream.Failed != 1 || config.Failed != 1 {
		t.Errorf("两桶的 failed 应为 1/1，实际 %d/%d", upstream.Failed, config.Failed)
	}
}

// 判据 7：终止原因的可见性两态守恒，且未记录的比例分母同样是 Recorded。
func TestRoutingStopVisibilityIsConserved(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{status: "success", stopReason: "action=stop;reason=request_completed;source=system"})
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", stopReason: "action=stop;reason=attempt_budget_exhausted;source=config"})
	seedRoutingLog(t, conn, routingLogSeed{status: "failed", stopReason: ""})

	out := routingProfileOf(t, conn, 100)

	if out.StopRecorded != 2 || out.StopUnrecorded != 1 {
		t.Fatalf("StopRecorded/Unrecorded = %d/%d，期望 2/1", out.StopRecorded, out.StopUnrecorded)
	}
	if out.StopRecorded+out.StopUnrecorded != out.Window {
		t.Errorf("两态合计 %d != Window %d", out.StopRecorded+out.StopUnrecorded, out.Window)
	}
	if got := sumRoutingStopCounts(out); got != out.StopRecorded {
		t.Errorf("stops 合计 %d != StopRecorded %d", got, out.StopRecorded)
	}
	if item := routingStopAt(t, out, "request_completed", "system"); item.Ratio != 50 {
		t.Errorf("request_completed 占比 = %v，期望 50（分母是 Recorded=2）", item.Ratio)
	}
}

// 判据 8：slot 样本只含有序号的行，且均值与分布必须互相印证。
//
// SlotAvg 与 SlotDistribution 是同一批样本的两种呈现（一个是汇总、一个是分布），
// 它们互为交叉校验：任何一边漏行或用了不同样本集，这里就会不一致。
func TestRoutingSlotSamplesAndAverageAgree(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority;slot=1"})
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority;slot=3"})
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority;slot=0"}) // 0 不是合法序号
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority"})        // 无 slot 键
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority;slot=x"}) // 非数字

	out := routingProfileOf(t, conn, 100)

	if out.SlotSamples != 2 {
		t.Fatalf("SlotSamples = %d，期望 2（0/缺失/非数字都不计入）", out.SlotSamples)
	}
	if got := sumRoutingSlotCounts(out); got != out.SlotSamples {
		t.Errorf("分布合计 %d != SlotSamples %d", got, out.SlotSamples)
	}
	if len(out.SlotDistribution) != 2 || out.SlotDistribution[0].Slot != 1 || out.SlotDistribution[1].Slot != 3 {
		t.Fatalf("分布应按序号升序为 [1,3]，实际 %v", out.SlotDistribution)
	}
	wantAvg := 2.0 // (1+3)/2
	if out.SlotAvg != wantAvg {
		t.Errorf("SlotAvg = %v，期望 %v", out.SlotAvg, wantAvg)
	}
	if out.SlotDistribution[0].Ratio != 50 {
		t.Errorf("slot=1 占比 = %v，期望 50", out.SlotDistribution[0].Ratio)
	}
}

// 判据 9：解析器对未知键与含等号的值都必须容忍（前向兼容）。
//
// Text() 的结构将来可能新增键。若解析器把"多了一个不认识的键"当成损坏，
// 新增字段的那天起整个分布会突然变成 malformed，而代码看起来毫无问题。
// 同理，值里含等号时只按第一个等号切分，否则值会被切掉一半。
//
// 两条装置分别挡两类坏实现（第一版只有第二条，结果"按键数判断"的实现
// 因为键数恰好够而蒙对，变异检查直接漏掉它）：
//   - 未知键多、已知键少 → 挡「键数够就算认得出」型
//   - 未知键与已知键混杂且键数很多 → 挡「有未知键就判坏」型
//
// 第三条专门挡"用数量替代内容"：**一个已知键都没有**、但键数不少。
// 前两条都含 mode（已知键），实现会在扫到它时提前返回 true，
// 根本走不到"一个都不认得"那条分支 —— 少了这条，那类变异永远测不到。
func TestRoutingFieldsTolerateUnknownKeysAndEquals(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{
		status:   "success",
		decision: "mode=failover;future_key=x;note=y", // 3 个键，其中只有 1 个已知
	})
	seedRoutingLog(t, conn, routingLogSeed{
		status:   "success",
		decision: "mode=failover;reason=priority;slot=1;future_key=newvalue;note=a=b=c",
	})
	seedRoutingLog(t, conn, routingLogSeed{
		status:   "success",
		decision: "alpha=1;beta=2;gamma=3;delta=4;epsilon=5", // 5 个键，一个都不认得 → 坏数据
	})

	out := routingProfileOf(t, conn, 100)

	if out.DecisionRecorded != 2 {
		t.Fatalf("DecisionRecorded = %d，期望 2（未知键的行只要认得一个已知键就算记录）", out.DecisionRecorded)
	}
	if out.DecisionMalformed != 1 {
		t.Fatalf("DecisionMalformed = %d，期望 1（键多不等于认得：一个已知键都没有就是坏数据）",
			out.DecisionMalformed)
	}
	if item := routingReasonAt(t, out, "priority"); item.Count != 1 {
		t.Errorf("priority.Count = %d，期望 1", item.Count)
	}
	if item := routingReasonAt(t, out, routingReasonMissing); item.Count != 1 {
		t.Errorf("%s.Count = %d，期望 1（第一条没有 reason 段）", routingReasonMissing, item.Count)
	}
	if out.SlotSamples != 1 {
		t.Errorf("SlotSamples = %d，期望 1", out.SlotSamples)
	}
}

// 判据 10：已知键存在但值为空时，进 (原因缺失) 桶而不是被丢弃。
//
// 一条 `mode=failover;slot=1` 缺了 reason 的行**必须仍然出现在 reasons 里**
// ——它是"记录到了但少了那一段"，与"整行没记录"是两回事。
// 丢弃它会让 reasons 合计小于 Recorded，而分母不变，比例全部偏小。
func TestRoutingMissingReasonStillGetsBucket(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;slot=1"})

	out := routingProfileOf(t, conn, 100)

	if out.DecisionRecorded != 1 {
		t.Fatalf("DecisionRecorded = %d，期望 1", out.DecisionRecorded)
	}
	item := routingReasonAt(t, out, routingReasonMissing)
	if item.Count != 1 {
		t.Errorf("%s.Count = %d，期望 1", routingReasonMissing, item.Count)
	}
	if got := sumRoutingReasonCounts(out); got != out.DecisionRecorded {
		t.Errorf("reasons 合计 %d != Recorded %d（缺段的行不得凭空消失）", got, out.DecisionRecorded)
	}
}

// 判据 11：重复键保留第一个。
//
// Text() 结构上不会重复，重复说明文本被外部拼过；此时先出现的那个更靠近
// 判定源头。取最后一个会让"谁的文本拼在后面"决定统计结果。
func TestRoutingDuplicateKeyKeepsFirst(t *testing.T) {
	conn := withAnalyticsDB(t)
	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority;reason=affinity;slot=1"})

	out := routingProfileOf(t, conn, 100)

	if item := routingReasonAt(t, out, "priority"); item.Count != 1 {
		t.Errorf("应保留第一个 reason=priority，实际分布 %v", routingReasonNames(out))
	}
}

// 判据 12：五个排序纯函数在同一计数下必须有确定次序。
//
// 排序输入来自 map 遍历（顺序随机），埋在聚合里会出现"实现少写一个排序键、
// 但恰好偶发排对"的假绿。喂确定性输入并轮换输入顺序才能钉住它。
func TestRoutingSortsAreDeterministic(t *testing.T) {
	reasons := []RoutingReasonStat{
		{Reason: "ranked", Count: 5},
		{Reason: "affinity", Count: 5},
		{Reason: "manual", Count: 5},
		{Reason: "probe", Count: 9},
	}
	wantReasons := []string{"probe", "affinity", "manual", "ranked"}
	for round := 0; round < 4; round++ {
		rotated := rotateRoutingReasons(reasons, round)
		sortRoutingReasons(rotated)
		for i, want := range wantReasons {
			if rotated[i].Reason != want {
				t.Fatalf("第 %d 轮 reasons 排序第 %d 位 = %q，期望 %q", round, i, rotated[i].Reason, want)
			}
		}
	}

	modes := []RoutingModeStat{{Mode: "smart", Count: 2}, {Mode: "failover", Count: 2}, {Mode: "allocate", Count: 7}}
	sortRoutingModes(modes)
	if modes[0].Mode != "allocate" || modes[1].Mode != "failover" || modes[2].Mode != "smart" {
		t.Errorf("modes 排序错误：%v", modes)
	}

	tiers := []RoutingTierStat{{Tier: "execution", Count: 3}, {Tier: "decision", Count: 3}}
	sortRoutingTiers(tiers)
	if tiers[0].Tier != "decision" {
		t.Errorf("同数档位应按名称升序（decision 在前），实际 %v", tiers)
	}

	slots := []RoutingSlotStat{{Slot: 3, Count: 1}, {Slot: 1, Count: 9}, {Slot: 2, Count: 4}}
	sortRoutingSlots(slots)
	if slots[0].Slot != 1 || slots[1].Slot != 2 || slots[2].Slot != 3 {
		t.Errorf("slot 分布应按序号升序，实际 %v", slots)
	}

	stops := []RoutingStopStat{
		{Reason: "b", Source: "upstream", Count: 2},
		{Reason: "a", Source: "config", Count: 2},
		{Reason: "a", Source: "client", Count: 2},
	}
	sortRoutingStops(stops)
	wantStops := []struct{ reason, source string }{{"a", "client"}, {"a", "config"}, {"b", "upstream"}}
	for i, want := range wantStops {
		if stops[i].Reason != want.reason || stops[i].Source != want.source {
			t.Fatalf("stops 排序第 %d 位 = (%q,%q)，期望 (%q,%q)", i, stops[i].Reason, stops[i].Source, want.reason, want.source)
		}
	}
}

func rotateRoutingReasons(in []RoutingReasonStat, shift int) []RoutingReasonStat {
	out := make([]RoutingReasonStat, 0, len(in))
	for i := range in {
		out = append(out, in[(i+shift)%len(in)])
	}
	return out
}

// 判据 13：空窗口返回空切片而不是错误，且 window 越界被夹回。
func TestRoutingEmptyWindowAndClamp(t *testing.T) {
	conn := withAnalyticsDB(t)

	out := routingProfileOf(t, conn, 100)
	if out.Window != 0 {
		t.Fatalf("空库 Window = %d，期望 0", out.Window)
	}
	if out.Reasons == nil || out.Modes == nil || out.Tiers == nil ||
		out.SlotDistribution == nil || out.Stops == nil {
		t.Errorf("空窗口必须返回空切片而非 nil（前端按数组消费）：%+v", out)
	}

	seedRoutingLog(t, conn, routingLogSeed{status: "success", decision: "mode=failover;reason=priority;slot=1"})
	if out := routingProfileOf(t, conn, 0); out.Window != 1 {
		t.Errorf("window=0 应回落到默认值，实际 Window=%d", out.Window)
	}
	if out := routingProfileOf(t, conn, 99999); out.Window != 1 {
		t.Errorf("window 超上限应被夹住且只取到 1 条，实际 Window=%d", out.Window)
	}
}
