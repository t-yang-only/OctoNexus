package collector

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRefreshSessionToken 会话续期：读账 401 时用 refresh_token 换新令牌，并把新旧令牌都写回会话。
//
// 为什么必须有这条：验证码站点的登录只能人工过一次，之后长期自动**全靠这一步**——
// 续期接口不需要验证码（两家实测都是 POST /api/v1/auth/refresh）。任何一步失败都必须原样返回 false，
// 否则会把真实的 401（refresh 也过期了、只能重新人工登录）掩盖成"成功"。
func TestRefreshSessionToken(t *testing.T) {
	var gotBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/auth/refresh" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"data":{"access_token":"new-tok","refresh_token":"new-ref"}}`))
	}))
	defer server.Close()

	session := NewSession()
	vars := map[string]string{"refresh_token": "old-ref"}
	if !refreshSessionToken(context.Background(), server.Client(), server.URL, session, vars) {
		t.Fatal("续期应成功")
	}
	if vars[VarToken] != "new-tok" {
		t.Fatalf("新访问令牌没写进 vars：%q", vars[VarToken])
	}
	if vars["refresh_token"] != "new-ref" {
		t.Fatalf("新 refresh 没写回 vars：%q", vars["refresh_token"])
	}
	if session.Vars()[VarToken] != "new-tok" {
		t.Fatal("新访问令牌没写进会话（下次采集仍会 401）")
	}
	if !strings.Contains(gotBody, "old-ref") {
		t.Fatalf("请求体没带旧 refresh：%s", gotBody)
	}

	// 没有 refresh_token：必须 false（不掩盖真实 401）。
	if refreshSessionToken(context.Background(), server.Client(), server.URL, NewSession(), map[string]string{}) {
		t.Fatal("没有 refresh_token 时不该报成功")
	}
	// 上游拒绝：必须 false。
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer bad.Close()
	if refreshSessionToken(context.Background(), bad.Client(), bad.URL, NewSession(), map[string]string{"refresh_token": "x"}) {
		t.Fatal("上游 400 时不该报成功")
	}
}
