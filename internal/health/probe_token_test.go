package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestValidProbeKind 类别取值校验：四类合法 + Admin-costs 占位合法 + 非法拒绝。
func TestValidProbeKind(t *testing.T) {
	for _, kind := range []ProbeKind{
		ProbeKindNAToken, ProbeKindS2Token, ProbeKindOpenAIKey, ProbeKindAnthropicKey, ProbeKindAdminCosts,
	} {
		if !ValidProbeKind(kind) {
			t.Fatalf("kind %q: want valid", kind)
		}
	}
	for _, kind := range []ProbeKind{"", "openai", "NA_TOKEN", "admin"} {
		if ValidProbeKind(kind) {
			t.Fatalf("kind %q: want invalid", kind)
		}
	}
}

// TestProbeTokenKinds 覆盖四类探测主路径：各自鉴权头正确、2xx 健康、401/空 token 失败。
func TestProbeTokenKinds(t *testing.T) {
	var gotAuth, gotAPIKey, gotVersion string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth != "Bearer na-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	})
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get(AnthropicVersionHeader)
		switch {
		case gotAPIKey != "" && gotAPIKey != "an-tok":
			w.WriteHeader(http.StatusUnauthorized)
		case gotAuth != "" && gotAuth != "Bearer ok-tok":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"data":[]}`))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cases := []struct {
		kind  ProbeKind
		token string
		check func(t *testing.T)
	}{
		{ProbeKindNAToken, "na-tok", func(t *testing.T) {
			if gotAuth != "Bearer na-tok" {
				t.Fatalf("NA auth = %q", gotAuth)
			}
		}},
		{ProbeKindS2Token, "ok-tok", func(t *testing.T) {
			if gotAuth != "Bearer ok-tok" {
				t.Fatalf("S2 auth = %q", gotAuth)
			}
		}},
		{ProbeKindOpenAIKey, "ok-tok", func(t *testing.T) {
			if gotAuth != "Bearer ok-tok" {
				t.Fatalf("OpenAI auth = %q", gotAuth)
			}
		}},
		{ProbeKindAnthropicKey, "an-tok", func(t *testing.T) {
			if gotAPIKey != "an-tok" || gotVersion != AnthropicAPIVersion {
				t.Fatalf("Anthropic x-api-key = %q version = %q", gotAPIKey, gotVersion)
			}
		}},
	}
	for _, tc := range cases {
		gotAuth, gotAPIKey, gotVersion = "", "", ""
		result := ProbeToken(probeTestContext(), tc.kind, srv.URL, tc.token, false)
		if !result.Healthy {
			t.Fatalf("%s: want healthy, got %+v", tc.kind, result)
		}
		if result.StatusCode != http.StatusOK || result.Error != "" {
			t.Fatalf("%s: status=%d error=%q", tc.kind, result.StatusCode, result.Error)
		}
		tc.check(t)
	}

	// 401 与空 token 均失败。
	if r := ProbeToken(probeTestContext(), ProbeKindOpenAIKey, srv.URL, "bad-tok", false); r.Healthy || r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token: want unhealthy 401, got %+v", r)
	}
	if r := ProbeToken(probeTestContext(), ProbeKindS2Token, srv.URL, "", false); r.Healthy || r.Error == "" {
		t.Fatalf("empty token: want unhealthy with error, got %+v", r)
	}
}

// TestProbeTokenRejectsNonJSON 2xx 但响应体非 JSON（HTML 错误页/空体）不得判健康。
func TestProbeTokenRejectsNonJSON(t *testing.T) {
	cases := []struct {
		name    string
		content string
		body    string
	}{
		{"html-error-page", "text/html", "<html><body>502 Bad Gateway</body></html>"},
		{"empty-body", "application/json", ""},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", tc.content)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(tc.body))
		}))
		result := ProbeToken(probeTestContext(), ProbeKindOpenAIKey, srv.URL, "tok", false)
		srv.Close()
		if result.Healthy {
			t.Fatalf("%s: want unhealthy, got %+v", tc.name, result)
		}
		if result.StatusCode != http.StatusOK {
			t.Fatalf("%s: want recorded 200, got %+v", tc.name, result)
		}
	}
}

// TestProbeTokenAdminCostsGate Admin-costs 权限门：关闭时不发请求、错误信息说明权限要求。
func TestProbeTokenAdminCostsGate(t *testing.T) {
	if AdminCostsProbeEnabled {
		t.Skip("admin costs gate is enabled; gate test only applies while disabled")
	}
	result := ProbeToken(probeTestContext(), ProbeKindAdminCosts, "http://127.0.0.1:1", "t", false)
	if result.Healthy || result.StatusCode != 0 {
		t.Fatalf("admin costs: want unhealthy no-request, got %+v", result)
	}
	if result.Error == "" {
		t.Fatalf("admin costs: want explanatory error")
	}
}

// TestProbeTokenUnknownKind 未知类别直接失败。
func TestProbeTokenUnknownKind(t *testing.T) {
	result := ProbeToken(probeTestContext(), ProbeKind("nope"), "http://127.0.0.1:1", "t", false)
	if result.Healthy || result.Error == "" {
		t.Fatalf("unknown kind: want unhealthy with error, got %+v", result)
	}
}
