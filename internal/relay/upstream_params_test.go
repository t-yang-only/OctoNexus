package relay

import (
	"encoding/json"
	"testing"

	"github.com/looplj/axonhub/llm"
)

// TestCarryOverSamplingParams 回归 NM-DS-005 实测到的有损点：客户端 Chat / Anthropic 入、
// 上游 Responses 出时，转换层不会把 temperature 写进上游请求体（同协议直通时它是在的），
// 于是"同一个请求换个协议就少了采样参数"。补写口径：只在客户端给了、上游没有时补；
// 其余情况一律返回原正文。
func TestCarryOverSamplingParams(t *testing.T) {
	clientBody := []byte(`{"model":"g","temperature":0.25,"top_p":0.9,"max_tokens":321}`)

	t.Run("Responses 目标缺 temperature 时补回", func(t *testing.T) {
		outbound := []byte(`{"model":"mock-good","input":[],"stream":false,"max_output_tokens":321}`)
		got, err := carryOverSamplingParams(clientBody, llm.APIFormatOpenAIResponse, outbound)
		if err != nil {
			t.Fatalf("carry over: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(got, &body); err != nil {
			t.Fatalf("result not JSON: %v", err)
		}
		if body["temperature"] != 0.25 || body["top_p"] != 0.9 {
			t.Fatalf("sampling params not carried: %v", body)
		}
		// 原有字段一个不少（只增不改）。
		for key, want := range map[string]any{"model": "mock-good", "stream": false, "max_output_tokens": float64(321)} {
			if body[key] != want {
				t.Fatalf("outbound field %q changed: got %v want %v", key, body[key], want)
			}
		}
		if _, ok := body["input"]; !ok {
			t.Fatalf("input field lost: %v", body)
		}
	})

	t.Run("上游已有值时不覆盖", func(t *testing.T) {
		outbound := []byte(`{"model":"mock-good","temperature":0.9}`)
		got, err := carryOverSamplingParams(clientBody, llm.APIFormatOpenAIResponse, outbound)
		if err != nil {
			t.Fatalf("carry over: %v", err)
		}
		var body map[string]any
		_ = json.Unmarshal(got, &body)
		if body["temperature"] != 0.9 {
			t.Fatalf("existing upstream value overwritten: %v", body)
		}
	})

	t.Run("客户端没给的参数不许凭空造", func(t *testing.T) {
		got, err := carryOverSamplingParams([]byte(`{"model":"g"}`), llm.APIFormatOpenAIResponse, []byte(`{"model":"m"}`))
		if err != nil {
			t.Fatalf("carry over: %v", err)
		}
		var body map[string]any
		_ = json.Unmarshal(got, &body)
		if len(body) != 1 {
			t.Fatalf("invented fields: %v", body)
		}
	})

	t.Run("非 Responses 目标一律不动", func(t *testing.T) {
		outbound := []byte(`{"model":"mock-good","messages":[]}`)
		got, err := carryOverSamplingParams(clientBody, llm.APIFormatOpenAIChatCompletion, outbound)
		if err != nil {
			t.Fatalf("carry over: %v", err)
		}
		if string(got) != string(outbound) {
			t.Fatalf("chat target body changed: %s", got)
		}
	})

	t.Run("非 JSON 正文按原样返回且不报错", func(t *testing.T) {
		got, err := carryOverSamplingParams([]byte("not json"), llm.APIFormatOpenAIResponse, []byte(`{"model":"m"}`))
		if err != nil || string(got) != `{"model":"m"}` {
			t.Fatalf("non-JSON client body must be a no-op: got=%s err=%v", got, err)
		}
		got, err = carryOverSamplingParams(clientBody, llm.APIFormatOpenAIResponse, []byte("<html>"))
		if err != nil || string(got) != "<html>" {
			t.Fatalf("non-JSON outbound body must be a no-op: got=%s err=%v", got, err)
		}
	})
}
