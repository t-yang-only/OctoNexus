package collector

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRewriteScriptPullsAPICallsBehindPrefix 钉住交互登录能不能真的走通的**关键一跳**：
//
// 这两家 SPA 的登录调用写在 JS 里（形如 fetch("/api/v1/auth/login")）。
// 浏览器把根相对路径解析到面板自己的根，于是请求既不会流经反代、令牌也捕获不到 ——
// 用户看到的现象是"点了登录没反应"。所以脚本里的接口路径必须被改写到代理前缀下。
func TestRewriteScriptPullsAPICallsBehindPrefix(t *testing.T) {
	session := NewSession()
	proxy, err := NewLoginProxy("https://api.53hk.cn", session, "/api/v1/collector/login/7")
	if err != nil {
		t.Fatalf("建反代失败：%v", err)
	}
	source := strings.Join([]string{
		`fetch("/api/v1/auth/login",{method:"POST"})`,
		`xhr.open('GET','/api/v1/user/profile')`,
		"const u = `/api/v1/subscription`;",
		`const abs = "https://api.53hk.cn/api/v1/auth/me";`,
		`const other = "https://cdn.example.com/x.js";`,
	}, "\n")
	got := string(proxy.rewriteScript([]byte(source)))

	for _, want := range []string{
		`"/api/v1/collector/login/7/api/v1/auth/login"`,
		`'/api/v1/collector/login/7/api/v1/user/profile'`,
		"`/api/v1/collector/login/7/api/v1/subscription`",
		`"/api/v1/collector/login/7/api/v1/auth/me"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("脚本里应改写为 %s\n实得：%s", want, got)
		}
	}
	// 别人的域名一个字都不能动（反代绝不能变成开放代理）
	if !strings.Contains(got, `"https://cdn.example.com/x.js"`) {
		t.Errorf("非目标主机的地址不得改写，实得：%s", got)
	}
}

// TestIsScriptResponse 只改写脚本，绝不碰 JSON（改 JSON 会改坏数据）。
func TestIsScriptResponse(t *testing.T) {
	cases := map[string]bool{
		"application/javascript":                true,
		"application/javascript; charset=utf-8": true,
		"text/javascript":                       true,
		"application/ecmascript":                true,
		"application/json":                      false,
		"application/json; charset=utf-8":       false,
		"text/css":                              false,
		"text/html":                             false,
	}
	for contentType, want := range cases {
		if got := isScriptResponse(contentType); got != want {
			t.Errorf("isScriptResponse(%q) = %v，期望 %v", contentType, got, want)
		}
	}
}

// TestCaptureTokenFromProxiedResponse 反代流过的 JSON 响应里要能捞出令牌
// —— SPA 的会话不是 Cookie，只捕 Cookie 会导致"页面登录成功、采集却 401"。
func TestCaptureTokenFromProxiedResponse(t *testing.T) {
	for _, payload := range []string{
		`{"code":0,"data":{"access_token":"tok-abc","refresh_token":"r"}}`,
		`{"token":"tok-def"}`,
		`{"data":{"accessToken":"tok-ghi"}}`,
	} {
		session := NewSession()
		proxy, err := NewLoginProxy("https://api.example.com", session, "/api/v1/collector/login/1")
		if err != nil {
			t.Fatalf("建反代失败：%v", err)
		}
		proxy.captureToken([]byte(payload))
		if got := session.Vars()[VarToken]; got == "" {
			t.Errorf("应捕获到令牌，payload=%s", payload)
		}
	}
	// 非令牌响应不得凭空写令牌（也不得把整段 HTML 当令牌）
	session := NewSession()
	proxy, _ := NewLoginProxy("https://api.example.com", session, "/api/v1/collector/login/1")
	proxy.captureToken([]byte(`{"data":{"balance":12.6}}`))
	if got := session.Vars()[VarToken]; got != "" {
		t.Errorf("没有令牌字段时不得写入，实得 %q", got)
	}
	proxy.captureToken([]byte("<html>not json</html>"))
	if got := session.Vars()[VarToken]; got != "" {
		t.Errorf("非 JSON 响应不得写入令牌，实得 %q", got)
	}
}

// TestCaptureTokenKeepsRefreshToken 刷新令牌必须一并落进会话：只存访问令牌的话，
// 过期后仍要人工重登；存了 refresh_token 才能做到"人工登录一次、之后长期自动"。
func TestCaptureTokenKeepsRefreshToken(t *testing.T) {
	session := NewSession()
	proxy := &LoginProxy{session: session}
	proxy.captureToken([]byte(`{"code":0,"data":{"access_token":"tok-abc","refresh_token":"ref-xyz"}}`))
	if got := session.Vars()[VarToken]; got != "tok-abc" {
		t.Fatalf("访问令牌没捕获：%q", got)
	}
	if got := session.Vars()["refresh_token"]; got != "ref-xyz" {
		t.Fatalf("刷新令牌没捕获：%q", got)
	}
	// 只有访问令牌时不得凭空造出刷新令牌。
	proxy.captureToken([]byte(`{"data":{"access_token":"tok-2"}}`))
	if got := session.Vars()[VarToken]; got != "tok-2" {
		t.Fatalf("访问令牌没更新：%q", got)
	}
}

// TestCaptureEndpointStoresReportedTokens 页面 shim 上报的令牌必须落进会话。
// 为什么需要它：令牌可能既不在响应体、也不在 Cookie 里，而是 SPA 自己写进 localStorage 的；
// 只靠响应体捕获的站点会表现为"登录成功却仍然缺 token"（实测 53HK / apikey.fun 就卡在这里）。
func TestCaptureEndpointStoresReportedTokens(t *testing.T) {
	session := NewSession()
	proxy := &LoginProxy{session: session, prefix: "/p"}
	body := `{"access_token":"tok-abc","refresh_token":"ref-xyz","theme":"dark"}`
	req := httptest.NewRequest(http.MethodPost, "/p/__octopus/capture", strings.NewReader(body))
	rec := httptest.NewRecorder()
	proxy.Serve(rec, req, capturePath)
	if rec.Code != http.StatusOK {
		t.Fatalf("上报端点应回 200，实得 %d", rec.Code)
	}
	vars := session.Vars()
	if vars[VarToken] != "tok-abc" {
		t.Fatalf("token 没落进会话：%q", vars[VarToken])
	}
	if vars["refresh_token"] != "ref-xyz" {
		t.Fatalf("refresh_token 没落进会话：%q", vars["refresh_token"])
	}
	if vars["theme"] != "dark" {
		t.Fatalf("普通键也该留存（有的站点把用户名放这里）")
	}
	// 非 POST 一律拒绝，避免被当成写入面滥用。
	req2 := httptest.NewRequest(http.MethodGet, "/p/__octopus/capture", nil)
	rec2 := httptest.NewRecorder()
	proxy.Serve(rec2, req2, capturePath)
	if rec2.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET 上报端点应回 405，实得 %d", rec2.Code)
	}
}

// shimBalanced 判断脚本的括号是否平衡（{ } 与 ( ) 各自成对）。
func shimBalanced(js string) bool {
	for _, pair := range [][2]string{{"{", "}"}, {"(", ")"}} {
		if strings.Count(js, pair[0]) != strings.Count(js, pair[1]) {
			return false
		}
	}
	return true
}

// TestInjectedShimIsWellFormed 注入的 shim 必须是结构完整的脚本。
//
// 为什么必须有这条：本轮实测踩过 —— shim 里多了一个 }，IIFE 被提前闭合，
// 浏览器直接抛 SyntaxError 且**整段脚本静默不执行**（页面照常渲染，所以完全看不出来），
// 表现为"登录成功却永远抓不到令牌"。括号平衡是最便宜的哨兵。
func TestInjectedShimIsWellFormed(t *testing.T) {
	proxy := &LoginProxy{target: &url.URL{Scheme: "https", Host: "example.com"}, prefix: "/p"}
	body := proxy.injectOriginShim("<html><head></head><body></body></html>")
	start := strings.Index(body, "<script>")
	end := strings.Index(body, "</script>")
	if start < 0 || end <= start {
		t.Fatal("没有注入 shim 脚本")
	}
	js := body[start+len("<script>") : end]
	if !strings.HasPrefix(js, "(function(){") || !strings.HasSuffix(js, "})();") {
		t.Fatalf("shim 首尾结构不对：%q ... %q", js[:min(24, len(js))], js[max(0, len(js)-12):])
	}
	if !shimBalanced(js) {
		t.Fatalf("shim 括号不平衡（多一个会让 IIFE 提前闭合、脚本静默不执行）：%s", js)
	}
	// 负向对照自检：断言必须真的能识破本轮那个坏脚本，否则它只是装饰。
	bad := `(function(){var a=1;};function rp(){}setInterval(rp,1);})();`
	if shimBalanced(bad) {
		t.Fatal("括号平衡断言没有牙齿：它没能识破 IIFE 提前闭合的坏脚本")
	}
	// 括号平衡抓不到 ASI 类错误（本轮实测：少一个分号 → node 报 Unexpected token 'function'，
	// 而括号是平衡的）。所以有 node 就真解析一遍；没有就跳过（不能假装验过）。
	if nodePath, lookErr := exec.LookPath("node"); lookErr == nil {
		file := filepath.Join(t.TempDir(), "shim.js")
		if writeErr := os.WriteFile(file, []byte(js), 0o600); writeErr != nil {
			t.Fatalf("写临时脚本失败：%v", writeErr)
		}
		if out, runErr := exec.Command(nodePath, "--check", file).CombinedOutput(); runErr != nil {
			t.Fatalf("shim 未通过 JS 语法解析（浏览器会静默不执行）：%s", out)
		}
	} else {
		t.Log("本机没有 node，跳过真解析（括号平衡已验）")
	}
	// 关键能力必须在（改坏了要立刻知道）。
	for _, key := range []string{"window.fetch", "XMLHttpRequest.prototype.open", "__octopus/capture", "localStorage"} {
		if !strings.Contains(js, key) {
			t.Fatalf("shim 缺少关键能力：%s", key)
		}
	}
}

// TestCollapsePrefixAnywhere 路径里任意位置的前缀都要剥掉，且要幂等。
// 实测形状来自 nginx 日志：<前缀>/api/v1<前缀>/auth/login（点登录报 404 的真凶）。
func TestCollapsePrefixAnywhere(t *testing.T) {
	prefix := "/api/v1/collector/portal/4/TOK"
	proxy := &LoginProxy{prefix: prefix}
	cases := map[string]string{
		prefix + "/auth/login":                      "/auth/login",
		prefix + "/api/v1" + prefix + "/auth/login": "/api/v1/auth/login",
		prefix + prefix + "/auth/login":             "/auth/login",
		"/api/v1/auth/login":                        "/api/v1/auth/login",
		"":                                          "",
	}
	for in, want := range cases {
		if got := proxy.collapsePrefix(in); got != want {
			t.Fatalf("collapsePrefix(%q) = %q，期望 %q", in, got, want)
		}
		// 幂等：再剥一次结果不变。
		if got := proxy.collapsePrefix(proxy.collapsePrefix(in)); got != want {
			t.Fatalf("collapsePrefix 不幂等：%q", got)
		}
	}
}

// TestRewriteScriptFixesRouterBase SPA 路由 base 必须被改写成反代前缀。
// 实测站点 bundle 里是 `history:Ua("/")`：路由按整条 URL 匹配，带前缀的路径一律匹配不上，
// 登录后跳转就显示"页面未找到"（页面与数据其实都正常）。
func TestRewriteScriptFixesRouterBase(t *testing.T) {
	proxy := &LoginProxy{prefix: "/api/v1/collector/portal/4/TOK", target: &url.URL{Scheme: "https", Host: "example.com"}}
	in := []byte(`ae=qa({history:Ua("/"),routes:zp});`)
	out := string(proxy.rewriteScript(in))
	if !strings.Contains(out, `history:Ua("/api/v1/collector/portal/4/TOK/")`) {
		t.Fatalf("路由 base 没被改写：%s", out)
	}
	// 幂等：再跑一遍不得叠加。
	again := string(proxy.rewriteScript([]byte(out)))
	if strings.Count(again, "/api/v1/collector/portal/4/TOK/") != strings.Count(out, "/api/v1/collector/portal/4/TOK/") {
		t.Fatalf("路由 base 改写不幂等：%s", again)
	}
	// 未命中的写法必须原样保留。
	plain := []byte(`var x={history:"/other"};`)
	if got := string(proxy.rewriteScript(plain)); !strings.Contains(got, `history:"/other"`) {
		t.Fatalf("不该动的写法被改了：%s", got)
	}
}
