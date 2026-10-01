package relay

// 思考等级的生效值（上游 issue #409）。
//
// # 缺陷
//
// 渠道参数覆盖把思考等级改成 low、实测已生效，但日志页徽章仍显示客户端请求里的 high ——
// 界面上没有任何地方能确认覆盖是否生效，用户第一反应都是"覆盖没生效"。
//
// 两件事共同造成它：
//  1. 入站只解析 `reasoning_effort`（OpenAI 口径），Anthropic 的
//     `thinking` / `output_config.effort` 两种形态进不了日志；
//  2. 存进请求状态的是**入站**值，而覆盖发生在出站阶段，之后没有回填。
//
// # 判据取向
//
// 必须覆盖三种输入形态 + 一个"解析不到"的兜底：
// 漏掉任一形态，那一类客户端看到的徽章就是空的；
// 漏掉兜底，出站解析失败时会把已有的入站值抹成空。

import (
	"testing"
)

// OpenAI 形态：reasoning_effort。
func TestParseThinkingEffortOpenAI(t *testing.T) {
	cases := map[string]string{
		`{"reasoning_effort":"high"}`:               "high",
		`{"reasoning_effort":"xhigh"}`:              "xhigh",
		`{"reasoning_effort":"none"}`:               "none",
		`{"reasoning_effort":"High"}`:               "high", // 大小写归一
		`{"reasoning_effort":" high "}`:             "high", // 空白归一
		`{"model":"m","reasoning_effort":"medium"}`: "medium",
		`{"model":"m"}`:                             "", // 没带档位
		`{"reasoning_effort":""}`:                   "", // 空串
	}
	for body, want := range cases {
		if got := parseThinkingEffort([]byte(body)); got != want {
			t.Errorf("parseThinkingEffort(%s) = %q, want %q", body, got, want)
		}
	}
}

// Anthropic 形态 1：output_config.effort（#409 报告用的正是这个）。
func TestParseThinkingEffortAnthropicOutputConfig(t *testing.T) {
	cases := map[string]string{
		`{"output_config":{"effort":"low"}}`:  "low",
		`{"output_config":{"effort":"LOW"}}`:  "low",
		`{"output_config":{"effort":"high"}}`: "high",
		`{"output_config":{}}`:                "",
		`{"output_config":{"effort":""}}`:     "",
	}
	for body, want := range cases {
		if got := parseThinkingEffort([]byte(body)); got != want {
			t.Errorf("parseThinkingEffort(%s) = %q, want %q", body, got, want)
		}
	}
}

// Anthropic 形态 2：thinking 开关。
// thinking 是布尔语义，只有显式禁用/启用才有确定含义，且**不猜档位** ——
// budget_tokens 与 low/high 之间没有可靠映射，猜一个比显示"enabled"更糟。
func TestParseThinkingEffortAnthropicThinking(t *testing.T) {
	if got := parseThinkingEffort([]byte(`{"thinking":{"type":"disabled"}}`)); got != "none" {
		t.Errorf("thinking disabled = %q, want none", got)
	}
	if got := parseThinkingEffort([]byte(`{"thinking":{"type":"enabled","budget_tokens":4096}}`)); got != "enabled" {
		t.Errorf("thinking enabled = %q, want enabled（不猜档位）", got)
	}
	// 空 type 不当成有档位。
	if got := parseThinkingEffort([]byte(`{"thinking":{}}`)); got != "" {
		t.Errorf("thinking 空对象 = %q, want 空串", got)
	}
}

// 优先级：两种形态同时存在时 reasoning_effort 优先（OpenAI 口径更具体）。
func TestParseThinkingEffortPrefersReasoningEffort(t *testing.T) {
	body := `{"reasoning_effort":"high","output_config":{"effort":"low"}}`
	if got := parseThinkingEffort([]byte(body)); got != "high" {
		t.Errorf("两种形态并存时应取 reasoning_effort，实得 %q", got)
	}
}

// 非 JSON 正文 / 空正文：返回空串而不是报错（透传的其它协议）。
func TestParseThinkingEffortNonJSON(t *testing.T) {
	for _, body := range []string{"", "not json at all", "[]", "null", `{"a":1`} {
		if got := parseThinkingEffort([]byte(body)); got != "" {
			t.Errorf("parseThinkingEffort(%q) = %q, want 空串", body, got)
		}
	}
}

// registerTestState 把状态登记进全局表。
//
// noteEffectiveEffort 与 retargetRound 同范式：只有已登记的状态才会被更新
// （未登记的直接返回，避免给一个已经结束的请求回填）。所以这两个用例
// 必须真的登记，否则测的是"未登记时静默返回"而不是"回填是否生效"。
func registerTestState(t *testing.T, state *RequestState) {
	t.Helper()
	mu.Lock()
	if requests == nil {
		requests = make(map[uint64]*RequestState)
	}
	requests[state.ID] = state
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		delete(requests, state.ID)
		mu.Unlock()
	})
}

// noteEffectiveEffort 只在解析到非空档位时覆盖 ——
// 出站解析不到（非 JSON、客户端没带）时必须保留入站值，
// 否则会把"客户端确实带了 high"这条已有信息抹成空。
func TestNoteEffectiveEffortKeepsInboundWhenEmpty(t *testing.T) {
	state := &RequestState{ID: 9401, ReasoningEffort: "high"}
	registerTestState(t, state)
	// 出站没解析出档位：保留入站的 high。
	state.noteEffectiveEffort("")
	if state.ReasoningEffort != "high" {
		t.Errorf("出站未解析到档位时应保留入站值，实得 %q", state.ReasoningEffort)
	}
	// 出站解析到 low：覆盖成 low（这正是 #409 要的效果）。
	state.noteEffectiveEffort("low")
	if state.ReasoningEffort != "low" {
		t.Errorf("出站生效值应覆盖入站值，实得 %q", state.ReasoningEffort)
	}
	// 归一化：大小写/空白不该产生两个显示值。
	state.noteEffectiveEffort("  HIGH ")
	if state.ReasoningEffort != "high" {
		t.Errorf("回填值应归一化，实得 %q", state.ReasoningEffort)
	}
}

// 覆盖生效值必须来自**出站**正文，不是入站正文 ——
// 这条直接对应 #409 的复现步骤：客户端带 high，渠道覆盖成 low。
func TestEffectiveEffortComesFromOutboundBody(t *testing.T) {
	inbound := []byte(`{"output_config":{"effort":"high"},"model":"m"}`)
	outbound := []byte(`{"output_config":{"effort":"low"},"model":"upstream-m"}`)

	state := &RequestState{ID: 9402}
	registerTestState(t, state)
	// 建状态时记入站值（旧行为）。
	state.ReasoningEffort = parseThinkingEffort(inbound)
	if state.ReasoningEffort != "high" {
		t.Fatalf("入站解析应为 high，实得 %q", state.ReasoningEffort)
	}
	// 出站定型后回填生效值（新行为）。
	state.noteEffectiveEffort(parseThinkingEffort(outbound))
	if state.ReasoningEffort != "low" {
		t.Errorf("日志应显示生效值 low（渠道已覆盖），实得 %q", state.ReasoningEffort)
	}
}
