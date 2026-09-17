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
		gotDecision := ids(SmartTierItems(items(testCase.n), true))
		gotExecution := ids(SmartTierItems(items(testCase.n), false))
		if !equalInts(gotDecision, testCase.decision) || !equalInts(gotExecution, testCase.execution) {
			t.Fatalf("%d 个成员（%s）: 决策档=%v 执行档=%v, 期望 %v / %v",
				testCase.n, testCase.comment, gotDecision, gotExecution, testCase.decision, testCase.execution)
		}
	}
	if got := SmartTierItems(nil, true); got != nil {
		t.Fatalf("空成员应返回 nil, 实际 %v", got)
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
	complex := SmartFeatures{Score: 80, Rounds: 12, Tools: 9, Tokens: 9000}
	simple := SmartFeatures{Score: 10, Rounds: 1, Tools: 0, Tokens: 40}

	if got := pickGroupItemSmart(group, routeDeps{}, false, complex); got.ID != 11 {
		t.Fatalf("复杂请求命中成员 %d, 期望 11（决策引擎档）", got.ID)
	}
	ResetRouteState(9) // 清掉上一轮写入的 CurrentItemID，避免亲和影响下一断言
	if got := pickGroupItemSmart(group, routeDeps{}, false, simple); got.ID != 12 {
		t.Fatalf("简单请求命中成员 %d, 期望 12（执行引擎档）", got.ID)
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
