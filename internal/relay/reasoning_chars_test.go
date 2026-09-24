package relay

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm"
)

// T-insight-005 思考字数的判据。
//
// 这个字段存在的理由就是「上游不报思考 token」—— 生产实测 122 行里
// reasoning_tokens > 0 的一条都没有，所以第一条用例必须钉住：
// **token 缺失时字符数仍然算得出来**，否则这个功能等于没做。

func TestReasoningCharsOpenAIMessage(t *testing.T) {
	body := `{"choices":[{"message":{"reasoning_content":"让我想想这个问题","content":"答案"}}]}`
	if got := reasoningCharsFromBody(body); got != 8 {
		t.Errorf("ReasoningChars = %d, want 8（「让我想想这个问题」8 个 rune）", got)
	}
}

func TestReasoningCharsCountsRunesNotBytes(t *testing.T) {
	// 中文按 rune 计：若误用 len() 会得到 3 倍的数字，这个用例就是为它准备的。
	body := `{"choices":[{"message":{"reasoning_content":"思考"}}]}`
	if got := reasoningCharsFromBody(body); got != 2 {
		t.Errorf("ReasoningChars = %d, want 2（中文 2 个字符；用字节算会得到 6）", got)
	}

	// 英文与 emoji 同样各算 1。
	mixed := `{"choices":[{"message":{"reasoning_content":"ab🎯"}}]}`
	if got := reasoningCharsFromBody(mixed); got != 3 {
		t.Errorf("ReasoningChars = %d, want 3（a、b、🎯 各 1）", got)
	}
}

func TestReasoningCharsAnthropicThinkingBlock(t *testing.T) {
	body := `{"content":[{"type":"text","text":"正文不算"},{"type":"thinking","thinking":"先看条件再推导"}]}`
	if got := reasoningCharsFromBody(body); got != 7 {
		t.Errorf("ReasoningChars = %d, want 7（只看 thinking 块，正文「正文不算」不参与）", got)
	}
}

func TestReasoningCharsRedactedThinkingIgnored(t *testing.T) {
	// redacted_thinking 里是可读文本的密文（base64），把它算进去会得到
	// 一个看起来很像真的、但毫无意义的大数字。
	body := `{"content":[{"type":"redacted_thinking","data":"RW5jcnlwdGVkIHJlYXNvbmluZyBjb250ZW50"}]}`
	if got := reasoningCharsFromBody(body); got != 0 {
		t.Errorf("ReasoningChars = %d, want 0（被加密的思考没有可读文本）", got)
	}
}

func TestReasoningCharsResponsesOutput(t *testing.T) {
	body := `{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"分三步"}]},{"type":"message","content":[{"type":"output_text","text":"不算"}]}]}`
	if got := reasoningCharsFromBody(body); got != 3 {
		t.Errorf("ReasoningChars = %d, want 3（只算 type=reasoning 的 summary，message 的正文不参与）", got)
	}
}

func TestReasoningCharsMultipleChoicesSummed(t *testing.T) {
	body := `{"choices":[{"message":{"reasoning_content":"abc"}},{"message":{"reasoning_content":"de"}}]}`
	if got := reasoningCharsFromBody(body); got != 5 {
		t.Errorf("ReasoningChars = %d, want 5（多个 choice 累加）", got)
	}
}

func TestReasoningCharsInvalidBodyIsZero(t *testing.T) {
	// 异常路径不能 panic，也不能凭空造值。
	for _, body := range []string{"", "not json", "{", `{"choices":null}`, `{"content":[]}`} {
		if got := reasoningCharsFromBody(body); got != 0 {
			t.Errorf("reasoningCharsFromBody(%q) = %d, want 0", body, got)
		}
	}
}

// TestReasoningCharsPersistedWhenTokensAbsent 是本功能的核心判据：
// 上游**不报** reasoning_tokens 时，reasoning_chars 仍必须落库有值。
//
// 变异方式：把 finishLocked 里的 reasoningChars 写死成 0（或删掉那行赋值）
// 会让本用例变红 —— 而其它用例都碰不到落库路径。
func TestReasoningCharsPersistedWhenTokensAbsent(t *testing.T) {
	openInsightTestDB(t)

	// 响应体里有思考文本，usage 里**没有** CompletionTokensDetails。
	body := `{"choices":[{"message":{"reasoning_content":"需要仔细算一下这个题"}}]}`
	state := newRequestState(nil, "Auto-Model", 1, 2, "{}", "", 0)
	state.markSucceeded(body, &llm.Usage{CompletionTokens: 12})

	row := loadInsightRow(t, state.ID)
	if row.ReasoningTokens != 0 {
		t.Fatalf("ReasoningTokens = %d, want 0（本用例的上游没有回报 token）", row.ReasoningTokens)
	}
	if row.ReasoningChars != 10 {
		t.Errorf("ReasoningChars = %d, want 10（上游不报 token 时，字符数是唯一的度量）", row.ReasoningChars)
	}
}

// TestReasoningCharsAndTokensCoexist 钉住与参考项目不同的那个选择：
// **两个字段各记各的，不互斥**。参考项目在有官方 token 时不记字符数，
// 照搬会让本字段的分母随上游是否升级而变化。
func TestReasoningCharsAndTokensCoexist(t *testing.T) {
	openInsightTestDB(t)

	body := `{"choices":[{"message":{"reasoning_content":"abcdef"}}]}`
	state := newRequestState(nil, "Auto-Model", 1, 2, "{}", "", 0)
	state.markSucceeded(body, &llm.Usage{
		CompletionTokens:        20,
		CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 46},
	})

	row := loadInsightRow(t, state.ID)
	if row.ReasoningTokens != 46 {
		t.Errorf("ReasoningTokens = %d, want 46", row.ReasoningTokens)
	}
	if row.ReasoningChars != 6 {
		t.Errorf("ReasoningChars = %d, want 6（官方 token 存在时字符数不能被视为冗余而丢弃）", row.ReasoningChars)
	}
}

// TestReasoningCharsOnFailedRequest 钉住收口点的选择：
// 失败请求若上游已经吐过思考文本，也必须记下来 —— 这正是"看一条报错时
// 想知道它思考到哪一步"的场景。若把计算放进 markSucceeded，本用例会变红。
func TestReasoningCharsOnFailedRequest(t *testing.T) {
	openInsightTestDB(t)

	body := `{"choices":[{"message":{"reasoning_content":"先试着算"}}]}`
	state := newRequestState(nil, "Auto-Model", 1, 2, "{}", "", 0)
	state.markFailed(errTestCanceled{}, body, nil, stopReasonCommitGuard, stopSourceClient)

	row := loadInsightRow(t, state.ID)
	if row.ReasoningChars != 4 {
		t.Errorf("ReasoningChars = %d, want 4（失败路径也要算：思考到哪一步是排障信息）", row.ReasoningChars)
	}
}

func TestReasoningCharsLimitGuardsHugeBody(t *testing.T) {
	// 超过上限的正文只在前 8 MiB 里找思考文本 —— 不能因为落库路径上
	// 要遍历一个几十 MB 的响应而拖慢请求收尾。这里构造一个超大正文，
	// 断言既不 panic 也不会穷尽遍历（思考文本放在超限之后，取不到即为 0）。
	filler := strings.Repeat("x", reasoningCharsLimit+1024)
	body := `{"choices":[{"message":{"padding":"` + filler + `"}}],"tail":{"reasoning_content":"不该被看到"}}`
	if got := reasoningCharsFromBody(body); got != 0 {
		t.Errorf("ReasoningChars = %d, want 0（超限部分不再解析，避免拖慢落库）", got)
	}
}

func TestReasoningCharsLimitStillCountsWithinLimit(t *testing.T) {
	// 反向对照：上限**之内**的思考文本必须照常算出来，
	// 否则「有上限」就会退化成「大响应一律记 0」。
	pad := strings.Repeat("y", 1024)
	body := `{"choices":[{"message":{"reasoning_content":"想想","padding":"` + pad + `"}}]}`
	if got := reasoningCharsFromBody(body); got != 2 {
		t.Errorf("ReasoningChars = %d, want 2（上限内的思考必须算出来）", got)
	}
}

// TestReasoningCharsNumericFieldIgnored 钉住类型守卫：
// JSON 里出现非字符串的 reasoning_content 时不能 panic（部分站点会返回 null）。
func TestReasoningCharsNumericFieldIgnored(t *testing.T) {
	for _, body := range []string{
		`{"choices":[{"message":{"reasoning_content":null}}]}`,
		`{"choices":[{"message":{"reasoning_content":123}}]}`,
		`{"content":[{"type":"thinking","thinking":false}]}`,
		`{"output":[{"type":"reasoning","summary":"not-an-array"}]}`,
	} {
		if got := reasoningCharsFromBody(body); got != 0 {
			t.Errorf("reasoningCharsFromBody(%s) = %d, want 0（非字符串要安全跳过）", body, got)
		}
	}
}

// TestReasoningCharsIgnoresNonReasoningFields 是负向对照：
// 普通输出文本**不得**被当成思考字数。否则所有非思考请求都会显示
// 「思考 N 字」，那比没有这个字段更糟。
func TestReasoningCharsIgnoresNonReasoningFields(t *testing.T) {
	cases := []string{
		`{"choices":[{"message":{"content":"这是一段普通回答，很长很长很长"}}]}`,
		`{"content":[{"type":"text","text":"普通正文"}]}`,
		`{"output":[{"type":"message","content":[{"type":"output_text","text":"普通输出"}]}]}`,
	}
	for _, body := range cases {
		if got := reasoningCharsFromBody(body); got != 0 {
			t.Errorf("reasoningCharsFromBody(%s) = %d, want 0（普通正文不是思考）", body, got)
		}
	}
}

// 确保测试用的 JSON 断言本身有效（防止上面手写正文写坏导致假通过）。
func TestReasoningCharsBodiesAreValidJSON(t *testing.T) {
	bodies := []string{
		`{"choices":[{"message":{"reasoning_content":"让我想想这个问题","content":"答案"}}]}`,
		`{"content":[{"type":"text","text":"正文不算"},{"type":"thinking","thinking":"先看条件再推导"}]}`,
		`{"output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"分三步"}]},{"type":"message","content":[{"type":"output_text","text":"不算"}]}]}`,
	}
	for _, body := range bodies {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(body), &parsed); err != nil {
			t.Fatalf("测试正文本身不是合法 JSON: %v (%s)", err, body)
		}
	}
}
