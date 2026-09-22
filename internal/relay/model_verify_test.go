package relay

import (
	"testing"
)

// T-verify-001 上游模型一致性校验。
//
// 这个功能的全部价值在于回答一个问题：**上游有没有偷换模型**。
// 按 opus 收费却用 haiku 出货，只看"我们请求了什么"（TargetModel）永远发现不了；
// 只有拿"上游自称用了什么"去比对才看得见。
//
// 判据设计上最要紧的是**不要误报**：上游不回报模型名是普遍现象，
// 把"没回报"当"不匹配"会让这个标记被噪音淹没，最终没人看它。

// 双方都有值且相等 → 不报。
func TestModelMismatchEqualIsFalse(t *testing.T) {
	for _, pair := range [][2]string{
		{"gpt-4o", "gpt-4o"},
		{"claude-sonnet-4", "claude-sonnet-4"},
		{"step-5-preview", "step-5-preview"},
	} {
		if modelMismatch(pair[0], pair[1]) {
			t.Fatalf("%q vs %q 应判定为一致", pair[0], pair[1])
		}
	}
}

// 大小写不同不算不一致。
//
// 反例：站点回 "GPT-4O" 而我们请求 "gpt-4o" 时判为偷换，
// 会产生大量假警报 —— 同一个模型名在不同站点的写法本来就不同。
func TestModelMismatchCaseInsensitive(t *testing.T) {
	for _, pair := range [][2]string{
		{"gpt-4o", "GPT-4O"},
		{"Claude-Sonnet-4", "claude-sonnet-4"},
		{"step-5-PREVIEW", "STEP-5-preview"},
	} {
		if modelMismatch(pair[0], pair[1]) {
			t.Fatalf("%q vs %q 只差大小写，不该判为不一致", pair[0], pair[1])
		}
	}
}

// 首尾空白不算不一致（上游回包常带空格）。
func TestModelMismatchTrimsWhitespace(t *testing.T) {
	if modelMismatch("  gpt-4o  ", "gpt-4o") {
		t.Fatalf("首尾空白不该判为不一致")
	}
	if modelMismatch("gpt-4o", "\tgpt-4o\n") {
		t.Fatalf("首尾空白不该判为不一致")
	}
}

// **上游没回报模型名时不判定**。这是防误报的核心。
//
// 反例：把空串当"不匹配"，则所有不回报 model 字段的站点（现实中很常见）
// 全部会被标红，这个标记立刻失去意义。
func TestModelMismatchEmptyReportedIsNotMismatch(t *testing.T) {
	for _, reported := range []string{"", "   ", "\n"} {
		if modelMismatch("gpt-4o", reported) {
			t.Fatalf("上游没回报模型名（%q）时不该判为不一致", reported)
		}
	}
}

// 我方没请求模型名时也不判定（理论上不该发生，但防御性一致）。
func TestModelMismatchEmptyRequestedIsNotMismatch(t *testing.T) {
	if modelMismatch("", "gpt-4o") {
		t.Fatalf("我方没记录请求模型时不该判为不一致")
	}
	if modelMismatch("", "") {
		t.Fatalf("双方都空时不该判为不一致")
	}
}

// 真正不同的模型必须被判定出来 —— 这是功能的正面判据。
func TestModelMismatchDetectsSwap(t *testing.T) {
	cases := [][2]string{
		{"claude-opus-4", "claude-haiku-3"}, // 典型偷换：贵价请求低价出货
		{"gpt-4o", "gpt-4o-mini"},
		{"step-5-preview", "step-3.5-flash"},
		{"gpt-4o", "gpt-4o-2024-11-20"}, // 带日期后缀：也算不同（用户可据日志判断是否可接受）
	}
	for _, pair := range cases {
		if !modelMismatch(pair[0], pair[1]) {
			t.Fatalf("%q vs %q 应判定为不一致", pair[0], pair[1])
		}
	}
}

// 从 SSE 首事件里取模型名。
func TestReportedModelFromSSE(t *testing.T) {
	cases := map[string]string{
		`{"id":"x","model":"gpt-4o","choices":[]}`:    "gpt-4o",
		`{"model":"  step-5-preview  ","choices":[]}`: "step-5-preview",
		`{"id":"x","choices":[]}`:                     "",
		`not json at all`:                             "",
		`{}`:                                          "",
		`{"model":""}`:                                "",
	}
	for input, want := range cases {
		if got := reportedModelFromSSE([]byte(input)); got != want {
			t.Fatalf("reportedModelFromSSE(%q) = %q, want %q", input, got, want)
		}
	}
}

// 流式 chunk 与 Anthropic 形态都要能取到（Anthropic 的回包里 model 在顶层同名字段）。
func TestReportedModelFromSSEVariants(t *testing.T) {
	// Anthropic message_start 事件的 data 载荷
	anthropic := `{"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4","role":"assistant"}}`
	if got := reportedModelFromSSE([]byte(anthropic)); got != "claude-sonnet-4" {
		t.Fatalf("Anthropic 形态应能从 message.model 取到（或如实返回空）：got=%q", got)
	}
	// OpenAI chunk
	openai := `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[]}`
	if got := reportedModelFromSSE([]byte(openai)); got != "gpt-4o" {
		t.Fatalf("OpenAI chunk 应取到 gpt-4o，实得 %q", got)
	}
}
