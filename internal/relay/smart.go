package relay

import (
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/tidwall/gjson"
)

// 智能路由（model.GroupModeSmart，对齐阶跃 Step Router V1 的用法）：客户端只填一个模型名（分组名），
// 由选路层按请求特征判复杂度，复杂走「决策引擎档」、简单走「执行引擎档」。
//
// 为什么按请求特征而不是按成员统计：阶跃文档写得很明确 —— 判定依据是**请求**的消息轮数、输入量、工具数量，
// 引擎本身是固定的两档。落到本项目，用户自己的渠道就是那两档引擎，所以档位由成员顺序（Priority）决定：
// 靠前的成员是决策引擎（强/贵），靠后的是执行引擎（快/便宜）。这样不需要新字段、不动迁移、不依赖统计数据，
// 用户通过拖拽成员顺序就能表达「复杂走 A、简单走 B」；用子分组时更是天然的「两条链」两档。
//
// 与既有语义的关系：档内继续走既有的可用/冷却过滤与（可选的）加权轮询，档内没有可转发成员时**回退到全体成员**，
// 因此智能路由不会因为分档而让请求失败 —— 最坏情况与 failover 一致。

const (
	// smartScoreRoundsFull 轮数达到该值即算满分（阶跃文档把消息轮数列为判定依据之一）。
	smartScoreRoundsFull = 8
	// smartScoreTokensFull 估算输入 token 达到该值即算满分。
	smartScoreTokensFull = 8000
	// smartScoreToolsFull 工具数量达到该值即算满分。
	smartScoreToolsFull = 8
	// smartApproxBytesPerToken 正文字节数换算 token 的粗略比例（英文约 4 字节/token，中文更密，
	// 这里刻意用偏大的分母：宁可低估规模，也不要把简单请求误判成复杂请求、白白走贵的那档）。
	smartApproxBytesPerToken = 4
)

// SmartFeatures 是一次请求里用于判复杂度的特征。
type SmartFeatures struct {
	Rounds int // 消息/输入条目数
	Tokens int // 估算输入 token 数
	Tools  int // 工具数量
	Score  int // 0..100 的复杂度评分
}

// SmartRoute 是智能路由一次请求的完整判定输入：请求特征 + 档位切分点。
//
// DecisionMembers 是**决策引擎档在展平成员列表里的条数**（其余归执行引擎档）。它由调用方按顶层成员结构
// 算好（见 SmartDecisionMembers）：顶层成员（可能是子分组）整体归入某一档，子分组不会被从中间切开。
// 为 0（调用方没给结构，例如直接走兼容外壳）或 ≥ 成员总数（只有一个顶层成员）时退回整体对半 / 全量口径。
type SmartRoute struct {
	Features        SmartFeatures
	DecisionMembers int
}

// smartScore 把三个特征归一化到 0..100 后等权平均（阶跃文档列的三个依据权重相同）。
// 任一特征为 0 就是 0 分，不需要额外惩罚 —— 平均本身就让"只有一项大"的请求拿到中等分。
func smartScore(rounds, tokens, tools int) int {
	norm := func(value, full int) int {
		if value <= 0 {
			return 0
		}
		if value >= full {
			return 100
		}
		return value * 100 / full
	}
	return (norm(rounds, smartScoreRoundsFull) + norm(tokens, smartScoreTokensFull) + norm(tools, smartScoreToolsFull)) / 3
}

// SmartScoreBody 从客户端请求正文里提取特征并给出复杂度评分。
// 只读不写：拿不到任何字段（正文不是 JSON、字段缺失）时返回零值特征与 0 分，
// 调用方据此走「简单」那一档 —— 这与"宁可低估"的取向一致。
func SmartScoreBody(body []byte) SmartFeatures {
	features := SmartFeatures{}
	if len(body) == 0 {
		return features
	}
	if !gjson.ValidBytes(body) {
		return features
	}
	// 轮数：chat 的 messages、responses 的 input 都算；两者都没有时为 0。
	rounds := 0
	for _, field := range []string{"messages", "input"} {
		value := gjson.GetBytes(body, field)
		if value.IsArray() {
			rounds += len(value.Array())
		}
	}
	tools := 0
	if value := gjson.GetBytes(body, "tools"); value.IsArray() {
		tools = len(value.Array())
	}
	features.Rounds = rounds
	features.Tools = tools
	// 输入量：整份正文的字节数除以一个粗略系数。这里包含 tools 的 schema 与 instructions，
	// 因为它们同样会占上游的输入预算。
	features.Tokens = len(body) / smartApproxBytesPerToken
	features.Score = smartScore(features.Rounds, features.Tokens, features.Tools)
	return features
}

// SmartComplex 报告这次请求是否达到「复杂」档（评分 ≥ 阈值）。阈值来自分组配置，越界值按默认 50。
func SmartComplex(features SmartFeatures, threshold int) bool {
	if threshold < 1 || threshold > 100 {
		threshold = model.DefaultGroupRelayConfig().SmartRouteThreshold
	}
	return features.Score >= threshold
}

// SmartTierTopCount 返回按顶层成员条数切分时，决策引擎档包含几个顶层成员：
// 靠前的一半（奇数多出来的那一个归决策引擎档），保证「复杂请求可用的成员数 ≥ 简单请求可用的成员数」
// —— 复杂请求更需要选择余地。顶层成员数为 1 时两档都是它（等价于不分档）。
func SmartTierTopCount(topCount int, complex bool) int {
	if topCount <= 0 {
		return 0
	}
	half := (topCount + 1) / 2
	if complex {
		return half
	}
	if half >= topCount {
		return topCount
	}
	return half
}

// SmartDecisionMembers 把「决策档的顶层成员数」换算成展平列表里的条数（front 为 True 时取前 k 个顶层成员之和）：
// topCounts[i] 是第 i 个顶层成员展平后的成员条数（见 op.FlattenGroupItemsWithTopCounts）。
// 展平成员按顶层成员分段且连续，所以前缀和就是档位在平面表里的下标。
func SmartDecisionMembers(topCounts []int, complex bool) int {
	top := SmartTierTopCount(len(topCounts), complex)
	total := 0
	for i := 0; i < top && i < len(topCounts); i++ {
		total += topCounts[i]
	}
	return total
}

// smartTierItems 按档位切出本次请求要用的成员。
//
// 两种口径，显式优先：
//  1. 显式口径：只要有任意一个成员声明了档位（GroupItem.SmartTier，顶层声明会随展平下传给整条链），
//     就按声明切分 —— 复杂请求取 decision 档，简单请求取其余成员（execution 档 + 未声明的）。
//     目标档为空时返回全体成员：这是「标错一边」的兜底，档位是"优先考虑谁"而不是"只许用谁"。
//  2. 顺序口径（既有行为，逐字不变）：前 decisionMembers 个归决策引擎档，其余归执行引擎档。
//     decisionMembers 为 0 或 ≥ 成员总数时退回「整体对半 / 全量」口径（调用方没给顶层结构时的兜底）。
func smartTierItems(items []model.GroupItem, decisionMembers int, complex bool) []model.GroupItem {
	if len(items) == 0 {
		return nil
	}
	if hasExplicitSmartTier(items) {
		tiered := make([]model.GroupItem, 0, len(items))
		for _, item := range items {
			inDecision := item.SmartTier == model.GroupSmartTierDecision
			if inDecision == complex {
				tiered = append(tiered, item)
			}
		}
		if len(tiered) == 0 {
			// 声明的档位与本次请求的档位对不上（例如所有成员都标成决策引擎档，而这是个简单请求）：
			// 回退全体成员而不是返回空集 —— 空集会让请求直接失败，把一个标注问题升级成故障。
			return items
		}
		return tiered
	}
	if decisionMembers <= 0 {
		// 调用方没给顶层结构（例如直接走兼容外壳）：退回整体对半的旧口径。
		decisionMembers = (len(items) + 1) / 2
	}
	if decisionMembers > len(items) {
		decisionMembers = len(items)
	}
	if complex {
		return items[:decisionMembers]
	}
	if decisionMembers >= len(items) {
		// 只有一个顶层成员（整条链都在「决策档」）：两档都是它，不能退化成空集。
		return items
	}
	return items[decisionMembers:]
}

// hasExplicitSmartTier 报告这批成员里有没有人显式声明了档位。
// 声明与否决定整批成员走显式口径还是顺序口径，所以只要有一个就算。
func hasExplicitSmartTier(items []model.GroupItem) bool {
	for _, item := range items {
		if item.SmartTier != model.GroupSmartTierAuto {
			return true
		}
	}
	return false
}

// pickGroupItemSmart 智能路由的选路：先按复杂度选定档位，再在该档内沿用既有选路
// （加权轮询开启时走加权，否则按成员顺序/failover 语义），档内没有可转发成员时回退到全体成员。
//
// 回退的理由：档位只是「优先考虑谁」，不是「只许用谁」。目标档的成员全被冷却或渠道停用时，
// 回退到全体成员仍能出发 —— 否则智能路由就成了新的失败来源（这是它最容易踩的坑：
// 简单请求占多数，若执行引擎档整档掉线而这里只回退一次就放弃，会把「省钱」变成「不可用」）。
func pickGroupItemSmart(group model.Group, deps routeDeps, balanceEnabled bool, smart SmartRoute) model.GroupItem {
	threshold := group.RelayConfig.SmartRouteThreshold
	complex := SmartComplex(smart.Features, threshold)
	tiered := smartTierItems(group.Items, smart.DecisionMembers, complex)
	if len(tiered) == 0 {
		return model.GroupItem{}
	}
	group.Mode = model.GroupModeFailover // 档内按 failover 语义走既有选路（含加权开关）
	if item := pickGroupItemByMode(group.WithItems(tiered), deps, balanceEnabled); item.ID != 0 {
		return item
	}
	// 档内没有可转发成员：回退到全体成员，语义与故障转移一致。
	return pickGroupItemByMode(group.WithItems(group.Items), deps, balanceEnabled)
}

// smartTierNames 给日志/测试用：把成员按档位分组后返回两边的主键列表（保持原顺序）。
func smartTierNames(items []model.GroupItem, decisionMembers int) map[string][]int {
	out := map[string][]int{"decision": {}, "execution": {}}
	for _, item := range smartTierItems(items, decisionMembers, true) {
		out["decision"] = append(out["decision"], item.ID)
	}
	for _, item := range smartTierItems(items, decisionMembers, false) {
		out["execution"] = append(out["execution"], item.ID)
	}
	return out
}

// SmartRouteDescription 生成一句可读的判定说明，用于日志排障：
// 例如「smart: 复杂(评分 67/阈值 50) 轮数=9 工具=10 估算token=8600 档位=decision」。
func SmartRouteDescription(features SmartFeatures, threshold int, complex bool) string {
	tier := "execution"
	if complex {
		tier = "decision"
	}
	effective := threshold
	if effective < 1 || effective > 100 {
		effective = model.DefaultGroupRelayConfig().SmartRouteThreshold
	}
	return strings.Join([]string{
		"smart:",
		smartWord(complex),
		"评分", strconv.Itoa(features.Score), "/阈值", strconv.Itoa(effective),
		"轮数=" + strconv.Itoa(features.Rounds),
		"工具=" + strconv.Itoa(features.Tools),
		"估算token=" + strconv.Itoa(features.Tokens),
		"档位=" + tier,
	}, " ")
}

func smartWord(complex bool) string {
	if complex {
		return "复杂"
	}
	return "简单"
}
