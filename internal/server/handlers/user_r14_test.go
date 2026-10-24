package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
)

// R14-F2 回归: 登出不得要求 JSON body(无 Content-Type 的 POST 曾因挂在
// RequireJSON 组返回 415, 前端清不掉 HttpOnly cookie), 且必须服务端清除
// auth cookie(HttpOnly 之后前端无权清)。
func TestLogoutClearsCookieWithoutJSONBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	engine := gin.New()
	if err := router.RegisterOn(engine, router.ServerAdmin); err != nil {
		t.Fatalf("register admin: %v", err)
	}

	// 无 Content-Type、无 body —— 正是前端 fetch('/api/v1/user/logout', {method:'POST'}) 的形态
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/logout", nil)
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("登出应 200(不要求 JSON body), 实际 %d: %s", w.Code, w.Body.String())
	}
	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "auth=") || !strings.Contains(setCookie, "HttpOnly") {
		t.Fatalf("登出应清除 auth cookie 且带 HttpOnly, 实际 %q", setCookie)
	}
}
