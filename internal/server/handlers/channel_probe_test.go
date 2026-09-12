package handlers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// newProbeTestUpstream 起一个按 (模型, 协议) 规则回话的假上游:
//   - chat: 除 dead-chat 外全部 200
//   - responses: 仅 gpt-x 200, 其余 404
//   - messages: 仅 claude-y 200, 其余 404
//     并把每次请求的 Authorization / X-Api-Key 记入 seen, 供断言凭据头正确。
//     seen 由测试收尾后串行读取, 写入侧必须持锁: probeModels 对同一模型三协议并发,
//     messages 分支一次写两个键, 无锁并发写 map 会直接 fatal("concurrent map writes")。
func newProbeTestUpstream(t *testing.T, seen *map[string]string) *httptest.Server {
	t.Helper()
	var seenMu sync.Mutex
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		(*seen)["chat-auth"] = r.Header.Get("Authorization")
		seenMu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		// 只有 gpt-x 与 claude-y 走 chat; dead-chat 与 nothing-works 一律 404。
		if body["model"] != "gpt-x" && body["model"] != "claude-y" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"message":"model not found"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"ok","choices":[{"message":{"content":"pong"}}]}`)
	})
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		(*seen)["response-auth"] = r.Header.Get("Authorization")
		seenMu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "gpt-x" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":{"message":"no route"}}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"resp_1","status":"completed"}`)
	})
	mux.HandleFunc("/v1/messages", func(w http.ResponseWriter, r *http.Request) {
		seenMu.Lock()
		(*seen)["message-auth"] = r.Header.Get("X-Api-Key")
		(*seen)["message-version"] = r.Header.Get("Anthropic-Version")
		seenMu.Unlock()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "claude-y" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"error"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"msg_1","content":[{"text":"pong"}]}`)
	})
	// 未知路径按 200 + HTML 回话: 验证探测不只看状态码, 非 JSON 正文必须判为不支持。
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<html>gateway landing</html>`)
	})
	return httptest.NewServer(mux)
}

func TestProbeModelsPerProtocolBits(t *testing.T) {
	seen := map[string]string{}
	upstream := newProbeTestUpstream(t, &seen)
	defer upstream.Close()

	target := model.ChannelConfig{
		BaseURL:                  upstream.URL,
		OpenAIChatCompletionPath: "/v1/chat/completions",
		OpenAIResponsePath:       "/v1/responses",
		AnthropicMessagePath:     "/v1/messages",
	}
	order := []string{"gpt-x", "claude-y", "dead-chat", "nothing-works"}
	results := probeModels(http.DefaultClient, context.Background(), target, "sk-test", order)

	if len(results) != len(order) {
		t.Fatalf("results length = %d, want %d", len(results), len(order))
	}
	// 输入顺序必须原样保留, 界面排列才不会随机。
	for i, name := range order {
		if results[i].Name != name {
			t.Fatalf("results[%d].Name = %q, want %q", i, results[i].Name, name)
		}
	}

	want := map[string]model.Protocol{
		// chat 200 + responses 200, messages 404 → 只亮两枚 OpenAI 位。
		"gpt-x": model.ProtocolOpenAIChatCompletion | model.ProtocolOpenAIResponse,
		// chat 200 + messages 200, responses 404。
		"claude-y": model.ProtocolOpenAIChatCompletion | model.ProtocolAnthropicMessage,
		// chat 明确 404, responses/messages 404 → 全灭。
		"dead-chat":     0,
		"nothing-works": 0,
	}
	for _, m := range results {
		if m.Protocols != want[m.Name] {
			t.Errorf("%s protocols = %b, want %b (probes: %+v)", m.Name, m.Protocols, want[m.Name], m.Probes)
		}
		if len(m.Probes) != 3 {
			t.Errorf("%s probes length = %d, want 3 (三协议全探, 不按 /models 归属裁剪)", m.Name, len(m.Probes))
		}
	}

	// 全灭模型也要给出三条失败明细, 供界面展示 error 原因。
	dead := results[2]
	for _, p := range dead.Probes {
		if p.OK || p.Error == "" {
			t.Errorf("dead-chat probe %+v: want failed with error summary", p)
		}
	}

	if seen["chat-auth"] != "Bearer sk-test" || seen["response-auth"] != "Bearer sk-test" {
		t.Errorf("openai probes must carry Bearer auth, got %q / %q", seen["chat-auth"], seen["response-auth"])
	}
	if seen["message-auth"] != "sk-test" || seen["message-version"] == "" {
		t.Errorf("anthropic probe must carry X-Api-Key + version, got %q / %q", seen["message-auth"], seen["message-version"])
	}
}

// 空路径回退默认值: 表单未填路径时探测仍打到标准端点而不是根路径 HTML。
func TestProbeEmptyPathsFallBackToDefaults(t *testing.T) {
	seen := map[string]string{}
	upstream := newProbeTestUpstream(t, &seen)
	defer upstream.Close()

	target := model.ChannelConfig{BaseURL: upstream.URL}
	results := probeModels(http.DefaultClient, context.Background(), target, "sk-test", []string{"gpt-x"})
	if results[0].Protocols != model.ProtocolOpenAIChatCompletion|model.ProtocolOpenAIResponse {
		t.Fatalf("empty-path probe = %b, want chat|response (probes: %+v)", results[0].Protocols, results[0].Probes)
	}
}

// 200+HTML 网关必须判为不支持, 而不是只看状态码。
func TestProbeRejectsJSONLookalikeHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html>ok</html>")
	}))
	defer server.Close()

	target := model.ChannelConfig{BaseURL: server.URL}
	results := probeModels(http.DefaultClient, context.Background(), target, "k", []string{"any"})
	if results[0].Protocols != 0 {
		t.Fatalf("200 HTML must score no protocols, got %b", results[0].Protocols)
	}
	for _, p := range results[0].Probes {
		if !strings.Contains(p.Error, "non-json body") {
			t.Errorf("probe %+v: error summary should mention non-json body", p)
		}
	}
}
