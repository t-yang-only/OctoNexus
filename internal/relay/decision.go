package relay

import (
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// Decision 是一次选路的判定结果（T-decision-001）：把「这次为什么走了这个成员」记进
// 请求状态、历史日志与响应头。
//
// 它只读已经发生的事实（分组模式、命中档位、路由状态），**不参与选路**，也不改变任何
// 模式的行为 —— 因此可以在任意模式上开启而不担心影响既有语义。
// 线上排障需要它：此前只记「走了哪个成员」，回答不了「为什么是它」（是亲和保持？是冷却
// 恢复探测放行？还是排序刚好轮到它？），用户只能对着分组配置猜。
type Decision struct {
	Mode    model.GroupMode `json:"mode"`           // 分组模式。
	Tier    string          `json:"tier,omitempty"` // smart 模式命中的档位: decision / execution; 其他模式为空。
	Reason  string          `json:"reason"`         // 决定这次选人的机制: manual / affinity / probe / priority / ranked。
	Slot    int             `json:"slot"`           // 选中成员在分组里的顶层序号（1 起）。
	Attempt int             `json:"attempt"`        // 与面板「第几轮」同口径：首字竞速多路并发算一轮。
}

// 判定理由的取值。它们描述的是**哪个机制决定了这次选择**，不是"谁更好"：
//   - manual   人工在面板上指定的成员（手动模式恒为它）。
//   - affinity 当前成员仍在亲和期内，直接沿用（不重新选路）。
//   - probe    该成员刚冷却到期，被恢复探测放行（每分组同时只放行一个）。
//   - priority 按成员顺序取第一个可用（故障转移模式且未开加权轮询）。
//   - ranked   按某个综合维度排序后取首位（加权/最低成本/质量/延迟/最空闲/近期消耗）。
const (
	decisionReasonManual   = "manual"
	decisionReasonAffinity = "affinity"
	decisionReasonProbe    = "probe"
	decisionReasonPriority = "priority"
	decisionReasonRanked   = "ranked"
)

// smart 模式的两档档位名。
const (
	decisionTierDecision  = "decision"
	decisionTierExecution = "execution"
)

// DecisionText 把判定结果压成一行机器可读文本（响应头、历史日志、导出列共用同一份，
// 避免三处各写一套）。不含任何渠道名或凭据，只有模式/档位/理由/序号/轮次。
func (d Decision) Text() string {
	parts := make([]string, 0, 5)
	if d.Mode != "" {
		parts = append(parts, "mode="+string(d.Mode))
	}
	if d.Tier != "" {
		parts = append(parts, "tier="+d.Tier)
	}
	if d.Reason != "" {
		parts = append(parts, "reason="+d.Reason)
	}
	if d.Slot > 0 {
		parts = append(parts, fmt.Sprintf("slot=%d", d.Slot))
	}
	if d.Attempt > 0 {
		parts = append(parts, fmt.Sprintf("attempt=%d", d.Attempt))
	}
	return strings.Join(parts, ";")
}

// DecisionTier 报告 smart 模式下这次请求命中的档位；非 smart 模式返回空
// （档位只是 smart 的概念，其它模式不该被它污染）。
func DecisionTier(mode model.GroupMode, complex bool) string {
	if mode != model.GroupModeSmart {
		return ""
	}
	if complex {
		return decisionTierDecision
	}
	return decisionTierExecution
}

// DescribeDecision 读当前分组的路由状态，报告这次为什么选中了 itemID。
// 只读取路由状态副本（RouteStateOf 内部加锁），不改冷却/亲和/探测槽位。
func DescribeDecision(group model.Group, itemID int, tier string, attempt, slot int) Decision {
	decision := Decision{Mode: group.Mode, Tier: tier, Slot: slot, Attempt: attempt}
	route := RouteStateOf(group)
	now := time.Now().UnixMilli()
	switch {
	case group.Mode == model.GroupModeManual:
		// 手动模式的成员由人工指定，其余机制都不参与（route 里的亲和/探测恒为 0）。
		decision.Reason = decisionReasonManual
	case itemID != 0 && route.CurrentItemID == itemID && route.AffinityUntil > now:
		decision.Reason = decisionReasonAffinity
	case itemID != 0 && route.ProbeItemID == itemID:
		decision.Reason = decisionReasonProbe
	case group.Mode == model.GroupModeFailover && !RouteBalanceEnabled():
		decision.Reason = decisionReasonPriority
	default:
		decision.Reason = decisionReasonRanked
	}
	return decision
}

// systemOneDecision 报告一次自定义协议评估请求（/v1/systemone）的选路判定。
//
// 这条路径**与其它协议一样走分组选路**（ForwardSystemOne 里调 PickGroupItem 选人），
// 所以判定文本必须与它们同源：真实的分组模式、真实的机制理由、真实的成员序号。
//
// 历史形态是字面量 "mode=systemone;reason=direct"，两个值都是假的 —— 它写了一个不在
// model.GroupMode 枚举里的"模式"（systemone 不是分组模式），又写了一个不在 decisionReason*
// 里的"理由"（direct）；而这次请求明明有分组、明明按某个机制选了人。
// T-decision-001 的全部意义就是回答「为什么走了这个成员」，那行文本恰好把答案抹掉了。
//
// complexRequest 传 false：systemone 的请求体是 state + questions，没有消息轮数、没有工具，
// 复杂度评分必然落在最低档 —— 若分组是 smart 模式，命中的就是执行引擎档。
// round 传 1：这条路径单次尝试，没有重试轮次（与首字竞速的「多路并发算一轮」无关）。
func systemOneDecision(group model.Group, flat []model.GroupItem, topCounts []int, itemID int) Decision {
	return DescribeDecision(group, itemID, DecisionTier(group.Mode, false), 1, TopSlot(flat, topCounts, itemID))
}

// TopSlot 报告成员在分组里的顶层序号（1 起，子分组整条链归它引用的那个顶层成员）。
// 传展平后的成员表与各顶层成员贡献的条数（op.FlattenGroupItemsWithTopCounts）。
// 找不到该成员时返回 0（序号未知，判定文本里会省略这一项，不编造）。
func TopSlot(flat []model.GroupItem, topCounts []int, itemID int) int {
	if itemID == 0 || len(flat) == 0 || len(topCounts) == 0 {
		return 0
	}
	flatIndex := -1
	for index, member := range flat {
		if member.ID == itemID {
			flatIndex = index
			break
		}
	}
	if flatIndex < 0 {
		return 0
	}
	cumulative := 0
	for index, count := range topCounts {
		if count < 1 {
			count = 1 // 结构异常时按"每个顶层成员至少一条"推进，不至于把序号算成 0。
		}
		cumulative += count
		if flatIndex < cumulative {
			return index + 1
		}
	}
	return 0
}
