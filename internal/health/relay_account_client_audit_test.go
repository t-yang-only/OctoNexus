package health

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestSub2APIGetRejectsNonJSON S2 读侧公共路径：2xx 但 HTML 错误页/空体判失败，
// 不把垃圾体透传给 quotas 等 RawMessage 下游（anti-200-HTML 口径，NM-CUR-149 审计补）。
func TestSub2APIGetRejectsNonJSON(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"html-gateway-page", "<html><body>200 but gateway login</body></html>"},
		{"empty-body", ""},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(tc.body))
		}))
		quotas, ok := Sub2APIPlatformQuotas(context.Background(), nil, srv.URL, "tok")
		srv.Close()
		if ok {
			t.Fatalf("%s: want fail for non-JSON 2xx body, got %q", tc.name, string(quotas))
		}
	}
}

// TestSub2APIGetAcceptsJSONArray quotas 透传路径须同时兼容对象与顶层数组两种 JSON。
func TestSub2APIGetAcceptsJSONArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"platform":"openai"}]`))
	}))
	t.Cleanup(srv.Close)
	quotas, ok := Sub2APIPlatformQuotas(context.Background(), nil, srv.URL, "tok")
	if !ok || string(quotas) == "" {
		t.Fatalf("want raw array passthrough, got ok=%v %q", ok, string(quotas))
	}
}
