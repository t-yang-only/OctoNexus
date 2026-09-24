package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/looplj/axonhub/llm"
)

// T-trace-004 客户端入站协议落库的判据。
//
// ## 为什么必须落库，而不是留在内存里
//
// RequestState.Protocol 一直存在，实时看板也一直在显示它（日志弹窗把「入站协议 → 上游协议」
// 两个标签并排摆着）。但落库的 RelayLog 里**没有**这个字段，于是历史日志页那一栏恒为空，
// 前端当时只能拿 target_protocol 顶上 —— 等于用"上游吃的是什么协议"冒充"客户端发的是什么协议"。
//
// 而它要回答的是运营上最常被追问的一句话：这次请求中间做过跨协议转换吗？
//
// 判据一律查 relay_logs 的实际列，不断言内存字段：内存字段本来就有，证明不了落库这件事。

// 客户端协议与上游协议必须各自独立落库 —— 相同表示原样转发，不同表示转换过。
func TestRequestProtocolPersistedSeparatelyFromTarget(t *testing.T) {
	openInsightTestDB(t)

	state := &RequestState{
		ID:             9201,
		Round:          1,
		Protocol:       model.ProtocolAnthropicMessage,
		TargetProtocol: model.ProtocolOpenAIChatCompletion,
	}
	state.markSucceeded("body", &llm.Usage{CompletionTokens: 3})

	row := loadInsightRow(t, 9201)
	if row.RequestProtocol != int(model.ProtocolAnthropicMessage) {
		t.Errorf("RequestProtocol = %d, want %d（客户端发的是 Anthropic Messages）",
			row.RequestProtocol, int(model.ProtocolAnthropicMessage))
	}
	if row.TargetProtocol != int(model.ProtocolOpenAIChatCompletion) {
		t.Errorf("TargetProtocol = %d, want %d（上游吃的是 OpenAI Chat）",
			row.TargetProtocol, int(model.ProtocolOpenAIChatCompletion))
	}
	// 核心断言：两个字段必须能同时不同，否则「转换与否」根本无从判断。
	// 把 RequestProtocol 写成 TargetProtocol 的实现会在这里露出来。
	if row.RequestProtocol == row.TargetProtocol {
		t.Errorf("两个协议位不该被写成同一个值（都 = %d），否则转换与否无从判断", row.RequestProtocol)
	}
}

// 原样转发（两端同协议）时两个字段相等，且不能把它误记成"转换过"。
func TestRequestProtocolMatchesWhenNoConversion(t *testing.T) {
	openInsightTestDB(t)

	state := &RequestState{
		ID:             9202,
		Round:          1,
		Protocol:       model.ProtocolOpenAIResponse,
		TargetProtocol: model.ProtocolOpenAIResponse,
	}
	state.markSucceeded("body", &llm.Usage{CompletionTokens: 1})

	row := loadInsightRow(t, 9202)
	if row.RequestProtocol != int(model.ProtocolOpenAIResponse) {
		t.Errorf("RequestProtocol = %d, want %d", row.RequestProtocol, int(model.ProtocolOpenAIResponse))
	}
	if row.RequestProtocol != row.TargetProtocol {
		t.Errorf("原样转发时两个协议位应相等，实得 %d / %d（不该被判成转换过）",
			row.RequestProtocol, row.TargetProtocol)
	}
}

// 失败路径同样要落库：失败请求恰恰是"协议对不上"最常暴露的地方
// （拿 chat 端点去调 TTS 模型就是这一类），排查时不该只有成功行带协议信息。
func TestRequestProtocolPersistedOnFailurePath(t *testing.T) {
	openInsightTestDB(t)

	state := &RequestState{
		ID:             9203,
		Round:          2,
		Protocol:       model.ProtocolOpenAIChatCompletion,
		TargetProtocol: 0,
	}
	state.markFailed(errTestCanceled{}, "", &llm.Usage{CompletionTokens: 2}, stopReasonBudget, stopSourceConfig)

	row := loadInsightRow(t, 9203)
	if row.RequestProtocol != int(model.ProtocolOpenAIChatCompletion) {
		t.Errorf("失败行 RequestProtocol = %d, want %d（失败行与成功行看到的信息量不该有差别）",
			row.RequestProtocol, int(model.ProtocolOpenAIChatCompletion))
	}
}

// 没走标准协议的请求（systemone 自定义形态）不猜协议：留 0 表示"没有协议信息"。
func TestRequestProtocolZeroForCustomShape(t *testing.T) {
	openInsightTestDB(t)

	state := &RequestState{ID: 9204, Round: 1}
	state.markSucceeded("body", &llm.Usage{CompletionTokens: 1})

	row := loadInsightRow(t, 9204)
	if row.RequestProtocol != 0 {
		t.Errorf("自定义形态的 RequestProtocol = %d, want 0（不知道就不填，不许拿上游协议顶）",
			row.RequestProtocol)
	}
}
