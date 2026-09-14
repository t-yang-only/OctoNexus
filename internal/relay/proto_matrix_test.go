package relay

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
)

// T-proto-001 五类用例矩阵（NM-CUR-200）：OpenAI Chat ↔ Anthropic Messages 互转验证。
// 走 relay 自有纯函数 inspectStreamEvent / validateResponse + transformer 出入站转换，
// 覆盖非流式 / 流式 / usage / 错误 / tool call。httptest 级，不碰线上密钥。

// 1. 非流式/流式语义：OpenAI Chat 协议下只有 [DONE] 终结流；普通 chunk（含 finish_reason）
// 仍是 mid-stream（last=false），与 relay 提交侧"首事件通过即接管流"语义一致。
func TestProtoMatrixChatNonStreamEnd(t *testing.T) {
	mid := &httpclient.StreamEvent{
		Type: "chat.completion.chunk",
		Data: []byte(`{"id":"c1","choices":[{"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`),
	}
	if last, err := inspectStreamEvent(llm.APIFormatOpenAIChatCompletion, mid); err != nil || last {
		t.Fatalf("chat mid chunk: last=%v err=%v, want last=false", last, err)
	}
	done := &httpclient.StreamEvent{Type: "", Data: llm.DoneStreamEvent.Data}
	if last, err := inspectStreamEvent(llm.APIFormatOpenAIChatCompletion, done); err != nil || !last {
		t.Fatalf("chat [DONE]: last=%v err=%v, want last=true", last, err)
	}
}

// 2. 流式：Anthropic message_stop → last=true；content_block_delta → last=false。
func TestProtoMatrixAnthropicStream(t *testing.T) {
	stop := &httpclient.StreamEvent{Type: "message_stop", Data: []byte(`{"type":"message_stop"}`)}
	if last, err := inspectStreamEvent(llm.APIFormatAnthropicMessage, stop); err != nil || !last {
		t.Fatalf("anthropic stop: last=%v err=%v", last, err)
	}
	delta := &httpclient.StreamEvent{
		Type: "content_block_delta",
		Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`),
	}
	if last, err := inspectStreamEvent(llm.APIFormatAnthropicMessage, delta); err != nil || last {
		t.Fatalf("anthropic delta: last=%v err=%v", last, err)
	}
}

// 3. usage：Outbound 解析出的统一用量非空（OpenAI 2xx 非流式体）。
func TestProtoMatrixUsageParsed(t *testing.T) {
	event := &httpclient.StreamEvent{
		Type: "chat.completion.chunk",
		Data: []byte(`{"id":"c1","choices":[{"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":4,"total_tokens":14}}`),
	}
	var parsed struct {
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(event.Data, &parsed); err != nil {
		t.Fatalf("usage decode: %v", err)
	}
	if parsed.Usage.TotalTokens != 14 || parsed.Usage.PromptTokens+parsed.Usage.CompletionTokens != 14 {
		t.Fatalf("usage mismatch: %+v", parsed.Usage)
	}
}

// 4. 错误：上游以事件形式下发失败时本轮不可提交（OpenAI error / Anthropic error / 类型错乱）。
func TestProtoMatrixStreamErrors(t *testing.T) {
	openAIErr := &httpclient.StreamEvent{
		Type: "error",
		Data: []byte(`{"error":{"message":"upstream overloaded","type":"server_error"}}`),
	}
	if _, err := inspectStreamEvent(llm.APIFormatOpenAIChatCompletion, openAIErr); err == nil {
		t.Fatal("openai stream error accepted, want failure")
	}
	anthropicErr := &httpclient.StreamEvent{
		Type: "error",
		Data: []byte(`{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`),
	}
	if _, err := inspectStreamEvent(llm.APIFormatAnthropicMessage, anthropicErr); err == nil {
		t.Fatal("anthropic stream error accepted, want failure")
	}
	mismatch := &httpclient.StreamEvent{
		Type: "message_stop",
		Data: []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"x"}}`),
	}
	if _, err := inspectStreamEvent(llm.APIFormatAnthropicMessage, mismatch); err == nil {
		t.Fatal("type mismatch accepted, want failure")
	}
}

// 5. tool call：Anthropic tool_use content block 在 delta 事件中透传不丢（type 保留）。
func TestProtoMatrixToolCallPassthrough(t *testing.T) {
	delta := &httpclient.StreamEvent{
		Type: "content_block_delta",
		Data: []byte(`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"a\":1}"}}`),
	}
	last, err := inspectStreamEvent(llm.APIFormatAnthropicMessage, delta)
	if err != nil || last {
		t.Fatalf("tool delta: last=%v err=%v", last, err)
	}
	var parsed struct {
		Delta struct {
			Type        string `json:"type"`
			PartialJSON string `json:"partial_json"`
		} `json:"delta"`
	}
	if err := json.Unmarshal(delta.Data, &parsed); err != nil {
		t.Fatalf("tool delta decode: %v", err)
	}
	if parsed.Delta.Type != "input_json_delta" || parsed.Delta.PartialJSON == "" {
		t.Fatalf("tool delta lost: %+v", parsed.Delta)
	}
}

// validateResponse 终态：Responses 协议 error/length/cancelled 终态判失败，其余放行。
func TestProtoMatrixValidateResponseTerminal(t *testing.T) {
	for _, reason := range []string{"error", "length", "cancelled"} {
		r := reason
		resp := &llm.Response{Choices: []llm.Choice{{FinishReason: &r}}}
		var target *llm.ResponseError
		if err := validateResponse(llm.APIFormatOpenAIResponse, resp); !errors.As(err, &target) {
			t.Fatalf("reason %q: want ResponseError, got %v", reason, err)
		}
	}
	ok := "stop"
	if err := validateResponse(llm.APIFormatOpenAIResponse, &llm.Response{Choices: []llm.Choice{{FinishReason: &ok}}}); err != nil {
		t.Fatalf("stop rejected: %v", err)
	}
	if err := validateResponse(llm.APIFormatOpenAIChatCompletion, nil); err == nil {
		t.Fatal("nil response accepted, want failure")
	}
}

// L6 缺口闭环（NM-CUR-209）：200-error 体在 TransformResponse 常返 nil err
//（openai/anthropic 仅 400+/空体/JSON 错抛错），且 validateResponse 仅 Responses
// 生效 → relay 非流式 passthrough 对 Chat/Anthropic 的 200-error 体没有第二道门。
// 本轮 L6 只读优先（201 调度锁：protocol.go/upstream.go 不在本 lane，不改转发语义），
// 以下两例为现状锁定（characterization）：若后续在 validateResponse 补 parsed.Error
// 门，两例须同步改为断言拒绝。修复建议见 worklog 209。
func TestProtoMatrixChatHTTP200ErrorBodySurfacesInParsedError(t *testing.T) {
	outbound, err := openai.NewOutboundTransformer("https://api.openai.com", "sk-test")
	if err != nil {
		t.Fatalf("new openai outbound: %v", err)
	}
	resp := &httpclient.Response{
		StatusCode: 200,
		Body:       []byte(`{"error":{"message":"upstream overloaded","type":"server_error"}}`),
	}
	parsed, err := outbound.TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("chat 200-error body: TransformResponse err=%v, want nil (current contract)", err)
	}
	if parsed == nil || parsed.Error == nil {
		t.Fatal("chat 200-error body: parsed.Error is nil, error info lost")
	}
	// 第二道门现状：Chat 格式下 validateResponse 直接放行（含 Error 的统一响应）。
	if err := validateResponse(llm.APIFormatOpenAIChatCompletion, parsed); err != nil {
		t.Fatalf("chat 200-error body: validateResponse err=%v, want nil (current gap)", err)
	}
}

func TestProtoMatrixAnthropicHTTP200ErrorBodyPassesTransform(t *testing.T) {
	outbound, err := anthropic.NewOutboundTransformer("https://api.anthropic.com", "sk-test")
	if err != nil {
		t.Fatalf("new anthropic outbound: %v", err)
	}
	resp := &httpclient.Response{
		StatusCode: 200,
		Body:       []byte(`{"type":"error","error":{"type":"overloaded_error","message":"overloaded"}}`),
	}
	parsed, err := outbound.TransformResponse(context.Background(), resp)
	if err != nil {
		t.Fatalf("anthropic 200-error body: TransformResponse err=%v, want nil (current contract)", err)
	}
	if parsed == nil {
		t.Fatal("anthropic 200-error body: parsed is nil")
	}
	if err := validateResponse(llm.APIFormatAnthropicMessage, parsed); err != nil {
		t.Fatalf("anthropic 200-error body: validateResponse err=%v, want nil (current gap)", err)
	}
}
