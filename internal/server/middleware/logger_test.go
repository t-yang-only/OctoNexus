package middleware

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestLoggerRedactsJumpTokenPath 一次性跳转令牌明文不得落访问日志（R-sec-001 红线）；
// 其他路径原样保留。
func TestLoggerRedactsJumpTokenPath(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	engine := gin.New()
	engine.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Output: &buf,
		Formatter: func(param gin.LogFormatterParams) string {
			return redactLogPath(param.Path) + "\n"
		},
	}))
	engine.GET("/api/v1/account/jump/go/:token", func(c *gin.Context) { c.Status(http.StatusOK) })
	engine.GET("/api/v1/channel/list", func(c *gin.Context) { c.Status(http.StatusOK) })

	const plain = "a3f1c0deadbeef0123456789abcdef0123456789abcdef0123456789abcdef01"
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/account/jump/go/"+plain, nil))
	engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/channel/list", nil))

	logged := buf.String()
	if strings.Contains(logged, plain) {
		t.Fatalf("plaintext token leaked into access log: %q", logged)
	}
	if !strings.Contains(logged, "/api/v1/account/jump/go/[REDACTED]") {
		t.Fatalf("jump path not redacted: %q", logged)
	}
	if !strings.Contains(logged, "/api/v1/channel/list") {
		t.Fatalf("normal path should stay intact: %q", logged)
	}
}

// TestRedactLogPathOnlyJumpPrefix 仅跳转前缀后紧跟的段被掩码，前缀自身/无关路径不动。
func TestRedactLogPathOnlyJumpPrefix(t *testing.T) {
	cases := map[string]string{
		"/api/v1/account/jump/go/":              "/api/v1/account/jump/go/[REDACTED]",
		"/api/v1/account/jump/go/abc":           "/api/v1/account/jump/go/[REDACTED]",
		"/api/v1/account/jump/create":           "/api/v1/account/jump/create",
		"/api/v1/account/jump/list":             "/api/v1/account/jump/list",
		"/api/v1/channel/detail":                "/api/v1/channel/detail",
		"/x/api/v1/account/jump/go/token-in-fix": "/x/api/v1/account/jump/go/[REDACTED]",
	}
	for in, want := range cases {
		if got := redactLogPath(in); got != want {
			t.Fatalf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}
