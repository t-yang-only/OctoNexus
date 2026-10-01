package health

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newNAStub 起一个 New API 系桩站点：/api/user/login 校验账密并下发 session cookie，
// /api/user/self 校验 cookie 后返回额度 JSON。
func newNAStub(t *testing.T, selfPayload map[string]any) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/user/login", func(w http.ResponseWriter, r *http.Request) {
		var body naLoginPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Username != "u" || body.Password != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "message": "bad creds"})
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "s1", Path: "/"})
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true})
	})
	mux.HandleFunc("/api/user/self", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("session"); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(selfPayload)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestNewAPILoginAndSelf(t *testing.T) {
	srv := newNAStub(t, map[string]any{"data": map[string]any{"quota": 500, "used_quota": 200}})
	client, ok := NewAPILogin(context.Background(), nil, srv.URL, "u", "p")
	if !ok {
		t.Fatalf("login: want ok")
	}
	if _, ok := NewAPILogin(context.Background(), nil, srv.URL, "u", "wrong"); ok {
		t.Fatalf("login with bad password: want fail")
	}
	snap, ok := NewAPISelf(context.Background(), client, srv.URL)
	if !ok {
		t.Fatalf("self: want ok")
	}
	if snap.Quota != 500 || snap.Used != 200 || snap.Remaining != 300 {
		t.Fatalf("self = %+v, want quota=500 used=200 remaining=300", snap)
	}
	// 未登录会话自查失败。
	if _, ok := NewAPISelf(context.Background(), nil, srv.URL); ok {
		t.Fatalf("self without session: want fail")
	}
}

func TestSub2APILoginAndReads(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var body s2LoginPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email != "a@x.io" || body.Password != "p" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "tok1"})
	})
	mux.HandleFunc("/api/subscriptions", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{
			{"id": 3, "plan": "pro", "expires_at": "2026-10-01"},
		}})
	})
	mux.HandleFunc("/api/platform/quotas", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"used": 12})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	token, ok := Sub2APILogin(context.Background(), nil, srv.URL, "a@x.io", "p")
	if !ok || token != "tok1" {
		t.Fatalf("login = %q %v, want tok1 true", token, ok)
	}
	if _, ok := Sub2APILogin(context.Background(), nil, srv.URL, "a@x.io", "wrong"); ok {
		t.Fatalf("login bad password: want fail")
	}
	subs, ok := Sub2APISubscriptions(context.Background(), nil, srv.URL, token)
	if !ok || len(subs) != 1 || subs[0].Plan != "pro" {
		t.Fatalf("subs = %+v ok=%v, want 1 pro", subs, ok)
	}
	quotas, ok := Sub2APIPlatformQuotas(context.Background(), nil, srv.URL, token)
	if !ok || string(quotas) == "" {
		t.Fatalf("quotas ok=%v, want raw payload", ok)
	}
}
