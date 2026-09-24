package relay

import (
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// 本文件守 T-decision-001（判定理由）与 T-retry-003（请求非法时的取向）两条契约：
// 判定文本是响应头/日志/导出共用的同一份产物，取值集合与"描述的是哪个机制"必须稳定；
// 取向开关只能取两个值，非法值一律回落 failover（既有行为）。

func TestDecisionTextIsStable(t *testing.T) {
	full := Decision{Mode: model.GroupModeSmart, Tier: decisionTierDecision, Reason: decisionReasonAffinity, Slot: 2, Attempt: 3}
	if got, want := full.Text(), "mode=smart;tier=decision;reason=affinity;slot=2;attempt=3"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	// 非 smart 模式没有档位, 尚未发起上游请求时没有轮次: 两项都不该出现在文本里（不编造）。
	partial := Decision{Mode: model.GroupModeFailover, Reason: decisionReasonPriority}
	if got, want := partial.Text(), "mode=failover;reason=priority"; got != want {
		t.Fatalf("Text() = %q, want %q", got, want)
	}
	if strings.Contains(partial.Text(), "tier=") || strings.Contains(partial.Text(), "attempt=") {
		t.Fatalf("空字段不该出现在判定文本里: %q", partial.Text())
	}
}

func TestDecisionTierOnlyForSmart(t *testing.T) {
	if got := DecisionTier(model.GroupModeFailover, true); got != "" {
		t.Fatalf("非 smart 模式的档位应为空, got %q", got)
	}
	if got := DecisionTier(model.GroupModeSmart, true); got != decisionTierDecision {
		t.Fatalf("复杂请求应命中决策档, got %q", got)
	}
	if got := DecisionTier(model.GroupModeSmart, false); got != decisionTierExecution {
		t.Fatalf("简单请求应命中执行档, got %q", got)
	}
}

// TestDescribeDecisionReportsMechanism 逐个机制验一遍: 判定理由必须描述"谁决定了这次选择"，
// 而不是"谁更好"。四种机制用四个分组 ID 隔离（路由状态按分组 ID 持有）。
func TestDescribeDecisionReportsMechanism(t *testing.T) {
	now := time.Now().UnixMilli()

	manual := model.Group{ID: 9101, Mode: model.GroupModeManual, Items: []model.GroupItem{{ID: 1}}}
	if got := DescribeDecision(manual, 1, "", 1, 1).Reason; got != decisionReasonManual {
		t.Fatalf("手动模式理由 = %q, want manual", got)
	}

	failover := model.Group{ID: 9102, Mode: model.GroupModeFailover, Items: []model.GroupItem{{ID: 2}}}
	if got := DescribeDecision(failover, 2, "", 1, 1).Reason; got != decisionReasonPriority {
		t.Fatalf("故障转移（未开加权轮询）理由 = %q, want priority", got)
	}

	weighted := model.Group{ID: 9103, Mode: model.GroupModeWeighted, Items: []model.GroupItem{{ID: 3}}}
	if got := DescribeDecision(weighted, 3, "", 1, 1).Reason; got != decisionReasonRanked {
		t.Fatalf("综合排序模式理由 = %q, want ranked", got)
	}

	// 亲和期内沿用当前成员: 需要先把路由状态摆成"当前 + 未过期亲和"。
	affinity := model.Group{ID: 9104, Mode: model.GroupModeFailover, Items: []model.GroupItem{{ID: 4}}}
	affinityRoute := RouteStateOf(affinity)
	affinityRoute.GroupID = affinity.ID
	routes[affinity.ID] = &RouteState{GroupID: affinity.ID, CurrentItemID: 4, AffinityUntil: now + 60000,
		Cooldowns: map[int]int64{}}
	t.Cleanup(func() { ResetRouteState(affinity.ID) })
	if got := DescribeDecision(affinity, 4, "", 1, 1).Reason; got != decisionReasonAffinity {
		t.Fatalf("亲和期内理由 = %q, want affinity", got)
	}

	// 冷却恢复探测放行: 探针槽位指的是这个成员。
	probe := model.Group{ID: 9105, Mode: model.GroupModeFailover, Items: []model.GroupItem{{ID: 5}}}
	routes[probe.ID] = &RouteState{GroupID: probe.ID, ProbeItemID: 5, Cooldowns: map[int]int64{}}
	t.Cleanup(func() { ResetRouteState(probe.ID) })
	if got := DescribeDecision(probe, 5, "", 1, 1).Reason; got != decisionReasonProbe {
		t.Fatalf("探测放行理由 = %q, want probe", got)
	}

	// 判定只读: 描述一次不该改动路由状态（否则排障动作本身会改变行为）。
	before := RouteStateOf(affinity)
	_ = DescribeDecision(affinity, 4, "", 1, 1)
	after := RouteStateOf(affinity)
	if before.CurrentItemID != after.CurrentItemID || before.AffinityUntil != after.AffinityUntil {
		t.Fatalf("DescribeDecision 改动了路由状态: before=%+v after=%+v", before, after)
	}
}

func TestTopSlotMapsThroughTopCounts(t *testing.T) {
	flat := []model.GroupItem{{ID: 11}, {ID: 12}, {ID: 13}, {ID: 14}, {ID: 15}}
	topCounts := []int{2, 3} // 顶层成员 1 = 展平前两条, 顶层成员 2 = 后三条（子分组整条链归档）
	want := map[int]int{11: 1, 12: 1, 13: 2, 14: 2, 15: 2}
	for id, slot := range want {
		if got := TopSlot(flat, topCounts, id); got != slot {
			t.Fatalf("TopSlot(%d) = %d, want %d", id, got, slot)
		}
	}
	if got := TopSlot(flat, topCounts, 999); got != 0 {
		t.Fatalf("未知成员的序号应为 0（不编造）, got %d", got)
	}
	if got := TopSlot(nil, nil, 11); got != 0 {
		t.Fatalf("空输入应为 0, got %d", got)
	}
}

func TestRequestFaultActionOnlyTwoValues(t *testing.T) {
	t.Cleanup(func() { SetRequestFaultAction(RequestFaultActionFailover) })
	SetRequestFaultAction(RequestFaultActionFailFast)
	if got := RequestFaultAction(); got != RequestFaultActionFailFast {
		t.Fatalf("切换失败: %q", got)
	}
	// 非法取值回落默认 failover: 宁可走既有行为, 也不进入"未知状态"。
	SetRequestFaultAction("whatever")
	if got := RequestFaultAction(); got != RequestFaultActionFailover {
		t.Fatalf("非法取值应回落 failover, got %q", got)
	}
	SetRequestFaultAction(RequestFaultActionFailover)
	if got := RequestFaultAction(); got != RequestFaultActionFailover {
		t.Fatalf("默认值 = %q, want failover", got)
	}
}

// TestSystemOneDecisionHasNoGroupMode 守 T-decision-002：不经分组选路的独立入口
// 报告的必须是「没有选路」，而不是借用某个分组模式。
//
// 历史形态是字面量 "mode=systemone;reason=direct" —— mode 的值既不在 model.GroupMode
// 的枚举内（IsValid 与三处 binding oneof 都不认），也不对应任何真实分组，
// 下游只能把它当成一个未知模式原样显示。
func TestSystemOneDecisionHasNoGroupMode(t *testing.T) {
	text := SystemOneDecision().Text()
	if text != "reason=direct" {
		t.Fatalf("独立入口的判定文本 = %q, want %q", text, "reason=direct")
	}
	// 没有分组就没有模式、档位、序号与轮次：这些字段一个都不该出现（不编造）。
	for _, banned := range []string{"mode=", "tier=", "slot=", "attempt="} {
		if strings.Contains(text, banned) {
			t.Fatalf("独立入口没有分组可依据，不该出现 %q: %q", banned, text)
		}
	}
	// direct 描述的是「没有选路」这件事本身，它不是分组模式。这里锁住这一点：
	// 将来若有人为了让 systemone 能进「按模式分布」而把它加进 GroupMode 枚举，
	// 会让统计把它算成一个分组模式（并且前端模式下拉里会多出一个不能选的值）。
	if model.GroupMode(decisionReasonDirect).IsValid() {
		t.Fatal("direct 是判定理由，不是分组模式；把它写进 mode 段会让统计把它算成一个分组模式")
	}
}

// TestDescribeDecisionNeverReportsDirect 是上一条的反向对照：
// direct 只属于「没有选路」的路径，选路路径若报出它，说明这个词被当成了第六种机制用 ——
// 它描述的是「没选路」，出现在选过路的场合是自相矛盾的。
func TestDescribeDecisionNeverReportsDirect(t *testing.T) {
	modes := []model.GroupMode{
		model.GroupModeManual, model.GroupModeFailover, model.GroupModeWeighted,
		model.GroupModeLowestCost, model.GroupModeQualityFirst, model.GroupModeLowestLatency,
		model.GroupModeLeastBusy, model.GroupModeLowestTpmRpm, model.GroupModeAllocate,
		model.GroupModeSmart,
	}
	for index, mode := range modes {
		group := model.Group{ID: 9200 + index, Mode: mode, Items: []model.GroupItem{{ID: index + 1}}}
		t.Cleanup(func() { ResetRouteState(group.ID) })
		got := DescribeDecision(group, index+1, "", 1, 1).Reason
		if got == decisionReasonDirect {
			t.Fatalf("模式 %q 的选路判定报出了 direct（那是「没有选路」的说法）", mode)
		}
		if got == "" {
			t.Fatalf("模式 %q 没有给出判定理由（三种出口都必须落到某一个机制上）", mode)
		}
	}
}
