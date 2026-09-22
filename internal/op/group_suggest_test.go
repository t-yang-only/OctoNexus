package op

import (
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// T-usability-002 分组名拼错时给候选。
//
// 这个功能的价值全在**「给得准」**上：
//   - 给不出候选 → 用户还是要自己翻面板（等于没做）
//   - 给错了候选 → 用户被引向一个同样不存在的分组，比不给更糟
//
// 所以判据的重点不是「有没有返回」，而是「返回的是不是那个对的名字」。
// 下面用真实的分组名形态（生产库里就有这些）做输入。

func seedSuggestGroups(t *testing.T, names ...string) {
	t.Helper()
	conn := openAutoGroupTestDB(t)
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	for _, name := range names {
		if err := conn.Create(&model.Group{Name: name}).Error; err != nil {
			t.Fatalf("create group %q: %v", name, err)
		}
	}
}

// 大小写写错 → 必须命中，且排在最前。
//
// 这是最容易发生也最容易修的一类：用户把 claude-sonnet-4 写成 Claude-Sonnet-4，
// 系统却只回一句 model not found。
func TestGroupSuggestMatchesCaseDifference(t *testing.T) {
	seedSuggestGroups(t, "TypeSafe/jev-latest", "53HK/Auto-Model", "High-flash")
	got := GroupSuggestSimilar("high-FLASH", 3)
	if len(got) == 0 {
		t.Fatalf("大小写差异应给出候选，实得空")
	}
	if got[0] != "High-flash" {
		t.Fatalf("首选候选应为 High-flash，实得 %v", got)
	}
}

// 少打/多打一段（包含关系）→ 命中。
func TestGroupSuggestMatchesPartial(t *testing.T) {
	seedSuggestGroups(t, "StepFun/step-5-preview", "StepFun/step-3.7-flash")
	got := GroupSuggestSimilar("step-5-preview", 3)
	if len(got) == 0 {
		t.Fatalf("少打前缀应给出候选，实得空")
	}
	found := false
	for _, g := range got {
		if g == "StepFun/step-5-preview" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应命中 StepFun/step-5-preview，实得 %v", got)
	}
}

// 单处拼错（编辑距离 1-2）→ 命中。
//
// 这是本功能的核心场景：claude-sonet-4 少了一个 n。
func TestGroupSuggestMatchesTypo(t *testing.T) {
	seedSuggestGroups(t, "53HK/claude-sonnet-4", "53HK/claude-opus-4", "53HK/gpt-5.5")
	got := GroupSuggestSimilar("53HK/claude-sonet-4", 3)
	if len(got) == 0 {
		t.Fatalf("单字符拼错应给出候选，实得空")
	}
	if got[0] != "53HK/claude-sonnet-4" {
		t.Fatalf("首选候选应为 53HK/claude-sonnet-4，实得 %v", got)
	}
}

// **完全无关时不给候选** —— 这条防的是「噪音比没有更糟」。
//
// 反例：把编辑距离放宽到 3 或改用子串匹配后，"gpt-4o" 会命中 "gpt-4.1"、
// 甚至命中任何含 "gpt" 的名字，用户照着改反而改到一个错的。
func TestGroupSuggestSilentWhenNothingSimilar(t *testing.T) {
	seedSuggestGroups(t, "53HK/Auto-Model", "StepFun/step-5-preview", "TypeSafe/jev-latest")
	got := GroupSuggestSimilar("完全不相干的名字", 3)
	if len(got) != 0 {
		t.Fatalf("无关名字不该给候选（会误导），实得 %v", got)
	}
}

// 空名字不给候选（调用方应已拦，这里是防御）。
func TestGroupSuggestEmptyName(t *testing.T) {
	seedSuggestGroups(t, "a/b")
	if got := GroupSuggestSimilar("", 3); len(got) != 0 {
		t.Fatalf("空名字不该给候选，实得 %v", got)
	}
	if got := GroupSuggestSimilar("   ", 3); len(got) != 0 {
		t.Fatalf("纯空白不该给候选，实得 %v", got)
	}
}

// limit 生效：给太多等于没给。
func TestGroupSuggestRespectsLimit(t *testing.T) {
	seedSuggestGroups(t, "m/aaa", "m/aab", "m/aac", "m/aad", "m/aae")
	got := GroupSuggestSimilar("m/aaa", 2)
	if len(got) > 2 {
		t.Fatalf("limit=2 时最多给 2 个，实得 %d 个：%v", len(got), got)
	}
	// limit<=0 用默认值，且不超过默认上限。
	got = GroupSuggestSimilar("m/aaa", 0)
	if len(got) > groupSuggestLimit {
		t.Fatalf("默认上限是 %d，实得 %d 个", groupSuggestLimit, len(got))
	}
}

// **编辑距离的边界必须精确压住**：距离 ≤2 命中、距离 3 不命中。
//
// 上一条「无关名字不给候选」用的是长度差很大的例子（"完全不相干的名字"），
// 任何合理阈值都能挡住它 —— 变异检查当场证明了这一点（把阈值从 2 放宽到 10，
// 那条用例照样通过）。真正能压住阈值的是**贴边界的例子**，也就是下面这组：
// 同样是长度相同的名字，只改 1/2 个字符要命中，改 3 个字符要静默。
//
// 放宽阈值的后果是真实的：用户把分组名打错 3 个字符时，
// 系统会给一个同样不对的候选，他照着改反而改到错的地方。
func TestGroupSuggestBoundaryOfEditDistance(t *testing.T) {
	seedSuggestGroups(t, "m/abcdefgh")

	// 距离 1（替换中间一个字符）→ 必须命中
	if got := GroupSuggestSimilar("m/abcdXfgh", 3); len(got) == 0 || got[0] != "m/abcdefgh" {
		t.Fatalf("距离 1 应命中 m/abcdefgh，实得 %v", got)
	}
	// 距离 1（末尾多一个字符）→ 必须命中
	if got := GroupSuggestSimilar("m/abcdefghX", 3); len(got) == 0 || got[0] != "m/abcdefgh" {
		t.Fatalf("末尾多一字符应命中 m/abcdefgh，实得 %v", got)
	}
	// 距离 2（替换两个字符）→ 必须命中
	if got := GroupSuggestSimilar("m/abcXXfgh", 3); len(got) == 0 || got[0] != "m/abcdefgh" {
		t.Fatalf("距离 2 应命中 m/abcdefgh，实得 %v", got)
	}
	// 距离 3（替换三个字符）→ **必须静默**
	if got := GroupSuggestSimilar("m/abXXXfgh", 3); len(got) != 0 {
		t.Fatalf("距离 3 不该给候选（阈值是 %d），实得 %v", groupSuggestMaxDistance, got)
	}
}

// editDistanceWithin 的提前放弃不能影响结果正确性。
func TestEditDistanceWithin(t *testing.T) {
	cases := []struct {
		a, b string
		max  int
		want int
	}{
		{"abc", "abc", 2, 0},
		{"abc", "abd", 2, 1},
		{"abc", "axc", 2, 1},
		{"abc", "abcd", 2, 1},
		{"abcd", "abc", 2, 1},
		{"abc", "axy", 2, 2},
		{"abc", "xyz", 2, -1}, // 距离 3 > 2，应提前放弃
		{"", "ab", 2, 2},
		{"ab", "", 2, 2},
		{"abcdefgh", "x", 2, -1}, // 长度差就超了
		{"claude-sonet-4", "claude-sonnet-4", 2, 1},
	}
	for _, c := range cases {
		if got := editDistanceWithin(c.a, c.b, c.max); got != c.want {
			t.Fatalf("editDistanceWithin(%q, %q, %d) = %d, want %d", c.a, c.b, c.max, got, c.want)
		}
	}
}
