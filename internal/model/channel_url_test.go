package model

import (
	"strings"
	"testing"
)

// TestIsAPIVersionSegment 版本段识别: v1/v1beta/v2 之类算, 纯字母(vip)与空段不算。
func TestIsAPIVersionSegment(t *testing.T) {
	cases := map[string]bool{
		"v1": true, "V1": true, "v12": true, "v1beta": true, "v2alpha": true, "v1.1": false,
		"vip": false, "v": false, "": false, "1": false, "chat": false, "v-1": false,
	}
	for in, want := range cases {
		if got := IsAPIVersionSegment(in); got != want {
			t.Fatalf("IsAPIVersionSegment(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestChannelBaseURL 基地址与端点路径的版本段去重:
// 用户把 /v1 写进 BaseURL 时不再拼成 /v1/v1/..., 其余写法原样保留。
func TestChannelBaseURL(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		{"https://api.x5m5x.com", "/v1/chat/completions", "https://api.x5m5x.com"},
		{"https://api.x5m5x.com/", "/v1/chat/completions", "https://api.x5m5x.com"},
		{"https://api.thqllm.com/v1", "/v1/chat/completions", "https://api.thqllm.com"},
		{"https://api.thqllm.com/v1", "/v1/models", "https://api.thqllm.com"},
		{"https://host/api/v1", "/v1/messages", "https://host/api"},
		{"https://host/v1beta", "/v1beta/models", "https://host"},
		// 端点路径不含同版本段时不得动: 用户可能刻意把版本段放在基地址里, 端点只给 /chat/completions。
		{"https://host/v1", "/chat/completions", "https://host/v1"},
		{"https://host/v2", "/v1/chat/completions", "https://host/v2"},
		// 只有主机名且主机名恰好像版本段: 不得把主机名切掉。
		{"https://v1", "/v1/models", "https://v1"},
		{"https://host/", "/v1/models", "https://host"},
		{" https://host/v1 ", "/v1/models", "https://host"},
		{"https://host/v1", "", "https://host/v1"},
		{"", "/v1/models", ""},
	}
	for _, c := range cases {
		if got := ChannelBaseURL(c.base, c.path); got != c.want {
			t.Fatalf("ChannelBaseURL(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}

// TestJoinUpstreamURL 最终请求地址: 版本段去重 + 中间恰好一个斜杠。
func TestJoinUpstreamURL(t *testing.T) {
	cases := []struct {
		base, path, want string
	}{
		// 用户实测场景: THQ-gpt 的 base 带 /v1, 修复前拼成 /v1/v1/models 得 404。
		{"https://api.thqllm.com/v1", "/v1/models", "https://api.thqllm.com/v1/models"},
		{"https://api.x5m5x.com", "/v1/models", "https://api.x5m5x.com/v1/models"},
		{"https://api.x5m5x.com/", "v1/models", "https://api.x5m5x.com/v1/models"},
		{"https://host/v1/", "/v1/chat/completions", "https://host/v1/chat/completions"},
		{"https://host", "", "https://host"},
		{"https://host/v1", "", "https://host/v1"},
		{"", "/v1/models", "/v1/models"},
	}
	for _, c := range cases {
		if got := JoinUpstreamURL(c.base, c.path); got != c.want {
			t.Fatalf("JoinUpstreamURL(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}

// TestEndpointPathOrDefault 端点路径留空时落回默认路径（与库列默认值同值）。
func TestEndpointPathOrDefault(t *testing.T) {
	if got := EndpointPathOrDefault("", "/v1/responses"); got != "/v1/responses" {
		t.Fatalf("empty path = %q, want the fallback", got)
	}
	if got := EndpointPathOrDefault("  ", "/v1/responses"); got != "/v1/responses" {
		t.Fatalf("blank path = %q, want the fallback", got)
	}
	if got := EndpointPathOrDefault("/custom/models", "/v1/responses"); got != "/custom/models" {
		t.Fatalf("custom path = %q, want it kept", got)
	}
}

// TestJoinUpstreamURLNeverDoublesVersion 回归守卫: 任何"基地址末尾就是版本段"的写法
// 都不允许在结果里出现两段相同的版本段。
func TestJoinUpstreamURLNeverDoublesVersion(t *testing.T) {
	bases := []string{"https://host/v1", "https://host/v1/", "https://host/api/v1", "https://host/v1beta"}
	paths := []string{"/v1/models", "/v1/chat/completions", "/v1/responses", "/v1/messages", "/v1beta/models"}
	for _, base := range bases {
		for _, path := range paths {
			got := JoinUpstreamURL(base, path)
			if strings.Contains(got, "/v1/v1/") || strings.Contains(got, "/v1beta/v1beta/") {
				t.Fatalf("JoinUpstreamURL(%q, %q) = %q doubles the version segment", base, path, got)
			}
		}
	}
}
