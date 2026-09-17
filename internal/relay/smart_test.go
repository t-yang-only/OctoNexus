package relay

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// T-smart-001 智能路由（对齐阶跃 Step Router 的用法）：只填一个模型名（分组名），按请求特征定档。
// 这里只测纯逻辑：评分、特征提取、档位切分、阈值边界与"档内选不出人时回退全体"。

func TestSmartScoreGrowsWithRequestFeatures(t *testing.T) {
	// 空请求：三项特征都是 0，评分 0（走简单档）。
	if got := smartScore(0, 0, 0); got != 0 {
		t.Fatalf("空请求评分 = %d, 期望 0", got)
	}
	// 单项拉满：三项等权，单项满分 ⇒ 约 33 分（不足以单独越过默认阈值 50）。
	one := smartScore(smartScoreRoundsFull, 0, 0)
	if one != 33 {
		t.Fatalf("单项满分评分 = %d, 期望 33（三项等权）", one)
	}
	if SmartComplex(SmartFeatures{Score: one}, 50) {
		t.Fatalf("单项特征不该越过默认阈值 50: 评分 %d", one)
	}
	// 两项拉满：约 66 分 ⇒ 越过默认阈值。
	two := smartScore(smartScoreRoundsFull, smartScoreTokensFull, 0)
	if two != 66 || !SmartComplex(SmartFeatures{Score: two}, 50) {
		t.Fatalf("两项满分评分 = %d, 期望 66 且判为复杂", two)
	}
	// 单调性：每加一项特征，评分只升不降。
	values := []int{smartScore(1, 100, 0), smartScore(4, 4000, 4), smartScore(8, 8000, 8)}
	for i := 1; i < len(values); i++ {
		if values[i] < values[i-1] {
			t.Fatalf("评分不单调: %v", values)
		}
	}
	if values[len(values)-1] != 100 {
		t.Fatalf("三项拉满评分 = %d, 期望 100", values[len(values)-1])
	}
	// 超过"满分线"的特征不会把评分推过 100。
	if got := smartScore(999, 999999, 999); got != 100 {
		t.Fatalf("超大请求评分 = %d, 期望夹在 100", got)
	}
}

func TestSmartScoreBodyExtractsFeatures(t *testing.T) {
	chat := []byte(`{"model":"g","messages":[{"role":"user","content":"a"},{"role":"assistant","content":"b"},` +
		`{"role":"user","content":"c"}],"tools":[{"type":"function","function":{"name":"t","parameters":{}}}]}`)
	features := SmartScoreBody(chat)
	if features.Rounds != 3 {
		t.Fatalf("chat 轮数 = %d, 期望 3", features.Rounds)
	}
	if features.Tools != 1 {
		t.Fatalf("工具数 = %d, 期望 1", features.Tools)
	}
	if features.Tokens <= 0 {
		t.Fatalf("估算 token 应大于 0, 实际 %d", features.Tokens)
	}

	// responses 客户端的轮数看 input 数组；两处都有的正文按两处累加（转换层之前不做假设）。
	responses := []byte(`{"model":"g","input":[{"role":"user","content":[{"type":"input_text","text":"a"}]}]}`)
	if got := SmartScoreBody(responses).Rounds; got != 1 {
		t.Fatalf("responses 轮数 = %d, 期望 1", got)
	}
	both := []byte(`{"messages":[{"role":"user"}],"input":[{"role":"user"},{"role":"user"}]}`)
	if got := SmartScoreBody(both).Rounds; got != 3 {
		t.Fatalf("messages+input 轮数 = %d, 期望 3", got)
	}

	// 非法/空正文不该 panic，也不该给出特征（走简单档）。
	for _, body := range [][]byte{nil, {}, []byte("not json"), []byte(`{"messages":"not-an-array"}`)} {
		features := SmartScoreBody(body)
		if features.Rounds != 0 || features.Tools != 0 || features.Score != 0 {
			t.Fatalf("非数组/非法正文特征 = %+v, 期望全零（%s）", features, body)
		}
	}
}

func TestSmartComplexThresholdBoundaryAndFallback(t *testing.T) {
	// 评分恰好等于阈值算复杂（>= 口径），低一分算简单。
	if !SmartComplex(SmartFeatures{Score: 50}, 50) {
		t.Fatal("评分等于阈值应判为复杂")
	}
	if SmartComplex(SmartFeatures{Score: 49}, 50) {
		t.Fatal("评分低于阈值应判为简单")
	}
	// 阈值越界（0 或 >100，含存量分组 JSON 里没有这个键时的零值）按默认 50 处理。
	for _, threshold := range []int{0, -5, 101, 1000} {
		if !SmartComplex(SmartFeatures{Score: 50}, threshold) {
			t.Fatalf("阈值 %d 越界时未按默认 50 处理", threshold)
		}
		if SmartComplex(SmartFeatures{Score: 49}, threshold) {
			t.Fatalf("阈值 %d 越界时未按默认 50 处理（低分被判复杂）", threshold)
		}
	}
}

func TestSmartTierSplitKeepsOrderAndGivesComplexTheExtra(t *testing.T) {
	items := func(n int) []model.GroupItem {
		out := make([]model.GroupItem, 0, n)
		for i := 1; i <= n; i++ {
			out = append(out, model.GroupItem{ID: i, Available: true, Priority: i})
		}
		return out
	}
	ids := func(list []model.GroupItem) []int {
		out := make([]int, 0, len(list))
		for _, item := range list {
			out = append(out, item.ID)
		}
		return out
	}
	cases := []struct {
		n                   int
		decision, execution []int
		comment             string
	}{
		{1, []int{1}, []int{1}, "单成员：两档都是它"},
		{2, []int{1}, []int{2}, "双成员：前决策后执行（与阶跃双引擎同构）"},
		{3, []int{1, 2}, []int{3}, "奇数：多出来的归决策档"},
		{4, []int{1, 2}, []int{3, 4}, "偶数：对半"},
		{5, []int{1, 2, 3}, []int{4, 5}, "奇数：多出来的归决策档"},
	}
	for _, testCase := range cases {
		// decisionMembers 传 0：调用方没给顶层结构时的兜底口径就是「整体对半」。
		gotDecision := ids(smartTierItems(items(testCase.n), 0, true))
		gotExecution := ids(smartTierItems(items(testCase.n), 0, false))
		if !equalInts(gotDecision, testCase.decision) || !equalInts(gotExecution, testCase.execution) {
			t.Fatalf("%d 个成员（%s）: 决策档=%v 执行档=%v, 期望 %v / %v",
				testCase.n, testCase.comment, gotDecision, gotExecution, testCase.decision, testCase.execution)
		}
	}
	if got := smartTierItems(nil, 0, true); got != nil {
		t.Fatalf("空成员应返回 nil, 实际 %v", got)
	}
}

// TestSmartTierSplitFollowsTopLevelItems 守住档位切分的粒度（T-smart-006）：档位按**顶层成员**切，
// 子分组（一条链）整体归入某一档，不会被从中间切开。
// 反例（修复前的口径）：决策链 1 名成员 + 执行链 2 名成员，按展平后的条数对半切会把执行链第一个成员
// 算进决策档 —— 复杂请求于是可能落到"便宜"的那条链上，与用户「强链走复杂」的排布相反。
func TestSmartTierSplitFollowsTopLevelItems(t *testing.T) {
	flat := []model.GroupItem{{ID: 1}, {ID: 2}, {ID: 3}} // 决策链 1 名 + 执行链 2 名（按引用顺序展平）
	ids := func(list []model.GroupItem) []int {
		out := make([]int, 0, len(list))
		for _, item := range list {
			out = append(out, item.ID)
		}
		return out
	}

	// 顶层结构 [1, 2]：决策档 = 第一个顶层成员贡献的 1 条；执行档 = 剩下的 2 条。
	decisionMembers := SmartDecisionMembers([]int{1, 2}, true)
	if decisionMembers != 1 {
		t.Fatalf("复杂请求的档位切分点 = %d, 期望 1（只含第一个顶层成员）", decisionMembers)
	}
	if got := ids(smartTierItems(flat, decisionMembers, true)); !equalInts(got, []int{1}) {
		t.Fatalf("复杂请求决策档 = %v, 期望 [1]（不能把执行链的成员算进来）", got)
	}
	executionMembers := SmartDecisionMembers([]int{1, 2}, false)
	if got := ids(smartTierItems(flat, executionMembers, false)); !equalInts(got, []int{2, 3}) {
		t.Fatalf("简单请求执行档 = %v, 期望 [2 3]", got)
	}

	// 顶层结构 [2, 1]：对半后决策档 = 前两个顶层成员（共 2 条）。
	if got := SmartDecisionMembers([]int{2, 1}, true); got != 2 {
		t.Fatalf("顶层 [2,1] 的切分点 = %d, 期望 2", got)
	}
	// 只有一个顶层成员：两档都是整条链（等价于不分档）。
	single := SmartDecisionMembers([]int{3}, true)
	if got := ids(smartTierItems(flat, single, true)); !equalInts(got, []int{1, 2, 3}) {
		t.Fatalf("单顶层成员的决策档 = %v, 期望整条链", got)
	}
	if got := ids(smartTierItems(flat, single, false)); !equalInts(got, []int{1, 2, 3}) {
		t.Fatalf("单顶层成员的执行档 = %v, 期望整条链（不能为空）", got)
	}
	// 空顶层结构（没有成员）不 panic。
	if got := SmartDecisionMembers(nil, true); got != 0 {
		t.Fatalf("空顶层结构的切分点 = %d, 期望 0", got)
	}
}

func TestPickGroupItemSmartUsesTierThenFallsBack(t *testing.T) {
	ResetRouteState(9)
	defer ResetRouteState(9)
	group := model.Group{
		ID:   9,
		Name: "smart",
		Mode: model.GroupModeSmart,
		Items: []model.GroupItem{
			{ID: 11, Available: true, Priority: 1}, // 决策引擎档（用户把强的排前面）
			{ID: 12, Available: true, Priority: 2}, // 执行引擎档
		},
		RelayConfig: model.GroupRelayConfig{SmartRouteThreshold: 50, MemberAffinitySeconds: 0},
	}
	complex := SmartRoute{Features: SmartFeatures{Score: 80, Rounds: 12, Tools: 9, Tokens: 9000}}
	simple := SmartRoute{Features: SmartFeatures{Score: 10, Rounds: 1, Tools: 0, Tokens: 40}}

	if got := pickGroupItemSmart(group, routeDeps{}, false, complex); got.ID != 11 {
		t.Fatalf("复杂请求命中成员 %d, 期望 11（决策引擎档）", got.ID)
	}
	ResetRouteState(9) // 清掉上一轮写入的 CurrentItemID，避免亲和影响下一断言
	if got := pickGroupItemSmart(group, routeDeps{}, false, simple); got.ID != 12 {
		t.Fatalf("简单请求命中成员 %d, 期望 12（执行引擎档）", got.ID)
	}

	// 档位切分点由调用方给出（顶层成员口径）：两个顶层成员、各 1 条 → 切分点 1，与兜底口径一致。
	explicit := SmartRoute{Features: complex.Features, DecisionMembers: SmartDecisionMembers([]int{1, 1}, true)}
	ResetRouteState(9)
	if got := pickGroupItemSmart(group, routeDeps{}, false, explicit); got.ID != 11 {
		t.Fatalf("显式切分点 1 的复杂请求命中 %d, 期望 11", got.ID)
	}

	// 决策档整体冷却：复杂请求不该失败，也不该把请求丢到冷却成员上——
	// 回退到全体成员后由既有语义选出执行档（这正是"省钱不能变成不可用"）。
	ResetRouteState(9)
	routeMu.Lock()
	route := groupRouteLocked(group)
	route.Cooldowns = map[int]int64{11: time.Now().UnixMilli() + 60_000}
	route.CurrentItemID = 0
	routeMu.Unlock()
	if got := pickGroupItemSmart(group, routeDeps{}, false, complex); got.ID != 12 {
		t.Fatalf("决策档冷却时命中成员 %d, 期望 12（回退到执行档）", got.ID)
	}

	// 档内只剩一个成员时也是它（单成员分组不会因为分档而选不出人）。
	single := group
	single.Items = []model.GroupItem{{ID: 21, Available: true, Priority: 1}}
	ResetRouteState(9)
	if got := pickGroupItemSmart(single, routeDeps{}, false, complex); got.ID != 21 {
		t.Fatalf("单成员分组的复杂请求命中 %d, 期望 21", got.ID)
	}
}

// TestAffinityNeverLeavesTheCandidateSet 钉住「亲和不会把人带出候选集合」这条不变量（T-smart-007 的核查结论）。
//
// 核查起因：智能路由只把选中的那一档传给 pickGroupItem，而亲和分支 `return itemOf(group, CurrentItemID)`
// 在成员不在候选集合里时返回零值 —— 表面上会让调用方以为「档内没人」而回退到全体成员，
// 把另一档的亲和成员请回来（简单请求被改路由到强成员 = 档位的成本控制被绕过）。
//
// 实测结论：**不会**。groupRouteLocked 每次选路都按传入的候选集合校验一次路由状态
// （冷却表 / 探测占用 / CurrentItemID+亲和+affinityArmed 只要不在集合里就清掉），
// 所以进入亲和分支时 CurrentItemID 要么在集合里、要么已被清零而根本不进分支 —— 那条零值返回路径不可达。
// 把亲和分支改回旧写法（变异 SA1）本用例依旧全绿，正是因为它不可达：这不是判据空转，而是缺陷假设被证伪。
//
// 因此本用例是**不变量守卫**（不是回归判据）：档外亲和不得改路由到档外成员，且状态会被就地清掉；
// 反向对照保证档内亲和仍然沿用（收口没有把亲和本身的作用丢掉）；最后一条是同一不变量的通用形态。
//
// 已知边界（未采用的设计改动）：亲和是**每个分组一个槽位**（CurrentItemID/AffinityUntil），两个档共用一个槽
// —— 档外请求会把这枚亲和清掉，紧接着另一档的请求也就没有亲和保护了。按档各存一份亲和要求新增状态与迁移，
// 收益（少一点抖动）不抵复杂度，故维持现状。
func TestAffinityNeverLeavesTheCandidateSet(t *testing.T) {
	const groupID = 77
	ResetRouteState(groupID)
	defer ResetRouteState(groupID)

	group := model.Group{
		ID:   groupID,
		Name: "smart-affinity",
		Mode: model.GroupModeSmart,
		Items: []model.GroupItem{
			{ID: 71, Available: true, Priority: 1}, // 决策档（亲和将落在它身上）
			{ID: 72, Available: true, Priority: 2}, // 执行档（简单请求应当用它）
		},
		RelayConfig: model.GroupRelayConfig{SmartRouteThreshold: 50, MemberAffinitySeconds: 60},
	}
	simple := SmartRoute{Features: SmartFeatures{Score: 10}}

	// 直接注入路由状态：当前成员 = 决策档成员 71，且处于亲和期内。
	routeMu.Lock()
	route := groupRouteLocked(group)
	route.CurrentItemID = 71
	route.AffinityUntil = time.Now().UnixMilli() + 60_000
	routeMu.Unlock()

	if got := pickGroupItemSmart(group, routeDeps{}, false, simple); got.ID != 72 {
		t.Fatalf("简单请求命中成员 %d, 期望 72（执行档）—— 档外亲和不得改路由到决策档", got.ID)
	}
	// 状态侧可观测事实：档外亲和被就地清掉，当前成员换成档内选中的 72。
	if state := RouteStateOf(group); state.CurrentItemID != 72 || state.AffinityUntil != 0 {
		t.Fatalf("档外亲和应被清除并改写当前成员, 实际 current=%d affinity_until=%d",
			state.CurrentItemID, state.AffinityUntil)
	}

	// 反向对照：亲和成员落在**本次选中档**里时仍然沿用（收口没有把亲和本身的作用丢掉）。
	ResetRouteState(groupID)
	routeMu.Lock()
	route = groupRouteLocked(group)
	route.CurrentItemID = 72
	route.AffinityUntil = time.Now().UnixMilli() + 60_000
	routeMu.Unlock()
	if got := pickGroupItemSmart(group, routeDeps{}, false, simple); got.ID != 72 {
		t.Fatalf("档内亲和应沿用成员 72, 实际 %d", got.ID)
	}

	// 同一不变量的通用形态（与档位无关）：故障转移下把亲和成员从候选集合里剔除，
	// 选路必须落在集合内的成员上，而不是返回零值让调用方空等。
	ResetRouteState(groupID)
	failover := group
	failover.Mode = model.GroupModeFailover
	routeMu.Lock()
	route = groupRouteLocked(failover)
	route.CurrentItemID = 71
	route.AffinityUntil = time.Now().UnixMilli() + 60_000
	routeMu.Unlock()
	if got := PickGroupItem(failover, []model.GroupItem{{ID: 72, Available: true, Priority: 1}}); got.ID != 72 {
		t.Fatalf("候选集合不含亲和成员时应退回集合内选路, 实际 %d", got.ID)
	}
}

func TestSmartRouteDescriptionIsReadable(t *testing.T) {
	description := SmartRouteDescription(SmartFeatures{Score: 67, Rounds: 9, Tools: 10, Tokens: 8600}, 50, true)
	for _, want := range []string{"smart:", "复杂", "评分 67", "阈值 50", "轮数=9", "工具=10", "档位=decision"} {
		if !strings.Contains(description, want) {
			t.Fatalf("判定说明缺少 %q: %s", want, description)
		}
	}
	// 阈值越界时说明里显示生效值（默认 50），避免排障时被 0 误导。
	if got := SmartRouteDescription(SmartFeatures{Score: 10}, 0, false); !strings.Contains(got, "阈值 50") || !strings.Contains(got, "档位=execution") {
		t.Fatalf("阈值越界时说明不对: %s", got)
	}
}

// TestSmartModeIsRegisteredAndBackupSafe 守住"加模式要同步的地方"里最容易漏的两处：
// 模式枚举有效性（导入备份按 IsValid 校验）与 Relay 配置里阈值的默认/夹取。
func TestSmartModeIsRegisteredAndBackupSafe(t *testing.T) {
	if !model.GroupModeSmart.IsValid() {
		t.Fatal("smart 模式未登记进 IsValid，导入备份时会被判为非法模式")
	}
	// 面板与 API 的 oneof 校验靠字符串，这里用 JSON 往返确认字段名没写错。
	payload, err := json.Marshal(model.GroupRelayConfig{SmartRouteThreshold: 70})
	if err != nil {
		t.Fatalf("序列化失败: %v", err)
	}
	if !strings.Contains(string(payload), `"smart_route_threshold":70`) {
		t.Fatalf("阈值字段名不对（备份/接口都按这个键走）: %s", payload)
	}
	var decoded model.GroupRelayConfig
	if err := json.Unmarshal([]byte(`{"smart_route_threshold":70}`), &decoded); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if decoded.SmartRouteThreshold != 70 {
		t.Fatalf("阈值反序列化 = %d, 期望 70", decoded.SmartRouteThreshold)
	}
	// 默认值与夹取：0/越界都回到 50。
	if got := model.DefaultGroupRelayConfig().SmartRouteThreshold; got != 50 {
		t.Fatalf("默认阈值 = %d, 期望 50", got)
	}
	for _, value := range []int{0, -1, 200} {
		config := model.GroupRelayConfig{MemberMaxAttempts: 2, SmartRouteThreshold: value}
		model.NormalizeGroupRelayConfig(&config)
		if config.SmartRouteThreshold != 50 {
			t.Fatalf("阈值 %d 未被夹回默认 50, 实际 %d", value, config.SmartRouteThreshold)
		}
	}
	// 合法值必须原样保留（不能被夹走）。
	config := model.GroupRelayConfig{MemberMaxAttempts: 2, SmartRouteThreshold: 88}
	model.NormalizeGroupRelayConfig(&config)
	if config.SmartRouteThreshold != 88 {
		t.Fatalf("合法阈值被改动: %d", config.SmartRouteThreshold)
	}
}

// TestRankedHedgeCandidatesStaysInsideTier 守住智能路由与首字竞速的交叉点（T-smart-004）：
// 竞速只能在选中那一档内进行。否则简单请求会把靠前的强成员一起拉进竞速跑一遍，
// 复杂度分档的成本控制被竞速绕过（而竞速本身就是要多发请求的）。
func TestRankedHedgeCandidatesStaysInsideTier(t *testing.T) {
	ResetRouteState(21)
	defer ResetRouteState(21)
	group := model.Group{
		ID:   21,
		Name: "smart-hedge",
		Mode: model.GroupModeSmart,
		Items: []model.GroupItem{
			{ID: 31, Available: true, Priority: 1},
			{ID: 32, Available: true, Priority: 2},
			{ID: 33, Available: true, Priority: 3},
		},
		RelayConfig: model.GroupRelayConfig{SmartRouteThreshold: 50},
	}
	deps := routeDeps{}
	ids := func(list []model.GroupItem) []int {
		out := make([]int, 0, len(list))
		for _, item := range list {
			out = append(out, item.ID)
		}
		return out
	}
	contains := func(list []int, want int) bool {
		for _, value := range list {
			if value == want {
				return true
			}
		}
		return false
	}

	// 简单请求（执行引擎档 = 第 3 名成员）：候选里不能有决策档成员。
	simpleCandidates := ids(deps.rankedHedgeCandidatesWithFeatures(group, SmartRoute{Features: SmartFeatures{Score: 10}}))
	if contains(simpleCandidates, 31) || contains(simpleCandidates, 32) {
		t.Fatalf("简单请求把决策档成员拉进了竞速: %v", simpleCandidates)
	}
	if !contains(simpleCandidates, 33) {
		t.Fatalf("简单请求的竞速候选里没有执行档成员: %v", simpleCandidates)
	}

	// 复杂请求（决策引擎档 = 前 2 名成员）：候选里不能有执行档成员。
	complexCandidates := ids(deps.rankedHedgeCandidatesWithFeatures(group, SmartRoute{Features: SmartFeatures{Score: 90}}))
	if contains(complexCandidates, 33) {
		t.Fatalf("复杂请求把执行档成员拉进了竞速: %v", complexCandidates)
	}
	if len(complexCandidates) != 2 {
		t.Fatalf("复杂请求的竞速候选 = %v, 期望决策档两名成员", complexCandidates)
	}

	// 其它模式一律不受影响：failover 仍是全体成员参与。
	group.Mode = model.GroupModeFailover
	if got := ids(deps.rankedHedgeCandidatesWithFeatures(group, SmartRoute{Features: SmartFeatures{Score: 10}})); len(got) != 3 {
		t.Fatalf("failover 模式下竞速候选 = %v, 期望全体 3 名成员", got)
	}
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}
