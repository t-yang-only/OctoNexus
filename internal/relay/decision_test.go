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

// TestSystemOneDecisionReportsRealRoute 守 T-decision-002：自定义协议评估请求
// （/v1/systemone）与其它协议**走同一套分组选路**，判定文本必须报告真实信息。
//
// 历史形态是字面量 "mode=systemone;reason=direct"，两个值都是假的：systemone 不在
// model.GroupMode 的枚举里（IsValid 与三处 binding oneof 都不认），direct 也不在
// decisionReason* 里 —— 而这次请求明明有分组、明明按某个机制选了人。
// T-decision-001 要回答的「为什么走了这个成员」，那行文本恰好把答案抹掉了。
func TestSystemOneDecisionReportsRealRoute(t *testing.T) {
	group := model.Group{ID: 9301, Mode: model.GroupModeAllocate}
	t.Cleanup(func() { ResetRouteState(group.ID) })
	// 展平成员表 + 各顶层成员贡献的条数：两个顶层成员各一条，所以第 2 条对应序号 2。
	flat := []model.GroupItem{{ID: 71}, {ID: 72}}
	topCounts := []int{1, 1}

	got := systemOneDecision(group, flat, topCounts, 72).Text()
	// allocate（非 smart、非 failover、未开加权轮询之外的排序）→ 按综合维度排序取首位；
	// 选中第 2 个成员、单次尝试的第 1 轮。
	if want := "mode=allocate;reason=ranked;slot=2;attempt=1"; got != want {
		t.Fatalf("评估请求的判定文本 = %q, want %q", got, want)
	}
	// 反向对照：历史上那两个写死的值一个都不许再出现。
	for _, banned := range []string{"systemone", "direct"} {
		if strings.Contains(got, banned) {
			t.Fatalf("判定文本残留了写死的入口名/机制名 %q: %q", banned, got)
		}
	}

	// smart 分组下必须给出真实档位：评估请求体是 state + questions，没有消息轮数与工具，
	// 复杂度评分必然落最低档，因此命中执行引擎档。
	smart := model.Group{ID: 9302, Mode: model.GroupModeSmart}
	t.Cleanup(func() { ResetRouteState(smart.ID) })
	if got, want := systemOneDecision(smart, flat, topCounts, 71).Text(),
		"mode=smart;tier=execution;reason=ranked;slot=1;attempt=1"; got != want {
		t.Fatalf("smart 分组的评估请求判定 = %q, want %q", got, want)
	}
}
