package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// R14-F1/F3: auth cookie 必须带 HttpOnly(XSS 读不到 JWT), Secure 按链路判定
// (反代 terminate TLS 时 gin 看到 http, 必须认 X-Forwarded-Proto)。
func TestAuthCookieFlags(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 场景 1: 本地 http(无 XFP) —— HttpOnly 必须有, Secure 不应有(否则本地开发直接坏)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/login", nil)
	ClearAuthCookie(c)
	setCookie := w.Header().Get("Set-Cookie")
	if !contains(setCookie, "HttpOnly") {
		t.Fatalf("清除 cookie 必须带 HttpOnly, 实际: %q", setCookie)
	}
	if contains(setCookie, "Secure") {
		t.Fatalf("本地 http 不应带 Secure, 实际: %q", setCookie)
	}

	// 场景 2: 反代 HTTPS(XFP=https) —— 必须带 Secure
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/login", nil)
	c2.Request.Header.Set("X-Forwarded-Proto", "https")
	ClearAuthCookie(c2)
	setCookie2 := w2.Header().Get("Set-Cookie")
	if !contains(setCookie2, "HttpOnly") || !contains(setCookie2, "Secure") {
		t.Fatalf("HTTPS 链路 cookie 必须同时带 HttpOnly 与 Secure, 实际: %q", setCookie2)
	}

	// 场景 3: 直接 TLS(测试直连) —— Secure
	w3 := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(w3)
	c3.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/login", nil)
	c3.Request.TLS = &tls.ConnectionState{}
	ClearAuthCookie(c3)
	setCookie3 := w3.Header().Get("Set-Cookie")
	if !contains(setCookie3, "Secure") {
		t.Fatalf("TLS 直连必须带 Secure, 实际: %q", setCookie3)
	}
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}
