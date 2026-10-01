package collector

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestLoginProxyCapturesSessionAndStaysInside 是"第一个办法"的端到端判据：
// 用户在 octopus 托管的地址里登录 → 服务端捕获会话 → 页面里的地址被打回代理内。
func TestLoginProxyCapturesSessionAndStaysInside(t *testing.T) {
	var upstreamCookie, upstreamAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCookie = r.Header.Get("Cookie")
		upstreamAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/login":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("X-Frame-Options", "DENY")
			_, _ = w.Write([]byte(`<form action="/api/login" method="post"><img src="/captcha.png"></form>`))
		case "/api/login":
			w.Header().Set("Set-Cookie", "sid=xyz; Domain=example.com; Path=/; Secure; HttpOnly; SameSite=None")
			w.Header().Set("Location", "/panel")
			w.WriteHeader(http.StatusFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	session := NewSession()
	proxy, err := NewLoginProxy(server.URL, session, "/api/v1/collector/login/7")
	if err != nil {
		t.Fatalf("建代理失败：%v", err)
	}

	// 1) 打开登录页：地址改写 + 去掉不能嵌 iframe 的头。
	request := httptest.NewRequest(http.MethodGet, "http://octopus/api/v1/collector/login/7/login", nil)
	// 带上我们自己的管理 Cookie 与 Authorization：它们绝不能被转发给上游。
	request.Header.Set("Cookie", "auth=admin-token; theme=dark")
	request.Header.Set("Authorization", "Bearer admin-token")
	recorder := httptest.NewRecorder()
	proxy.Serve(recorder, request, "/login")
	if recorder.Code != http.StatusOK {
		t.Fatalf("登录页代理失败：%d", recorder.Code)
	}
	body := recorder.Body.String()
	if !strings.Contains(body, `action="/api/v1/collector/login/7/api/login"`) {
		t.Errorf("表单 action 没被改写到代理前缀：%s", body)
	}
	if !strings.Contains(body, `src="/api/v1/collector/login/7/captcha.png"`) {
		t.Errorf("图片地址没被改写：%s", body)
	}
	if recorder.Header().Get("X-Frame-Options") != "" {
		t.Error("X-Frame-Options 没去掉，页面没法嵌进面板")
	}
	if strings.Contains(upstreamCookie, "admin-token") || upstreamAuth != "" {
		t.Errorf("我们自己的管理凭据被转发给上游了（Cookie=%q Auth=%q）——这等于把管理会话送给对方", upstreamCookie, upstreamAuth)
	}
	if !strings.Contains(upstreamCookie, "theme=dark") {
		t.Errorf("目标站点自己的 Cookie 被一起丢掉了（Cookie=%q）——会让站点以为没登录", upstreamCookie)
	}

	// 2) 提交登录：Set-Cookie 必须被改写成"在代理前缀下可用"，Location 必须打回代理内。
	form := strings.NewReader("username=u&password=p")
	request = httptest.NewRequest(http.MethodPost, "http://octopus/api/v1/collector/login/7/api/login", form)
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	recorder = httptest.NewRecorder()
	proxy.Serve(recorder, request, "/api/login")
	if location := recorder.Header().Get("Location"); location != "/api/v1/collector/login/7/panel" {
		t.Errorf("重定向没被改写：%q", location)
	}
	rawCookie := strings.Join(recorder.Header().Values("Set-Cookie"), " | ")
	if strings.Contains(rawCookie, "Domain=") || strings.Contains(strings.ToLower(rawCookie), "secure") {
		t.Errorf("Cookie 属性没被本地化（Domain/Secure 会让浏览器在 http 面板下拒收）：%s", rawCookie)
	}
	if !strings.Contains(rawCookie, "Path=/api/v1/collector/login/7/") {
		t.Errorf("Cookie 路径没指向代理前缀：%s", rawCookie)
	}
	if !strings.Contains(rawCookie, "SameSite=Lax") {
		t.Errorf("SameSite=None 应降级为 Lax（无 Secure 时浏览器直接丢弃）：%s", rawCookie)
	}

	// 3) 关键：会话被服务端捕获（后续采集才能用）。
	state := session.Export()
	if len(state.Cookies) != 1 || state.Cookies[0].Name != "sid" || state.Cookies[0].Value != "xyz" {
		t.Fatalf("登录会话没被捕获：%+v", state.Cookies)
	}
}

// TestLoginProxyRefusesForeignRedirect：上游把我们甩到别的站点时，必须拉回自己的前缀（不做开放代理）。
func TestLoginProxyRefusesForeignRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "https://evil.example.com/steal")
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	proxy, err := NewLoginProxy(server.URL, NewSession(), "/api/v1/collector/login/1")
	if err != nil {
		t.Fatalf("建代理失败：%v", err)
	}
	recorder := httptest.NewRecorder()
	proxy.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://octopus/api/v1/collector/login/1/", nil), "/")
	if location := recorder.Header().Get("Location"); strings.Contains(location, "evil.example.com") {
		t.Fatalf("跨站重定向没被拦住：%q", location)
	}
}

// TestLoginProxyStreamsNonHTML：非 HTML 响应原样透传（图片、JS 不能被文本改写弄坏）。
func TestLoginProxyStreamsNonHTML(t *testing.T) {
	payload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(payload)
	}))
	defer server.Close()
	proxy, err := NewLoginProxy(server.URL, NewSession(), "/api/v1/collector/login/2")
	if err != nil {
		t.Fatalf("建代理失败：%v", err)
	}
	recorder := httptest.NewRecorder()
	proxy.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://octopus/api/v1/collector/login/2/x.png", nil), "/x.png")
	got, _ := io.ReadAll(recorder.Body)
	if string(got) != string(payload) {
		t.Errorf("二进制响应被改写坏了：%v", got)
	}
}

// TestLoginProxyNeverLeaksAdminCookie 是一条安全判据：无论浏览器还带没带别的 Cookie，
// octopus 自己的管理会话都不得出现在发往上游的请求里。
//
// 这条用例来自实测缺陷：先前只有在"过滤后还有别的 Cookie"时才覆盖 Cookie 头，
// 于是当浏览器只带 auth 一个 Cookie 时，原始头被原样转发 —— 把管理会话送给了第三方站点。
func TestLoginProxyNeverLeaksAdminCookie(t *testing.T) {
	var upstreamCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	proxy, err := NewLoginProxy(server.URL, NewSession(), "/api/v1/collector/login/9")
	if err != nil {
		t.Fatalf("建代理失败：%v", err)
	}
	for _, header := range []string{"auth=admin-jwt", "auth=admin-jwt; theme=dark"} {
		upstreamCookie = ""
		request := httptest.NewRequest(http.MethodGet, "http://octopus/api/v1/collector/login/9/x", nil)
		request.Header.Set("Cookie", header)
		proxy.Serve(httptest.NewRecorder(), request, "/x")
		if strings.Contains(upstreamCookie, "admin-jwt") {
			t.Fatalf("管理会话被转发给上游（浏览器 Cookie=%q → 上游收到 %q）", header, upstreamCookie)
		}
	}
}

// TestLoginProxyKeepsContentLength：表单提交必须带 Content-Length。
// 不设长度会被 Go 改成 chunked，有些站点按长度判空体，表现是"登录页能打开、提交却总说密码错"。
func TestLoginProxyKeepsContentLength(t *testing.T) {
	var upstreamLength int64 = -1
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamLength = r.ContentLength
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	proxy, err := NewLoginProxy(server.URL, NewSession(), "/api/v1/collector/login/10")
	if err != nil {
		t.Fatalf("建代理失败：%v", err)
	}
	body := "username=yang&password=secret"
	request := httptest.NewRequest(http.MethodPost, "http://octopus/api/v1/collector/login/10/api/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	proxy.Serve(httptest.NewRecorder(), request, "/api/login")
	if upstreamLength != int64(len(body)) {
		t.Errorf("上游收到的 Content-Length=%d，期望 %d", upstreamLength, len(body))
	}
}
