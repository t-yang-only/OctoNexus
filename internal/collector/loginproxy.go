package collector

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// LoginProxy 把目标站点的页面反代到 octopus 自己的地址下，于是：
//   - 用户在**我们托管的页面**里输入账号、密码、验证码；
//   - 登录成功后目标站点下发的 Cookie 由 octopus 服务端捕获（不依赖用户手工复制任何东西）；
//   - 之后余额采集直接复用这份会话。
//
// 这就是用户说的第一个办法（"网站被反向代理成临时地址，我在这里登录拿到凭证"）。
//
// 三条硬约束：
//   - 只代理采集包/站点自己声明的那一个主机，绝不做开放代理；
//   - 浏览器带来的 octopus 自己的 Cookie/Authorization **绝不转发给上游**（否则等于把自己的管理会话送给对方）；
//   - 响应里重定向一律改写成"打回我们自己的前缀"，让浏览器始终停在代理内。
type LoginProxy struct {
	target  *url.URL
	session *Session
	prefix  string
	client  *http.Client
	rewrite []*regexp.Regexp
	// fallbackClients 是备用出口（节点池会抖：实测同一节点几分钟前 200、几分钟后 EOF）。
	// 主出口失败时按顺序换下一个重试，避免"登录页打不开 / 采集 EOF"看起来像功能坏了。
	fallbackClients []*http.Client
}

const proxyBodyLimit = 8 << 20

// NewLoginProxy 建立到 targetBase 的登录代理；prefix 是它挂在 octopus 上的路径前缀（不含尾部斜杠）。
func NewLoginProxy(targetBase string, session *Session, prefix string) (*LoginProxy, error) {
	return NewLoginProxyVia(targetBase, session, prefix, "")
}

// NewLoginProxyVia 在指定出口上反代目标站点的登录页：
// 页面加载与表单提交都从该出口发出，站点看不到使用者的真实 IP。
func NewLoginProxyVia(targetBase string, session *Session, prefix, proxyURL string) (*LoginProxy, error) {
	parsed, err := url.Parse(strings.TrimSpace(targetBase))
	if err != nil {
		return nil, fmt.Errorf("站点地址无法解析：%v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("站点地址只允许 http/https")
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("站点地址缺少主机")
	}
	if session == nil {
		return nil, fmt.Errorf("缺少会话")
	}
	return &LoginProxy{
		target:  parsed,
		session: session,
		prefix:  strings.TrimSuffix(prefix, "/"),
		client: &http.Client{
			Timeout: 60 * time.Second,
			// 不自动跟随：把重定向交回浏览器（我们改写过 Location），这样地址栏与 Cookie 都在我们这一侧。
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			// 出口：指定了代理节点就用它。反代登录页时「页面加载 + 表单提交」都从该出口发出，
			// 站点看到的不是使用者的真实 IP —— 这是「用反代抓验证码」这条路不暴露 IP 的前提。
			Transport: &http.Transport{
				Proxy:               proxyFuncFor(proxyURL),
				TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
				TLSHandshakeTimeout: 15 * time.Second,
			},
		},
		rewrite: []*regexp.Regexp{
			regexp.MustCompile(`(?i)(href|src|action|data-src)\s*=\s*"/([^"]*)"`),
			regexp.MustCompile(`(?i)(href|src|action|data-src)\s*=\s*'/([^']*)'`),
		},
	}, nil
}

// Prefix 返回代理挂载的路径前缀。
// SetFallbackProxies 设置备用出口客户端（由调用方按可用节点列表构造）。
func (p *LoginProxy) SetFallbackProxies(clients []*http.Client) {
	p.fallbackClients = append(p.fallbackClients, clients...)
}

func (p *LoginProxy) Prefix() string { return p.prefix }

// Session 返回被捕获的会话（登录代理与采集共用同一份）。
func (p *LoginProxy) Session() *Session { return p.session }

// ProxyURL 是给用户打开的"登录临时地址"。
func (p *LoginProxy) ProxyURL() string { return p.prefix + "/" }

// Serve 处理一次代理请求；subPath 是前缀之后的部分（gin 通配符带前导斜杠）。
func (p *LoginProxy) Serve(w http.ResponseWriter, r *http.Request, subPath string) {
	if !strings.HasPrefix(subPath, "/") {
		subPath = "/" + subPath
	}
	// 页面里的 shim 会把 localStorage 里形如令牌的键值上报到这里。这是**兜底捕获**：
	// 令牌可能既不在响应体、也不在 Cookie 里，而是 SPA 自己写进 localStorage 的
	// （只靠响应体捕获的站点会表现为"登录成功却仍然缺 token"）。
	if subPath == capturePath {
		p.handleCapture(w, r)
		return
	}
	if subPath == "/" || subPath == "" {
		subPath = p.target.Path
		if subPath == "" {
			subPath = "/"
		}
	}
	// 折叠重复前缀：SPA 会把 <base> 前缀与"已被改写的接口字面量"拼在一起，于是请求路径里出现第二次前缀
	// （实测 .../portal/4/<tok>/api/v1/collector/portal/4/<tok>/settings/public）。不折叠就会被当成站点
	// 自己的路径转发过去 → 站点回 404（用户实测"登录报 404"就是它）。
	// 前缀在路径里可能出现在**任何位置**，而不只是开头：SPA 会把 <base> 前缀与运行时拼出来的
	// 绝对地址叠起来，实测出现 `<前缀>/api/v1<前缀>/auth/login` 这种形状（日志原文）。
	// 只剥开头那一次是不够的——中间那一次会被当成站点自己的路径转发过去，站点回 404。
	// 前缀里带一次性随机令牌，正常业务路径不会包含它，所以全局剥除是安全的。
	subPath = p.collapsePrefix(subPath)
	// 兼容丢前缀的 API 调用：有些 SPA 的 axios base 在反代下丢了 /api/v1（实测登录 POST 打到
	// <prefix>/auth/login），站点会回 404，用户看到的就是"登录报 404"。
	// 只对**看起来是 API 调用**的请求补前缀（写方法或 JSON 请求体），静态资源与页面请求一律不动。
	if !strings.HasPrefix(subPath, "/api/") {
		method := r.Method
		jsonish := strings.Contains(r.Header.Get("Content-Type"), "json")
		if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || jsonish {
			subPath = "/api/v1" + subPath
		}
	}
	target := *p.target
	target.Path = strings.TrimSuffix(p.target.Path, "/") + subPath
	target.RawQuery = r.URL.RawQuery

	request, err := http.NewRequestWithContext(r.Context(), r.Method, target.String(), r.Body)
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, "请求无法构造："+err.Error())
		return
	}
	copyProxyHeaders(request.Header, r.Header)
	// 我们自己的管理会话绝不出境。
	request.Header.Del("Authorization")
	request.Header.Del("Cookie")
	if filtered := dropAuthCookie(r.Header.Get("Cookie")); filtered != "" {
		request.Header.Set("Cookie", filtered)
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Accept-Language", r.Header.Get("Accept-Language"))
	request.Host = target.Host
	// 保持原始正文长度：不设的话 Go 会改用 chunked 传输，而不少站点按 Content-Length 判空体，
	// 表现就是"登录页能打开、提交却一直说密码错"（本轮实测）。
	if r.ContentLength >= 0 {
		request.ContentLength = r.ContentLength
	}

	response, err := p.client.Do(request)
	if err != nil {
		// 主出口抖动：换备用出口重试。只对**无请求体**的请求重试（有体的请求体已被读走，重放会出错）。
		for _, fallback := range p.fallbackClients {
			if r.Body != nil && r.ContentLength > 0 {
				break
			}
			retry, retryErr := http.NewRequestWithContext(r.Context(), r.Method, target.String(), nil)
			if retryErr != nil {
				continue
			}
			copyProxyHeaders(retry.Header, r.Header)
			retry.Header.Del("Authorization")
			retry.Header.Del("Cookie")
			if filtered := dropAuthCookie(r.Header.Get("Cookie")); filtered != "" {
				retry.Header.Set("Cookie", filtered)
			}
			retry.Header.Set("Accept-Encoding", "identity")
			retry.Header.Set("Accept-Language", r.Header.Get("Accept-Language"))
			retry.Host = target.Host
			if candidate, candidateErr := fallback.Do(retry); candidateErr == nil {
				response, err = candidate, nil
				break
			}
		}
	}
	if err != nil {
		writeProxyError(w, http.StatusBadGateway, "上游请求失败（已试过备用出口）："+trim(err.Error()))
		return
	}
	defer response.Body.Close()
	// 服务端捕获会话：这是“用户在页面里登录一次、之后长期自动采集”的关键一跳。
	if cookies := response.Cookies(); len(cookies) > 0 {
		p.session.jar.SetCookies(&target, cookies)
	}

	header := w.Header()
	copyProxyHeaders(header, response.Header)
	header.Del("X-Frame-Options")
	header.Del("Content-Security-Policy")
	header.Del("Content-Security-Policy-Report-Only")
	header.Del("Content-Length")
	header.Del("Content-Encoding")
	if location := response.Header.Get("Location"); location != "" {
		header.Set("Location", p.rewriteLocation(location))
	}
	if cookies := response.Header.Values("Set-Cookie"); len(cookies) > 0 {
		header.Del("Set-Cookie")
		for _, cookie := range cookies {
			header.Add("Set-Cookie", p.rewriteSetCookie(cookie))
		}
	}

	contentType := response.Header.Get("Content-Type")
	if !strings.Contains(strings.ToLower(contentType), "text/html") {
		// 交互登录里最重要的一跳其实在这里：现代 SPA 中转站的会话不是 Cookie，
		// 而是登录响应体里的 access_token（实测 okai.la / cochacode.com / aiaaa.cc / sub.tohoqing.com
		// 与 new-api 的 api.uu6.top / pipixia1.online 都是这个形状）。
		// 只捕获 Cookie 的话，用户明明在页面里登录成功了，后续采集却一律 401 ——
		// 所以凡是流经代理的响应，都顺手把令牌捞进会话变量，供采集步骤用 {token} 引用。
		raw, _ := io.ReadAll(io.LimitReader(response.Body, proxyBodyLimit))
		p.captureToken(raw)
		// 脚本要改写接口地址：SPA 的调用写在 JS 里（形如 "/api/v1/auth/login"），
		// 这种根相对路径在浏览器里会解析到**我们自己的根**而不是代理前缀，
		// 于是登录请求根本不会流经这里（用户会看到"点了登录没反应"）。
		if isScriptResponse(contentType) {
			raw = p.rewriteScript(raw)
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(raw)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(response.Body, proxyBodyLimit))
	body := p.rewriteHTML(raw)
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

// captureToken 从任意流经代理的响应体里捞出访问令牌（只在能确定是令牌字段时才写）。
// 这是"人在浏览器里过验证码、octopus 在服务端拿到会话"这条路的公共段：
// HTML 登录页里没有令牌，真正的令牌在后续的 XHR 响应里 —— 反代必须看得见它。
func (p *LoginProxy) captureToken(raw []byte) {
	if p.session == nil || len(raw) == 0 || len(raw) > proxyBodyLimit {
		return
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || (trimmed[0] != '{' && trimmed[0] != '[') {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(trimmed, &payload); err != nil {
		return
	}
	candidates := []map[string]any{payload}
	if data, ok := payload["data"].(map[string]any); ok {
		candidates = append(candidates, data)
	}
	if data, ok := payload["user"].(map[string]any); ok {
		candidates = append(candidates, data)
	}
	for _, source := range candidates {
		for _, name := range []string{"access_token", "token", "accessToken"} {
			if value, ok := source[name].(string); ok && strings.TrimSpace(value) != "" {
				p.session.SetVars(map[string]string{VarToken: value})
				break
			}
		}
		// 刷新令牌也一并存下：访问令牌过期时可以用它换新的（这一步不需要验证码），
		// 这是"人工登录一次、之后长期自动"的关键 —— 只存访问令牌的话，过期后仍要人工重登。
		if value, ok := source["refresh_token"].(string); ok && strings.TrimSpace(value) != "" {
			p.session.SetVars(map[string]string{"refresh_token": value})
		}
	}
}

// rewriteLocation 把上游的重定向改写成打回我们自己的前缀；跨主机跳转一律拒绝（避免变成开放代理）。
func (p *LoginProxy) rewriteLocation(location string) string {
	parsed, err := url.Parse(location)
	if err != nil {
		return p.prefix + "/"
	}
	if parsed.Host != "" && !strings.EqualFold(parsed.Host, p.target.Host) {
		return p.prefix + "/"
	}
	path := parsed.Path
	if path == "" {
		path = "/"
	}
	out := p.prefix + path
	if parsed.RawQuery != "" {
		out += "?" + parsed.RawQuery
	}
	return out
}

// rewriteSetCookie 让上游 Cookie 在"我们这一侧"可用：去掉 Domain（不要泄漏站点域）、
// 去掉 Secure（用户可能通过 http 访问面板）、SameSite=None 降级为 Lax（无 Secure 时浏览器会拒收 None）。
func (p *LoginProxy) rewriteSetCookie(raw string) string {
	parts := strings.Split(raw, ";")
	out := []string{strings.TrimSpace(parts[0])}
	sawPath := false
	for _, attribute := range parts[1:] {
		trimmed := strings.TrimSpace(attribute)
		lower := strings.ToLower(trimmed)
		switch {
		case strings.HasPrefix(lower, "domain="), lower == "secure":
			continue
		case lower == "samesite=none":
			// 无 Secure 时浏览器会直接丢弃 SameSite=None，降级成 Lax 才能在 http 面板下生效。
			out = append(out, "SameSite=Lax")
		case strings.HasPrefix(lower, "path="):
			out = append(out, "Path="+p.prefix+"/")
			sawPath = true
		default:
			out = append(out, trimmed)
		}
	}
	if !sawPath {
		out = append(out, "Path="+p.prefix+"/")
	}
	return strings.Join(out, "; ")
}

// rewriteHTML 做尽力而为的地址改写：绝对地址与根相对地址都拉回代理前缀。//
// 诚实的边界：这是文本级改写，不是完整 HTML 解析。用 JS 动态拼出来的地址、写在 CSS/JS 文件里的
// 相对路径（如 fetch('/api/x')）改不到；这类站点请改用"包"（form 模式）直接打接口。
// isScriptResponse 判断这是不是需要改写接口地址的脚本响应。
// 只认脚本类型：JSON 响应绝不能改写（会改坏数据），CSS 里也不会有接口地址。
func isScriptResponse(contentType string) bool {
	lowered := strings.ToLower(contentType)
	return strings.Contains(lowered, "javascript") || strings.Contains(lowered, "ecmascript")
}

// rewriteScript 把脚本里的接口地址拉回代理前缀。
//
// 为什么必须做：这两家 SPA 的登录调用写在 JS 里，形如 fetch("/api/v1/auth/login")。
// 浏览器会把这种**根相对路径**解析成 http://127.0.0.1:<面板端口>/api/v1/auth/login，
// 也就是打到 octopus 自己的根路径上 —— 结果是"点了登录没反应 / 404"，
// 而且这个请求根本不会流经反代，令牌自然也捕获不到。
//
// 三种形态都要覆盖（实测这几家的 bundle 里都有）：
//   - 绝对地址：https://api.53hk.cn/api/... → 换成前缀（顺带把跨域问题一起消掉）
//   - 引号里的根相对路径："/api/..."、'/api/...
//   - 模板字符串：`/api/...
//
// 只改这些前缀，不动其它字节；命中不了就原样返回 —— 失败方向是"登录页可能不可用"，
// 而不是"把请求偷偷发到别处"。
func (p *LoginProxy) rewriteScript(raw []byte) []byte {
	// 前缀自己也以 /api/ 开头（/api/v1/collector/login/N），所以先用占位符把它保护起来，
	// 否则二次替换会把已改好的地址再改一遍：
	// .../login/7/api/v1/collector/login/7/api/v1/auth/me（实测踩过）。
	const marker = "\x00octopus-prefix\x00"
	body := strings.ReplaceAll(string(raw), p.prefix, marker)
	for _, origin := range []string{"https://" + p.target.Host, "http://" + p.target.Host} {
		body = strings.ReplaceAll(body, origin, marker)
	}
	for _, quote := range []string{"\"", "'", "`"} {
		body = strings.ReplaceAll(body, quote+"/api/", quote+marker+"/api/")
	}
	// 下面两个改写**必须在还原前缀之前**做：此时前缀被 marker 遮住，天然幂等；
	// 放到还原之后会看到已带前缀的串，于是再包一层 → 出现 .../portal/4/<tok>/api/v1/.../portal/4/<tok>/settings/public 这种双前缀（实测踩到）。
	body = p.prefixRootRelative(body)
	body = p.prefixSlashConcat(body)
	// SPA 的前端路由 base 写死为 "/"（实测站点 bundle：`history:Ua("/")`），于是路由按**整条 URL**
	// 匹配 —— 带反代前缀的路径一律匹配不上，登录后跳转就显示"页面未找到"（页面本身是好的，
	// 数据也照常抓到）。把路由 base 改成反代前缀，路由即可正常匹配。
	body = routerBaseRe.ReplaceAllString(body, `history:$1("`+p.prefix+`/")`)
	body = strings.ReplaceAll(body, marker, p.prefix)
	var buffer bytes.Buffer
	buffer.WriteString(body)
	return buffer.Bytes()
}

// prefixRootRelative 把正文里的根相对字面量（`"/assets/...`、`'/assets/...`）拉回反代前缀下。
//
// 为什么必须做：`<base>` 只影响**相对**引用；以单个 / 开头的 URL 是"根相对"，浏览器只按 origin 解析，
// 于是 SPA 的动态 import 会去面板自己的根取 chunk → 404 → 整页白屏（用户实测就是这个现象）。
// 幂等：已带前缀的串先换成占位符，替换完再还原，重复调用不会叠加前缀。
func (p *LoginProxy) prefixRootRelative(body string) string {
	for _, quote := range []string{`"`, `'`, "`"} {
		body = strings.ReplaceAll(body, quote+"/assets/", quote+p.prefix+"/assets/")
	}
	return body
}

// prefixSlashConcat 处理 `"/"+dep` 这类**运行时拼接**的根前缀。
//
// Vite 默认 base="/" 时，bundle 里会留下 "/"+"assets/xxx.js" 这种拼法；浏览器把它解析到 origin 根，
// 反代场景下就是面板自己的根 → 404（实测页面能出壳、但动态 chunk 全 404，白屏就是这么来的）。
// 幂等：已带前缀的拼接先换成占位符，替换完再还原，重复调用不会叠加。
func (p *LoginProxy) prefixSlashConcat(body string) string {
	for _, quote := range []string{`"`, `'`, "`"} {
		body = strings.ReplaceAll(body, quote+"/"+quote+"+", quote+p.prefix+"/"+quote+"+")
	}
	return body
}

func (p *LoginProxy) rewriteHTML(raw []byte) []byte {
	body := string(raw)
	for _, pattern := range p.rewrite {
		body = pattern.ReplaceAllString(body, `$1="`+p.prefix+`/$2"`)
	}
	// 内联脚本里的根相对字面量（Vite 的 modulepreload / 动态 import）要拉回前缀下：
	// 以单个 / 开头的 URL **不受 <base> 影响**（只按 origin 解析），不去改就会打到面板自己的根 → 404 → 白屏。
	body = p.prefixRootRelative(body)
	for _, origin := range []string{"https://" + p.target.Host, "http://" + p.target.Host} {
		body = strings.ReplaceAll(body, origin, p.prefix)
	}
	// 注入 <base>：SPA 构建产物里还有**根相对**的引用（Vite 的动态 import /assets/chunk-x.js、
	// CSS 里的绝对路径等），这些不受属性改写影响，在浏览器里会解析到面板自己的根 → 404 → 白屏。
	// 一个 base 标签就能让它们全部落回反代前缀下（实测白屏就是这个原因）。
	body = p.injectBase(body)
	body = p.injectOriginShim(body)
	var buffer bytes.Buffer
	buffer.WriteString(body)
	return buffer.Bytes()
}

// injectBase 在 <head> 之后插入 <base href="<prefix>/">（幂等：已有 base 就不动）。
func (p *LoginProxy) injectBase(body string) string {
	lowered := strings.ToLower(body)
	if strings.Contains(lowered, "<base") {
		return body
	}
	index := strings.Index(lowered, "<head")
	if index < 0 {
		return body
	}
	closing := strings.Index(lowered[index:], ">")
	if closing < 0 {
		return body
	}
	at := index + closing + 1
	return body[:at] + `<base href="` + p.prefix + `/">` + body[at:]
}

func copyProxyHeaders(dst, src http.Header) {
	for key, values := range src {
		if hopByHop[strings.ToLower(key)] {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func writeProxyError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte("<!doctype html><meta charset=\"utf-8\"><p style=\"font:14px/1.6 system-ui\">登录代理错误：" + message + "</p>"))
}

// authCookieName 是 octopus 自己的管理会话 Cookie 名（internal/server/middleware/auth.go 里的 "auth"）。
// 代理转发时只剥这一个，绝不"整包丢掉 Cookie" —— 那会把目标站点自己的 Cookie 一起丢掉。
const authCookieName = "auth"

func dropAuthCookie(raw string) string {
	parts := strings.Split(raw, ";")
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		name := trimmed
		if index := strings.Index(trimmed, "="); index >= 0 {
			name = trimmed[:index]
		}
		if strings.EqualFold(strings.TrimSpace(name), authCookieName) {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, "; ")
}

// injectOriginShim 往页面里注入一段小脚本，把**指向站点 origin 的请求**改道到反代前缀。
//
// 为什么必须做：SPA 的接口地址常常是运行时拼的（"https://" + host + "/api/..."），
// JS 里没有可改写的字面量，浏览器就会**直连站点**——登录响应不流经代理，
// 令牌自然捞不到，用户看到的是"明明登录成功，采集却缺 token"（实测 53HK 就是这个）。
// 这段 shim 在应用脚本之前执行，把 fetch / XHR 的目标改到前缀下，登录与刷新就都会走代理。
func (p *LoginProxy) injectOriginShim(body string) string {
	lowered := strings.ToLower(body)
	index := strings.Index(lowered, "<head")
	if index < 0 {
		return body
	}
	closing := strings.Index(lowered[index:], ">")
	if closing < 0 {
		return body
	}
	at := index + closing + 1
	script := `<script>(function(){var O="https://` + p.target.Host + `",P="` + p.prefix + `";` +
		`function f(u){try{var s=String(u);if(s.indexOf(O)===0)return P+s.slice(O.length);}catch(e){}return u;}` +
		`var of=window.fetch;if(of){window.fetch=function(i,n){try{if(typeof i==="string"){i=f(i);}` +
		`else if(i&&i.url){var nu=f(i.url);if(nu!==i.url){i=new Request(nu,i);}}}catch(e){}return of.call(this,i,n);};}` +
		`var oo=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(m,u){try{u=f(u);}catch(e){}return oo.call(this,m,u);};` +
		// 兜底：令牌可能既不在响应体也不在 Cookie 里，而是 SPA 自己写进 localStorage 的。
		// 这里每隔一会儿扫一次 localStorage，发现形如令牌的键值就上报给代理（去重，不重复打扰）。
		`function rp(){try{var o={},i,k,v;for(i=0;i<localStorage.length;i++){k=localStorage.key(i);v=localStorage.getItem(k)||"";` +
		`if(/token/i.test(k)&&v.length>10&&v.length<4000)o[k]=v;}var ks=Object.keys(o);if(!ks.length)return;` +
		`var sig=JSON.stringify(o);if(window.__ocSig===sig)return;window.__ocSig=sig;` +
		`var of2=window.fetch;if(of2)of2.call(window,P+"/__octopus/capture",{method:"POST",headers:{"Content-Type":"application/json"},body:sig});}catch(e){}}` +
		`setInterval(rp,1500);setTimeout(rp,800);` +
		`})();</script>`
	return body[:at] + script + body[at:]
}

// capturePath 是页面 shim 上报令牌的固定路径（不会与站点路由冲突：它挂在反代前缀下）。
const capturePath = "/__octopus/capture"

// handleCapture 接收页面 shim 上报的键值，把其中的令牌写进会话变量。
func (p *LoginProxy) handleCapture(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	raw, _ := io.ReadAll(io.LimitReader(r.Body, proxyBodyLimit))
	var payload map[string]string
	if err := json.Unmarshal(raw, &payload); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	values := map[string]string{}
	for key, value := range payload {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" || len(trimmed) > 8192 {
			continue
		}
		values[key] = trimmed
		lowered := strings.ToLower(key)
		// 令牌类键名统一映射到 VarToken，采集步骤就能用 {token} 引用。
		if strings.Contains(lowered, "access_token") || strings.Contains(lowered, "accesstoken") || lowered == "token" {
			values[VarToken] = trimmed
		}
		if strings.Contains(lowered, "refresh") {
			values["refresh_token"] = trimmed
		}
	}
	if len(values) > 0 {
		p.session.SetVars(values)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// collapsePrefix 剥掉路径里**任意位置**出现的前缀（可能不止一次）。
//
// 实测形状（nginx 日志原文）：`<前缀>/api/v1<前缀>/auth/login` —— SPA 把 <base> 前缀与运行时
// 拼出来的绝对地址叠在一起了。只剥开头那一次不够：中间那一次会被当成站点自己的路径转发过去，
// 站点回 404，用户看到的就是"点登录报 404"。前缀里带一次性随机令牌，正常业务路径不会包含它。
func (p *LoginProxy) collapsePrefix(subPath string) string {
	for strings.Contains(subPath, p.prefix) {
		subPath = strings.ReplaceAll(subPath, p.prefix, "")
	}
	return subPath
}

// routerBaseRe 匹配 SPA 路由的 history 初始化：`history:Ua("/")` / `history:createWebHistory("/")`。
var routerBaseRe = regexp.MustCompile(`history:(\w+)\("/"\)`)

// ProxyClient 按主出口同样的配置建一个 HTTP 客户端（供备用出口使用）。
func ProxyClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		return nil, fmt.Errorf("出口为空")
	}
	return &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:               proxyFuncFor(proxyURL),
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 15 * time.Second,
		},
	}, nil
}
