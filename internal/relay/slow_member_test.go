package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// T-route-003 慢成员分区（splitSlow）的判据。
//
// 背景：冷却只惩罚**失败**的成员，而"成功但极慢"的成员永远不会被冷却 —— 实测一个
// 首字节 71~140 秒的上游在 failover 分组里按 priority 长期占据队首，每个请求都要先等
// 它把首事件超时耗完才换人（一次 163 秒）。本组用例锁住新行为：慢成员被移到候选队尾，
// 但仍然可用；保护关闭时既有顺序逐字不变。

func slowTestItems(n int) []model.GroupItem {
	items := make([]model.GroupItem, 0, n)
	for i := 1; i <= n; i++ {
		items = append(items, model.GroupItem{ID: i, Priority: i, Available: true})
	}
	return items
}

func slowLatencyStub(values map[int]int64) LatencyProvider {
	return func(itemID int) (int64, bool) {
		v, ok := values[itemID]
		return v, ok
	}
}

// withSlowLatency 设置判定数据源与阈值，并在用例结束后复位（两者都是包级状态）。
func withSlowLatency(t *testing.T, src LatencyProvider, thresholdMs int64) {
	t.Helper()
	prevSrc := slowLatencySource
	prevTh := routeSlowLatencyMs.Load()
	slowLatencySource = src
	SetRouteSlowLatencyMs(thresholdMs)
	t.Cleanup(func() {
		slowLatencySource = prevSrc
		routeSlowLatencyMs.Store(prevTh)
	})
}

func slowIDs(items []model.GroupItem) []int {
	ids := make([]int, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

// 阈值 0 = 关闭：整体返回原序，且不产生慢成员。
func TestSplitSlowDisabledKeepsOrder(t *testing.T) {
	items := slowTestItems(3)
	src := slowLatencyStub(map[int]int64{1: 999_999})
	fast, slow := splitSlow(items, src, 0)
	if len(slow) != 0 {
		t.Fatalf("阈值 0 时不应有慢成员, 得到 %v", slowIDs(slow))
	}
	if len(fast) != 3 {
		t.Fatalf("阈值 0 时应整体返回 3 个, 得到 %d", len(fast))
	}
	if got := slowIDs(fast); got[0] != 1 || got[2] != 3 {
		t.Fatalf("阈值 0 时应保持原序, 得到 %v", got)
	}
}

// 超过阈值的成员被分离出来，其余保持原序。
func TestSplitSlowSeparatesByThreshold(t *testing.T) {
	items := slowTestItems(3)
	src := slowLatencyStub(map[int]int64{2: 60_000})
	fast, slow := splitSlow(items, src, 30_000)
	if got := slowIDs(slow); len(got) != 1 || got[0] != 2 {
		t.Fatalf("慢成员应为 [2], 得到 %v", got)
	}
	if got := slowIDs(fast); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("正常成员应为 [1 3], 得到 %v", got)
	}
}

// 贴阈值两侧各取一个：恰好等于阈值不算慢（只认严格大于）。
func TestSplitSlowThresholdBoundary(t *testing.T) {
	items := slowTestItems(2)
	src := slowLatencyStub(map[int]int64{1: 30_000, 2: 30_001})
	fast, slow := splitSlow(items, src, 30_000)
	if got := slowIDs(fast); len(got) != 1 || got[0] != 1 {
		t.Fatalf("等于阈值应留在正常档, 得到 %v", got)
	}
	if got := slowIDs(slow); len(got) != 1 || got[0] != 2 {
		t.Fatalf("超过阈值 1 毫秒应进慢档, 得到 %v", got)
	}
}

// 无样本（新成员）保持乐观先验：不会被压到队尾。
func TestSplitSlowNoSampleIsNotSlow(t *testing.T) {
	items := slowTestItems(2)
	src := slowLatencyStub(map[int]int64{1: 90_000})
	fast, slow := splitSlow(items, src, 30_000)
	if got := slowIDs(slow); len(got) != 1 || got[0] != 1 {
		t.Fatalf("只有有样本的成员 1 应进慢档, 得到 %v", got)
	}
	if got := slowIDs(fast); len(got) != 1 || got[0] != 2 {
		t.Fatalf("无样本的成员 2 应留在正常档, 得到 %v", got)
	}
}

// 全部成员都慢：fast 为空但 slow 仍是候选的一部分（"全是慢成员" != "无可用成员"）。
func TestSplitSlowAllSlowKeepsCandidates(t *testing.T) {
	items := slowTestItems(2)
	src := slowLatencyStub(map[int]int64{1: 60_000, 2: 60_000})
	fast, slow := splitSlow(items, src, 30_000)
	if len(fast) != 0 {
		t.Fatalf("两个都慢时 fast 应为空, 得到 %v", slowIDs(fast))
	}
	if len(slow) != 2 {
		t.Fatalf("两个都慢时应全部进 slow, 得到 %d", len(slow))
	}
}

// 没有数据源或没有候选时是纯 no-op，不 panic。
func TestSplitSlowNoopCases(t *testing.T) {
	items := slowTestItems(2)
	if fast, slow := splitSlow(items, nil, 30_000); len(fast) != 2 || len(slow) != 0 {
		t.Fatalf("provider 为 nil 时应原样返回, 得到 fast=%v slow=%v", slowIDs(fast), slowIDs(slow))
	}
	src := slowLatencyStub(map[int]int64{1: 60_000})
	if fast, slow := splitSlow(nil, src, 30_000); len(fast) != 0 || len(slow) != 0 {
		t.Fatalf("空候选应返回空, 得到 fast=%v slow=%v", slowIDs(fast), slowIDs(slow))
	}
}

// 核心用例：failover（加权轮询定序）下慢成员不再占据队首。
// 改动前 rankCandidates 只看 priority，priority=1 的慢成员永远第一个被选中。
func TestRankCandidatesPutsSlowMemberLast(t *testing.T) {
	items := slowTestItems(2)
	src := slowLatencyStub(map[int]int64{1: 120_000})

	// 先断开关闭时的既有行为：priority 小的在前。
	withSlowLatency(t, src, 0)
	ranked, err := rankCandidates(items, nil, 0, nil, nil, 0)
	if err != nil {
		t.Fatalf("关闭时定序失败: %v", err)
	}
	if len(ranked) == 0 || ranked[0].ID != 1 {
		t.Fatalf("关闭时首个应为 priority 最小的成员 1, 得到 %v", slowIDs(ranked))
	}

	// 再断言开启后的新行为：慢成员被移到队尾。
	withSlowLatency(t, src, 30_000)
	ranked, err = rankCandidates(items, nil, 0, nil, nil, 0)
	if err != nil {
		t.Fatalf("开启时定序失败: %v", err)
	}
	if len(ranked) != 2 {
		t.Fatalf("慢成员不该被剔除, 候选应仍是 2 个, 得到 %v", slowIDs(ranked))
	}
	if ranked[0].ID != 2 {
		t.Fatalf("开启后首个应为不慢的成员 2, 得到 %v", slowIDs(ranked))
	}
	if ranked[1].ID != 1 {
		t.Fatalf("慢成员 1 应排在队尾, 得到 %v", slowIDs(ranked))
	}
}

// 其它策略同样生效（这个保护是共用的，不是 failover 专属）。
func TestOtherStrategiesAlsoDemoteSlowMember(t *testing.T) {
	items := slowTestItems(2)
	src := slowLatencyStub(map[int]int64{1: 120_000})
	withSlowLatency(t, src, 30_000)

	quality := func(int) (float64, bool) { return 1.0, true }
	busy := func(int) int { return 0 }
	load := func(int) (int, int, bool) { return 0, 0, true }
	cost := func(model.GroupItem) (float64, bool) { return 1.0, true }

	cases := []struct {
		name string
		call func() ([]model.GroupItem, error)
	}{
		{"quality_first", func() ([]model.GroupItem, error) { return rankByQuality(items, nil, 0, nil, quality) }},
		{"least_busy", func() ([]model.GroupItem, error) { return rankByBusy(items, nil, 0, nil, busy) }},
		{"lowest_tpm_rpm", func() ([]model.GroupItem, error) { return rankByRecentLoad(items, nil, 0, nil, load) }},
		{"lowest_cost", func() ([]model.GroupItem, error) { return rankByLowestCost(items, nil, 0, nil, cost) }},
		{"lowest_latency", func() ([]model.GroupItem, error) { return rankByLatency(items, nil, 0, nil, src) }},
	}
	for _, tc := range cases {
		ranked, err := tc.call()
		if err != nil {
			t.Fatalf("%s 定序失败: %v", tc.name, err)
		}
		if len(ranked) != 2 {
			t.Fatalf("%s 慢成员不该被剔除, 得到 %v", tc.name, slowIDs(ranked))
		}
		if ranked[0].ID != 2 {
			t.Fatalf("%s 首个应为不慢的成员 2, 得到 %v", tc.name, slowIDs(ranked))
		}
	}
}

// 计数守恒：慢成员只是换位置，任何策略下候选总数都不因该保护而改变。
func TestSlowMemberIsNeverDropped(t *testing.T) {
	items := slowTestItems(4)
	src := slowLatencyStub(map[int]int64{1: 60_000, 2: 60_000, 3: 60_000, 4: 60_000})
	withSlowLatency(t, src, 30_000)
	ranked, err := rankCandidates(items, nil, 0, nil, nil, 0)
	if err != nil {
		t.Fatalf("全慢时不该报无可用成员: %v", err)
	}
	if len(ranked) != 4 {
		t.Fatalf("全慢时候选应仍是 4 个, 得到 %v", slowIDs(ranked))
	}
}

// 未注入（负值哨兵）时用默认阈值，而不是被当成"关闭" —— 否则默认保护会静默失效。
func TestSlowThresholdDefaultsWhenUnset(t *testing.T) {
	prevTh := routeSlowLatencyMs.Load()
	t.Cleanup(func() { routeSlowLatencyMs.Store(prevTh) })
	routeSlowLatencyMs.Store(routeSlowLatencyUnset)
	if got := slowLatencyThresholdMs(); got != defaultRouteSlowLatencyMs {
		t.Fatalf("未注入时应取默认阈值 %d, 得到 %d", defaultRouteSlowLatencyMs, got)
	}
	SetRouteSlowLatencyMs(0)
	if got := slowLatencyThresholdMs(); got != 0 {
		t.Fatalf("明确关闭时应为 0, 得到 %d", got)
	}
}

// 慢成员不进冷却：它只是被移到队尾，冷却集不受影响（慢 != 失败）。
func TestSlowMemberDoesNotEnterCooldown(t *testing.T) {
	items := slowTestItems(2)
	src := slowLatencyStub(map[int]int64{1: 120_000})
	withSlowLatency(t, src, 30_000)
	cooldowns := map[int]int64{}
	ranked, err := rankCandidates(items, cooldowns, 0, nil, nil, 0)
	if err != nil {
		t.Fatalf("定序失败: %v", err)
	}
	if len(cooldowns) != 0 {
		t.Fatalf("慢成员不该写入冷却表, 得到 %v", cooldowns)
	}
	if len(ranked) != 2 {
		t.Fatalf("慢成员不该被剔除, 得到 %v", slowIDs(ranked))
	}
}

// 轮询定序的纯函数对空集/参数不匹配是安全的。
// 调用点（rankCandidates）已经在 len(fast)==0 时绕开了它，所以这条守卫在端到端
// 用例里会被那层守卫掩盖 —— 单独立一条直接打它，否则"空集早返回"删掉也没人发现。
func TestSmoothWeightedOrderEmptyInputIsSafe(t *testing.T) {
	if got := smoothWeightedOrder(0, nil, 0, 0); len(got) != 0 {
		t.Fatalf("空集应返回空序列, 得到 %v", got)
	}
	if got := smoothWeightedOrder(3, []int{1, 2}, 3, 0); len(got) != 0 {
		t.Fatalf("权重个数少于成员数时应返回空序列, 得到 %v", got)
	}
}
