package op

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// T-insight-006 模型链路一致性的判据。
//
// 这组用例的重点不是"字段有没有值"，而是**三态口径**：
// 正常别名解析 / 上游真的换了模型 / 上游沉默无法判定 —— 三者混起来会给用户一个错误结论。

// chainOf 取指定渠道的链路统计，取不到直接失败（避免用裸索引 panic 掉同进程里后面的用例）。
func chainOf(t *testing.T, out AnalyticsOverview, channel string) ModelChainStat {
	t.Helper()
	for _, stat := range out.ModelChain {
		if stat.Channel == channel {
			return stat
		}
	}
	t.Fatalf("model_chain 里没有渠道 %q，实际有 %d 项", channel, len(out.ModelChain))
	return ModelChainStat{}
}

// TestModelChainAliasIsNotMismatch 钉住本轮设计的核心：
//
//	请求名 ≠ 目标名 是**正常别名解析**（分组名→渠道内模型名），不是上游换模型。
//	实测踩过：拿请求名去比上游回报名，会把 42 条正常路由判成"模型被换"。
func TestModelChainAliasIsNotMismatch(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := time.Now().Add(-time.Hour)
	// 请求分组名 High-flash → 渠道内 glm-5.3-flash → 上游回报 glm-5.3-flash（同一层，一致）
	for i := 0; i < 3; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "High-flash", channel: "53HK-L",
			targetModel: "glm-5.3-flash", reportedModel: "glm-5.3-flash", mismatch: false,
			startedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	stat := chainOf(t, out, "53HK-L")
	if stat.Requests != 3 {
		t.Fatalf("requests = %d, want 3", stat.Requests)
	}
	if stat.AliasResolved != 3 {
		t.Fatalf("别名解析数 = %d, want 3（请求名与目标名不同）", stat.AliasResolved)
	}
	if stat.Mismatched != 0 {
		t.Fatalf("不匹配数 = %d, want 0 —— 别名解析不是上游换模型", stat.Mismatched)
	}
	if stat.Matched != 3 {
		t.Fatalf("一致数 = %d, want 3", stat.Matched)
	}
	if stat.MismatchRate != 0 {
		t.Fatalf("不匹配率 = %v, want 0", stat.MismatchRate)
	}
}

// TestModelChainThreeStatesSumUp 三态守恒：一致 + 不一致 == 回报数，回报数 + 沉默数 == 请求数。
// 守恒式是这类"分桶统计"最容易破的（漏一类、重复计一类都看得出来）。
func TestModelChainThreeStatesSumUp(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := time.Now().Add(-time.Hour)
	seed := func(i int, reported string, mismatch bool) {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "g", channel: "C1",
			targetModel: "m", reportedModel: reported, mismatch: mismatch,
			startedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	seed(0, "m", false)
	seed(1, "m", false)
	seed(2, "other", true)
	seed(3, "", false) // 上游沉默
	seed(4, "", false) // 上游沉默

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	stat := chainOf(t, out, "C1")
	if stat.Matched+stat.Mismatched != stat.Reported {
		t.Fatalf("一致(%d)+不一致(%d) != 回报数(%d)", stat.Matched, stat.Mismatched, stat.Reported)
	}
	if stat.Reported+stat.Silent != stat.Requests {
		t.Fatalf("回报数(%d)+沉默数(%d) != 请求数(%d)", stat.Reported, stat.Silent, stat.Requests)
	}
	if stat.Reported != 3 || stat.Silent != 2 {
		t.Fatalf("回报数 = %d（want 3）、沉默数 = %d（want 2）", stat.Reported, stat.Silent)
	}
}

// TestModelChainRateDenominatorIsReported 不匹配率的分母必须是"上游真的回报了"的行数。
//
// 用请求数当分母时，一个 90% 沉默的渠道会把 50% 的不匹配稀释成 5%，界面显示成"很健康"。
// 这条用例把两个数字拉开 10 倍，任何用错分母的实现都躲不过。
func TestModelChainRateDenominatorIsReported(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := time.Now().Add(-2 * time.Hour)
	// 90 条沉默
	for i := 0; i < 90; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "g", channel: "quiet",
			targetModel: "m", reportedModel: "",
			startedAt: base.Add(time.Duration(i) * time.Second),
		})
	}
	// 10 条回报，其中 5 条不一致
	for i := 0; i < 10; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "g", channel: "quiet",
			targetModel: "m", reportedModel: "m", mismatch: i < 5,
			startedAt: base.Add(time.Duration(100+i) * time.Second),
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 200)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	stat := chainOf(t, out, "quiet")
	if stat.Mismatched != 5 || stat.Reported != 10 || stat.Requests != 100 {
		t.Fatalf("不一致=%d（want 5）回报=%d（want 10）请求=%d（want 100）",
			stat.Mismatched, stat.Reported, stat.Requests)
	}
	// 5/10 = 50%，不是 5/100 = 5%
	if stat.MismatchRate < 49.9 || stat.MismatchRate > 50.1 {
		t.Fatalf("不匹配率 = %v, want 50（分母应为回报数 10，不是请求数 100）", stat.MismatchRate)
	}
}

// TestModelChainSilentIsNeitherMatchedNorMismatched 上游沉默既不算一致也不算不一致。
// 把它并进一致会让"上游从不回报"的渠道显示成 100% 一致。
func TestModelChainSilentIsNeitherMatchedNorMismatched(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 4; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "g", channel: "silent-ch",
			targetModel: "m", reportedModel: "",
			startedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	stat := chainOf(t, out, "silent-ch")
	if stat.Silent != 4 {
		t.Fatalf("沉默数 = %d, want 4", stat.Silent)
	}
	if stat.Matched != 0 || stat.Mismatched != 0 {
		t.Fatalf("沉默行被计进了判定：一致=%d 不一致=%d（都应为 0）", stat.Matched, stat.Mismatched)
	}
	if stat.MismatchRate != 0 {
		t.Fatalf("不匹配率 = %v, want 0（分母为 0 时返回 0 而不是 NaN）", stat.MismatchRate)
	}
}

// TestModelsDifferNormalizesLikeRelay 归一化必须与 relay 的 modelMismatch 同源：
// 裁剪空白 + 大小写不敏感；任一侧为空时不算不同（缺一侧名字无从比较）。
// 规则是两份（relay 依赖 op，不能反向引用），所以两处都要有用例看着。
func TestModelsDifferNormalizesLikeRelay(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"gpt-4o", "gpt-4o", false},
		{"GPT-4o", "gpt-4o", false},
		{"  gpt-4o  ", "gpt-4o", false},
		{"gpt-4o", "\tgpt-4o\n", false},
		{"gpt-4o", "gpt-4o-mini", true},
		{"", "gpt-4o", false},
		{"gpt-4o", "", false},
		{"", "", false},
		{"   ", "gpt-4o", false},
	}
	for _, c := range cases {
		if got := modelsDiffer(c.a, c.b); got != c.want {
			t.Fatalf("modelsDiffer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestModelChainEmptyModelNotCountedAsAlias 请求名或目标名缺失时不算"别名被解析过"。
// 未路由的请求（target_model 为空）不该凭空多出一个别名解析计数。
func TestModelChainEmptyModelNotCountedAsAlias(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := time.Now().Add(-time.Hour)
	seedAnalyticsLog(t, conn, analyticsLogSeed{
		status: "failed", modelName: "g", channel: "(未路由)", targetModel: "",
		startedAt: base,
	})

	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	stat := chainOf(t, out, "(未路由)")
	if stat.AliasResolved != 0 {
		t.Fatalf("别名解析数 = %d, want 0（目标名为空时无从比较）", stat.AliasResolved)
	}
	if stat.Requests != 1 {
		t.Fatalf("请求数 = %d, want 1（没模型名也要计入总请求）", stat.Requests)
	}
}

// TestSortModelChainStatsIsDeterministic 直接喂顺序确定的切片验证排序。
//
// 端到端用例（下面那条）走的是 map 遍历 → 切片，顺序随机会把"少写了末位键"的错误
// 实现偶发排对（本轮变异 M5 实测就是这样漏掉的）。所以排序必须用纯函数 + 确定输入再来一遍。
// 用例里刻意让"请求数相同、只有渠道名不同"的两项存在，末位键就是这个用例的全部意义。
func TestSortModelChainStatsIsDeterministic(t *testing.T) {
	// 故意打乱输入顺序，输出必须与输入顺序无关。
	stats := []ModelChainStat{
		{Channel: "zeta", Requests: 3, Mismatched: 0},
		{Channel: "dirty", Requests: 2, Mismatched: 2},
		{Channel: "alpha", Requests: 3, Mismatched: 0},
		{Channel: "clean", Requests: 5, Mismatched: 0},
		{Channel: "half", Requests: 4, Mismatched: 1},
	}
	for round := 0; round < 3; round++ {
		// 每轮换一个输入顺序：排序结果若依赖输入顺序，这里就会抖。
		rotated := append([]ModelChainStat{}, stats[round%len(stats):]...)
		rotated = append(rotated, stats[:round%len(stats)]...)
		sortModelChainStats(rotated)
		got := make([]string, 0, len(rotated))
		for _, stat := range rotated {
			got = append(got, stat.Channel)
		}
		want := "[dirty half clean alpha zeta]"
		if fmt.Sprint(got) != want {
			t.Fatalf("第 %d 轮排序 = %v, want %s（不匹配多的在前；同为 0 时按请求数倒序，再按渠道名升序）",
				round+1, got, want)
		}
	}
}

// TestModelChainSortsProblemsFirst 端到端补充：确实把有问题的渠道排到了最前。
func TestModelChainSortsProblemsFirst(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := time.Now().Add(-3 * time.Hour)
	add := func(channel string, n int, mismatched int) {
		for i := 0; i < n; i++ {
			seedAnalyticsLog(t, conn, analyticsLogSeed{
				status: "success", modelName: "g", channel: channel,
				targetModel: "m", reportedModel: "m", mismatch: i < mismatched,
				startedAt: base.Add(time.Duration(i) * time.Second),
			})
		}
	}
	add("clean", 5, 0)
	add("dirty", 2, 2)
	add("half", 4, 1)
	add("zeta", 3, 0)
	add("alpha", 3, 0)

	out, err := AnalyticsOverviewStats(context.Background(), 200)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	got := make([]string, 0, len(out.ModelChain))
	for _, stat := range out.ModelChain {
		got = append(got, stat.Channel)
	}
	want := []string{"dirty", "half", "clean", "alpha", "zeta"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("排序 = %v, want %v（不匹配多的在前；同为 0 时按请求数倒序，再按渠道名升序）", got, want)
	}
}

// TestModelChainSamplesAreMismatchOnlyAndCapped 样本只收真不匹配的行，且按时间倒序取最新的若干条。
func TestModelChainSamplesAreMismatchOnlyAndCapped(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := time.Now().Add(-24 * time.Hour)
	// 先播 30 条不匹配（时间较早）
	for i := 0; i < 30; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "want-me", channel: "C",
			targetModel: "m", reportedModel: "swapped", mismatch: true,
			startedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}
	// 再播 5 条正常行（时间更晚）—— 它们绝不能出现在样本里
	for i := 0; i < 5; i++ {
		seedAnalyticsLog(t, conn, analyticsLogSeed{
			status: "success", modelName: "g", channel: "C",
			targetModel: "m", reportedModel: "m",
			startedAt: base.Add(time.Duration(100+i) * time.Minute),
		})
	}

	out, err := AnalyticsOverviewStats(context.Background(), 200)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	if len(out.MismatchSamples) != modelMismatchSampleLimit {
		t.Fatalf("样本数 = %d, want %d（上限）", len(out.MismatchSamples), modelMismatchSampleLimit)
	}
	for _, sample := range out.MismatchSamples {
		if sample.ReportedModel != "swapped" {
			t.Fatalf("样本里混进了非不匹配行：%+v", sample)
		}
	}
	// rows 是 id DESC，所以收进来的一定是最新的那批（id 大 = 后播 = 时间晚）
	for i := 1; i < len(out.MismatchSamples); i++ {
		if out.MismatchSamples[i].ID >= out.MismatchSamples[i-1].ID {
			t.Fatalf("样本不是按时间倒序：%d 出现在 %d 之后",
				out.MismatchSamples[i].ID, out.MismatchSamples[i-1].ID)
		}
	}
}

// TestModelChainEmptyWindowReturnsEmptySlices 空窗口返回空切片而不是 nil，
// 否则前端 JSON 里是 null，`data.model_chain.length` 会抛错。
func TestModelChainEmptyWindowReturnsEmptySlices(t *testing.T) {
	withAnalyticsDB(t)
	out, err := AnalyticsOverviewStats(context.Background(), 100)
	if err != nil {
		t.Fatalf("AnalyticsOverviewStats: %v", err)
	}
	if out.ModelChain == nil {
		t.Fatalf("model_chain 是 nil，应为空切片")
	}
	if out.MismatchSamples == nil {
		t.Fatalf("mismatch_samples 是 nil，应为空切片")
	}
}
