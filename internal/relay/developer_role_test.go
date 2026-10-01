package relay

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// rolesOf 取出 messages 的 role 序列, 便于断言。
func rolesOf(t *testing.T, body []byte) []string {
	t.Helper()
	var payload struct {
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("正文不是合法 JSON: %v (%s)", err, body)
	}
	out := make([]string, 0, len(payload.Messages))
	for _, message := range payload.Messages {
		out = append(out, message.Role)
	}
	return out
}

func TestNormalizeDeveloperRoleOnChatCompletions(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6","messages":[{"role":"developer","content":"be terse"},` +
		`{"role":"user","content":"hi"},{"role":"developer","content":"also json"}]}`)
	request := &httpclient.Request{Body: body, JSONBody: append([]byte(nil), body...)}

	normalizeDeveloperRole(llm.APIFormatOpenAIChatCompletion, request)

	got := rolesOf(t, request.Body)
	want := []string{"system", "user", "system"}
	if len(got) != len(want) {
		t.Fatalf("消息条数变了: %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 条角色=%q, 期望 %q（全部: %v）", i, got[i], want[i], got)
		}
	}
	// JSONBody 与 Body 必须同步: 取哪一个都必须看到归一化后的角色。
	if string(request.JSONBody) != string(request.Body) {
		t.Fatalf("JSONBody 未同步: %s", request.JSONBody)
	}
	// 正文其余部分与内容不能被改动。
	if !json.Valid(request.Body) {
		t.Fatalf("归一化后正文不是合法 JSON: %s", request.Body)
	}
	var payload struct {
		Model    string `json:"model"`
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(request.Body, &payload); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if payload.Model != "gpt-5.6" || payload.Messages[0].Content != "be terse" || payload.Messages[2].Content != "also json" {
		t.Fatalf("除 role 外的字段被改动: %+v", payload)
	}
}

func TestNormalizeDeveloperRoleLeavesOtherShapesAlone(t *testing.T) {
	original := []byte(`{"model":"m","messages":[{"role":"system","content":"s"},{"role":"user","content":"u"}]}`)

	// Responses 形状看的是 input、Anthropic 形状看的是 messages 以外的结构: 都不该被本函数改动。
	for _, format := range []llm.APIFormat{llm.APIFormatOpenAIResponse, llm.APIFormatAnthropicMessage} {
		request := &httpclient.Request{Body: append([]byte(nil), original...)}
		normalizeDeveloperRole(format, request)
		if string(request.Body) != string(original) {
			t.Fatalf("format=%s 时不应改写正文: %s", format, request.Body)
		}
	}

	// 没有 messages 数组、messages 不是数组、空正文、nil 请求都不该 panic, 也不该改写。
	for _, body := range [][]byte{
		[]byte(`{"model":"m"}`),
		[]byte(`{"messages":"not-an-array"}`),
		[]byte(`{}`),
		[]byte(`not json`),
		nil,
	} {
		request := &httpclient.Request{Body: append([]byte(nil), body...)}
		normalizeDeveloperRole(llm.APIFormatOpenAIChatCompletion, request)
		if string(request.Body) != string(body) {
			t.Fatalf("不应改写: 原=%s 后=%s", body, request.Body)
		}
	}
	normalizeDeveloperRole(llm.APIFormatOpenAIChatCompletion, nil)
}

func TestNormalizeDeveloperRoleFallsBackToJSONBody(t *testing.T) {
	// 有的调用方只填 JSONBody: 也要能读到并同步回写两处。
	body := []byte(`{"messages":[{"role":"developer","content":"x"}]}`)
	request := &httpclient.Request{JSONBody: append([]byte(nil), body...)}
	normalizeDeveloperRole(llm.APIFormatOpenAIChatCompletion, request)
	if got := rolesOf(t, request.JSONBody); got[0] != "system" {
		t.Fatalf("JSONBody 未归一化: %v", got)
	}
}

// TestNormalizeDeveloperRoleOnResponsesInput 覆盖 Responses 形状：角色在顶层 input 数组里。
// 这是分协议位实测抓到的漏网路径（T-devrole-001）：此前只盖 chat 的 messages，客户端 chat 入、
// 上游 Responses 出时 developer 会原样发出去。
func TestNormalizeDeveloperRoleOnResponsesInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.6","input":[{"role":"developer","content":[{"type":"input_text","text":"be terse"}]},` +
		`{"type":"function_call","call_id":"c1","name":"f","arguments":"{}"},` +
		`{"role":"user","content":[{"type":"input_text","text":"hi"}]}],"temperature":0.25}`)
	request := &httpclient.Request{Body: body, JSONBody: append([]byte(nil), body...)}

	normalizeDeveloperRole(llm.APIFormatOpenAIResponse, request)

	var payload struct {
		Model       string  `json:"model"`
		Temperature float64 `json:"temperature"`
		Input       []struct {
			Type    string          `json:"type"`
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"input"`
	}
	if err := json.Unmarshal(request.Body, &payload); err != nil {
		t.Fatalf("归一化后正文不是合法 JSON: %v (%s)", err, request.Body)
	}
	if len(payload.Input) != 3 {
		t.Fatalf("条目数变了: %d", len(payload.Input))
	}
	if payload.Input[0].Role != "system" {
		t.Fatalf("input[0].role=%q, 期望 system", payload.Input[0].Role)
	}
	if payload.Input[1].Type != "function_call" || payload.Input[1].Role != "" {
		t.Fatalf("无 role 的条目被改动: %+v", payload.Input[1])
	}
	if payload.Input[2].Role != "user" {
		t.Fatalf("input[2].role=%q, 期望 user", payload.Input[2].Role)
	}
	if payload.Model != "gpt-5.6" || payload.Temperature != 0.25 {
		t.Fatalf("除 role 外的字段被改动: model=%q temperature=%v", payload.Model, payload.Temperature)
	}
	if !bytes.Contains(request.Body, []byte("be terse")) || !bytes.Contains(request.Body, []byte("input_text")) {
		t.Fatalf("内容被破坏: %s", request.Body)
	}
	if string(request.JSONBody) != string(request.Body) {
		t.Fatalf("JSONBody 未同步: %s", request.JSONBody)
	}
}

// TestNormalizeDeveloperRoleOnResponsesEdgeShapes Responses 的 input 允许是字符串, 也要安全跳过。
func TestNormalizeDeveloperRoleOnResponsesEdgeShapes(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"input":"a plain string prompt"}`),
		[]byte(`{"input":[]}`),
		[]byte(`{"input":{"role":"developer"}}`),
		[]byte(`{"model":"m"}`),
		nil,
	} {
		request := &httpclient.Request{Body: append([]byte(nil), body...)}
		normalizeDeveloperRole(llm.APIFormatOpenAIResponse, request)
		if string(request.Body) != string(body) {
			t.Fatalf("不应改写: 原=%s 后=%s", body, request.Body)
		}
	}
	normalizeDeveloperRole(llm.APIFormatOpenAIResponse, nil)
}
