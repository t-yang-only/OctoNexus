package handlers

import (
	"reflect"
	"testing"
)

// T-usability-006 「配置 vs 上游」对比的判据。
//
// ## 这个功能的两种失败方式
//
//   - **误报**：把上游明明有的模型报成「上游没有」。用户照着清理，
//     会删掉本来能用的配置 —— 这是不可逆的破坏，也是本功能最危险的失效方式。
//
//   - **漏报**：上游没有的没被发现。功能白做，但数据没坏。
//
// 所以下面「不能误报」的用例比「要能发现」的更重要。
//
// 实测背景：pipixia 配了 33 个模型、上游只列出 4 个；而 senseaudio 的上游清单里
// 大小写与配置写法不一致（同一批模型在不同站点写法常有出入），
// 按字面比对会把它们全报成缺失 —— 这正是误报的主要来源。

// 基本场景：上游有的不该被报成缺失。
func TestDiffNoFalsePositive(t *testing.T) {
	configured := []string{"GLM-5", "deepseek-v4-flash", "kimi-k2.6"}
	upstream := []string{"GLM-5", "deepseek-v4-flash", "kimi-k2.6", "extra-model"}

	got := diffUpstreamModels(configured, upstream)
	if len(got.MissingUpstream) != 0 {
		t.Fatalf("上游明明都有，不该报缺失，实得 %v", got.MissingUpstream)
	}
	if got.ListedCount != 3 {
		t.Fatalf("有效数应为 3，实得 %d", got.ListedCount)
	}
	if !reflect.DeepEqual(got.NotConfigured, []string{"extra-model"}) {
		t.Fatalf("上游多出来的应报 not_configured，实得 %v", got.NotConfigured)
	}
}

// **大小写差异不能算缺失** —— 这是误报的主要来源。
func TestDiffIgnoresCase(t *testing.T) {
	configured := []string{"GLM-5", "DeepSeek-V4-Flash"}
	upstream := []string{"glm-5", "deepseek-v4-flash"}

	got := diffUpstreamModels(configured, upstream)
	if len(got.MissingUpstream) != 0 {
		t.Fatalf("只有大小写不同，不该报缺失（会让用户删掉有效配置），实得 %v", got.MissingUpstream)
	}
	if got.ListedCount != 2 {
		t.Fatalf("有效数应为 2，实得 %d", got.ListedCount)
	}
}

// 首尾空白差异也不能算缺失。
func TestDiffIgnoresWhitespace(t *testing.T) {
	configured := []string{" glm-5 "}
	upstream := []string{"glm-5"}

	got := diffUpstreamModels(configured, upstream)
	if len(got.MissingUpstream) != 0 {
		t.Fatalf("只有首尾空白不同，不该报缺失，实得 %v", got.MissingUpstream)
	}
}

// 核心场景：上游没有的要被发现（pipixia 正是这种）。
func TestDiffFindsMissingUpstream(t *testing.T) {
	configured := []string{"GLM-5", "claude-opus-5", "kimi-k3", "deepseek-v4-flash"}
	upstream := []string{"deepseek-v4-flash"}

	got := diffUpstreamModels(configured, upstream)
	want := []string{"GLM-5", "claude-opus-5", "kimi-k3"}
	if !reflect.DeepEqual(got.MissingUpstream, want) {
		t.Fatalf("缺失清单应为 %v，实得 %v", want, got.MissingUpstream)
	}
	if got.ListedCount != 1 {
		t.Fatalf("33 个配置里只有 1 个上游有 → 有效数应为 1，实得 %d", got.ListedCount)
	}
}

// 上游一个都没有时：全部配置都是无效的，且有效数不能为负。
func TestDiffAllMissing(t *testing.T) {
	configured := []string{"a", "b"}
	upstream := []string{}

	got := diffUpstreamModels(configured, upstream)
	if len(got.MissingUpstream) != 2 {
		t.Fatalf("上游为空时两个配置都该报缺失，实得 %v", got.MissingUpstream)
	}
	if got.ListedCount != 0 {
		t.Fatalf("有效数应为 0（不能为负），实得 %d", got.ListedCount)
	}
}

// 空白项不该被计入数量、也不该出现在差异清单里。
func TestDiffSkipsBlankEntries(t *testing.T) {
	configured := []string{"glm-5", "", "   "}
	upstream := []string{"glm-5", ""}

	got := diffUpstreamModels(configured, upstream)
	if len(got.MissingUpstream) != 0 {
		t.Fatalf("空白项不该报缺失，实得 %v", got.MissingUpstream)
	}
	if len(got.NotConfigured) != 0 {
		t.Fatalf("空白项不该报 not_configured，实得 %v", got.NotConfigured)
	}
	if got.ListedCount != 1 {
		t.Fatalf("只有 1 个非空配置，有效数应为 1，实得 %d", got.ListedCount)
	}
}

// 两侧完全一致：没有差异，有效数等于配置数。
func TestDiffIdentical(t *testing.T) {
	names := []string{"a", "b", "c"}
	got := diffUpstreamModels(names, names)
	if len(got.MissingUpstream) != 0 || len(got.NotConfigured) != 0 {
		t.Fatalf("完全一致时不该有任何差异，实得 missing=%v notConfigured=%v",
			got.MissingUpstream, got.NotConfigured)
	}
	if got.ListedCount != 3 {
		t.Fatalf("有效数应为 3，实得 %d", got.ListedCount)
	}
}
