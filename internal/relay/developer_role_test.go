package relay

import (
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

	// 非 chat completions 上游协议: 不改写（Responses/Anthropic 上游本就没有该约束）。
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
