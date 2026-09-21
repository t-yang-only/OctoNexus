package collector

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// 失败原因码：与余额扫描（internal/health）保持同一套字符串，面板归组统计才能合并显示。
const (
	ReasonUnauthorized = "unauthorized"
	ReasonUnreachable  = "unreachable"
	ReasonEndpointGone = "no_endpoint"
	ReasonUnparsable   = "unparsable"
)

const (
	requestTimeout = 25 * time.Second
	maxBodyBytes   = 2 << 20
	maxRedirects   = 5
)

// Credentials 是"人给的凭据"（登录名/密码/验证码），不落盘、只在内存里用于一次登录。
type Credentials struct {
	Username string
	Password string
	Captcha  string
	OTP      string
}

// CookieState 是导出会话里的一条 Cookie。
type CookieState struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain,omitempty"`
	Path   string `json:"path,omitempty"`
}

// SessionState 是会话的可落盘形态（进库前由调用方整体加密，绝不明文落盘）。
type SessionState struct {
	Cookies  []CookieState     `json:"cookies,omitempty"`
	Vars     map[string]string `json:"vars,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
	Username string            `json:"username,omitempty"`
	At       time.Time         `json:"at"`
}

// Session 是一次采集的会话：Cookie 罐 + 变量（token 等）+ 会话级请求头。
type Session struct {
	mu      sync.Mutex
	jar     *recordingJar
	vars    map[string]string
	headers map[string]string
	user    string
}

// NewSession 建一个空会话（交互登录与包登录共用同一形状）。
func NewSession() *Session {
	return &Session{jar: newRecordingJar(), vars: map[string]string{}, headers: map[string]string{}}
}

// SessionFromState 从落盘状态恢复会话。
func SessionFromState(state SessionState) (*Session, error) {
	session := NewSession()
	for _, cookie := range state.Cookies {
		host := cookie.Domain
		if host == "" {
			return nil, fmt.Errorf("会话 Cookie %s 缺少 Domain", cookie.Name)
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		plain := host
		if !strings.Contains(plain, "://") {
			plain = "https://" + plain
		}
		parsed, err := url.Parse(plain)
		if err != nil {
			return nil, fmt.Errorf("会话 Cookie %s 的 Domain 不合法：%v", cookie.Name, err)
		}
		session.jar.SetCookies(parsed, []*http.Cookie{{Name: cookie.Name, Value: cookie.Value, Path: path}})
	}
	for key, value := range state.Vars {
		session.vars[key] = value
	}
	for key, value := range state.Headers {
		session.headers[key] = value
	}
	session.user = state.Username
	return session, nil
}

// Export 导出会话（供加密落盘）。
func (s *Session) Export() SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := SessionState{Vars: map[string]string{}, Headers: map[string]string{}, Username: s.user, At: time.Now()}
	state.Cookies = s.jar.snapshot()
	for key, value := range s.vars {
		state.Vars[key] = value
	}
	for key, value := range s.headers {
		state.Headers[key] = value
	}
	return state
}

// recordingJar 是"会记账"的 Cookie 罐。
//
// 为什么必须自己记账：Go 标准库的罐只在 Cookies(u) 时按 u 的域返回匹配的 Cookie，
// 没有"把罐里所有 Cookie 倒出来"的接口；导出会话时若拿一个探针域去问，永远得到空集 ——
// 表现就是"登录明明成功了，却被判成没有会话"。这里在 SetCookies 时顺手把值抄一份，
// 于是"导出"与"发请求"用的是同一批 Cookie，不会出现两套事实。
type recordingJar struct {
	base  http.CookieJar
	mu    sync.Mutex
	store map[string]CookieState // key = domain|path|name
}

func newRecordingJar() *recordingJar {
	base, _ := cookiejar.New(nil)
	return &recordingJar{base: base, store: map[string]CookieState{}}
}

func (j *recordingJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.base.SetCookies(u, cookies)
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, cookie := range cookies {
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		// Domain 记"请求打到哪个主机"，与罐的匹配口径一致；恢复时用它重建能送出去的 Cookie。
		entry := CookieState{Name: cookie.Name, Value: cookie.Value, Domain: u.Hostname(), Path: path}
		j.store[entry.Domain+"|"+path+"|"+cookie.Name] = entry
	}
}

func (j *recordingJar) Cookies(u *url.URL) []*http.Cookie { return j.base.Cookies(u) }

func (j *recordingJar) snapshot() []CookieState {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]CookieState, 0, len(j.store))
	for _, entry := range j.store {
		out = append(out, entry)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Domain != out[b].Domain {
			return out[a].Domain < out[b].Domain
		}
		return out[a].Name < out[b].Name
	})
	return out
}

// SetVars 写入会话变量（如登录时提取到的 token）。
func (s *Session) SetVars(values map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, value := range values {
		s.vars[key] = value
	}
}

// Vars 返回会话变量快照。
func (s *Session) Vars() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.vars))
	for key, value := range s.vars {
		out[key] = value
	}
	return out
}

// CookieCount 返回会话里的 Cookie 数量（用于状态展示，不回显内容）。
func (s *Session) CookieCount() int {
	return len(s.Export().Cookies)
}

// Result 是一次采集的结果。读到的"账"与"变量"分开：账进余额管线，变量留在会话里。
type Result struct {
	HasBalance bool
	Balance    float64
	HasUsed    bool
	Used       float64
	HasQuota   bool
	Quota      float64
	Currency   string
	Username   string
	Note       string
	Units      string
	Vars       map[string]string
	Session    SessionState
	Steps      []string
	ReasonKey  string
	ReasonText string
}

// OK 表示本次采集至少拿到了余额或总额度之一。
func (r Result) OK() bool { return r.HasBalance || r.HasQuota }

// Run 执行一次采集：先登录（form 模式）再逐步读取。
// interactive 模式跳过登录，直接用会话里已有的 Cookie —— 这正是"人在页面里登录一次、之后长期自动采集"。
func Run(ctx context.Context, pack Pack, session *Session, creds Credentials, site string) Result {
	return RunVia(ctx, pack, session, creds, site, "")
}

// RunVia 是指定出口的一次采集：proxyURL 非空时登录与读账都从该出口出去。
func RunVia(ctx context.Context, pack Pack, session *Session, creds Credentials, site, proxyURL string) Result {
	result := Result{Units: pack.Units, Vars: map[string]string{}}
	if result.Units == "" {
		result.Units = UnitsUSD
	}
	client := NewHTTPClientVia(pack.Hosts, proxyURL)
	// 会话 Cookie 必须挂到请求客户端上：交互登录（人在页面里登录）拿到的就是 Cookie，
	// 不挂的话读取请求会一路裸奔 —— 站点当然回 401，而用户看到的是"明明登录了却说没登录"。
	client.Jar = session.jar
	vars := map[string]string{"site": site, "username": creds.Username, "password": creds.Password, "captcha": creds.Captcha, "otp": creds.OTP}
	for key, value := range session.Vars() {
		vars[key] = value
	}
	mergeVars := func(extracted map[string]string) {
		for key, value := range extracted {
			vars[key] = value
			result.Vars[key] = value
		}
	}

	if pack.Login != nil && pack.Login.Mode == LoginModeForm {
		// 会话优先（R-collector-010）：交互登录（人在反代页面里登录一次）拿到的令牌直接复用。
		// 没有账号密码、而会话里已经有令牌时**绝不再跑 form 登录**——跑了必然被上游拒（实测
		// 空凭据 → 400 "LoginRequest.Email required"），用户看到的就是"明明登录成功却说采集失败"。
		if strings.TrimSpace(creds.Username) == "" && strings.TrimSpace(creds.Password) == "" {
			result.Steps = append(result.Steps, "复用交互登录会话（跳过 form 登录）")
		} else {
			extracted, step, reasonKey, reasonText := runLogin(ctx, client, session, *pack.Login, vars)
			result.Steps = append(result.Steps, step)
			mergeVars(extracted)
			if reasonKey != "" {
				result.ReasonKey, result.ReasonText = reasonKey, reasonText
				result.Session = session.Export()
				return result
			}
		}
	}

	for _, step := range pack.Read {
		body, reasonKey, reasonText, trace := runStep(ctx, client, session, step, vars)
		// 会话过期自动续期：读账 401 且会话里有 refresh_token 时，先换新令牌再重试一次。
		// 续期接口**不需要验证码**（两家实测都是 POST /api/v1/auth/refresh），
		// 这一环才是"人工过一次验证码、之后长期自动"的最后一公里。
		if body != nil && body.status == http.StatusUnauthorized && refreshSessionToken(ctx, client, site, session, vars) {
			body, reasonKey, reasonText, trace = runStep(ctx, client, session, step, vars)
		}
		extracted := map[string]string{}
		if body != nil {
			var extractErr error
			extracted, extractErr = extractFields(step.Extract, body.status, body.header, body.payload, body.raw)
			if extractErr != nil {
				result.Steps = append(result.Steps, trace)
				result.ReasonKey, result.ReasonText = ReasonUnparsable, extractErr.Error()
				result.Session = session.Export()
				return result
			}
		}
		result.Steps = append(result.Steps, trace)
		mergeVars(extracted)
		if reasonKey != "" {
			result.ReasonKey, result.ReasonText = reasonKey, reasonText
			result.Session = session.Export()
			return result
		}
	}

	applyFields(&result, result.Vars)
	session.SetVars(result.Vars)
	if !result.OK() {
		result.ReasonKey = ReasonUnparsable
		result.ReasonText = "?????????????????????????????????????"
	}
	result.Session = session.Export()
	return result
}

// applyFields 把提取到的变量映射成"账"。
func applyFields(result *Result, values map[string]string) {
	if raw, ok := values[FieldBalance]; ok {
		if value, parsed := CoerceNumber(raw); parsed {
			result.Balance, result.HasBalance = value, true
		}
	}
	if raw, ok := values[FieldUsed]; ok {
		if value, parsed := CoerceNumber(raw); parsed {
			result.Used, result.HasUsed = value, true
		}
	}
	if raw, ok := values[FieldQuota]; ok {
		if value, parsed := CoerceNumber(raw); parsed {
			result.Quota, result.HasQuota = value, true
		}
	}
	// 只给总额度不给余额时，用"总额度 − 已用"推出余额；两者都没有才算失败。
	if !result.HasBalance && result.HasQuota {
		used := 0.0
		if result.HasUsed {
			used = result.Used
		}
		result.Balance, result.HasBalance = result.Quota-used, true
	}
	if value, ok := values[FieldCurrency]; ok {
		result.Currency = strings.TrimSpace(value)
	}
	if value, ok := values[FieldUsername]; ok && result.Username == "" {
		result.Username = strings.TrimSpace(value)
	}
	if value, ok := values[FieldNote]; ok {
		result.Note = strings.TrimSpace(value)
	}
}

// runLogin 执行 form 模式登录：POST 凭据 → 从响应里提取变量（token 等）→ 会话拿到 Cookie。
func runLogin(ctx context.Context, client *http.Client, session *Session, login LoginSpec, vars map[string]string) (map[string]string, string, string, string) {
	body, err := RenderTemplate(login.Body, vars)
	if err != nil {
		return nil, "登录：正文模板缺变量", ReasonUnparsable, err.Error()
	}
	headers, err := renderHeaders(login.Headers, vars)
	if err != nil {
		return nil, "登录：请求头模板缺变量", ReasonUnparsable, err.Error()
	}
	response, reasonKey, reasonText, trace := doRequest(ctx, client, session, login.Method, login.URL, headers, login.BodyType, body)
	if response == nil {
		return nil, trace, reasonKey, reasonText
	}
	extracted, extractErr := extractFields(login.Extract, response.status, response.header, response.payload, response.raw)
	if extractErr != nil {
		return nil, trace + "（提取失败）", ReasonUnparsable, extractErr.Error()
	}
	// 登录接口返回 2xx 但没给任何 Cookie/令牌 = 实际上没登进去，必须报错而不是"看起来成功"。
	if len(session.Export().Cookies) == 0 && len(extracted) == 0 {
		return nil, trace, ReasonUnauthorized,
			"登录接口返回成功但没有会话（既无 Cookie 也无提取到令牌）—— 多半是密码/验证码不对，或该站点把会话放在浏览器本地存储里"
	}
	return extracted, trace, "", ""
}

// runStep 执行一次读取请求。
func runStep(ctx context.Context, client *http.Client, session *Session, step ReadStep, vars map[string]string) (*stepResponse, string, string, string) {
	body, err := RenderTemplate(step.Body, vars)
	if err != nil {
		return nil, ReasonUnparsable, err.Error(), step.Name + "：正文模板缺变量"
	}
	headers, err := renderHeaders(step.Headers, vars)
	if err != nil {
		return nil, ReasonUnparsable, err.Error(), step.Name + "：请求头模板缺变量"
	}
	response, reasonKey, reasonText, trace := doRequest(ctx, client, session, step.Method, step.URL, headers, step.BodyType, body)
	if response == nil {
		return nil, reasonKey, reasonText, trace
	}
	return response, "", "", trace
}

type stepResponse struct {
	status  int
	header  http.Header
	payload any
	raw     []byte
}

var hopByHop = map[string]bool{
	"connection": true, "proxy-connection": true, "keep-alive": true, "transfer-encoding": true,
	"te": true, "trailer": true, "upgrade": true, "proxy-authenticate": true, "proxy-authorization": true,
}

func doRequest(ctx context.Context, client *http.Client, session *Session, method, rawURL string, headers map[string]string, bodyType, body string) (*stepResponse, string, string, string) {
	trace := fmt.Sprintf("%s %s", method, safeURL(rawURL))
	var reader io.Reader
	if strings.TrimSpace(body) != "" {
		reader = strings.NewReader(body)
		if bodyType == BodyJSON && headers["Content-Type"] == "" {
			headers = withHeader(headers, "Content-Type", "application/json")
		}
		if bodyType == BodyForm && headers["Content-Type"] == "" {
			headers = withHeader(headers, "Content-Type", "application/x-www-form-urlencoded")
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return nil, ReasonUnparsable, "请求无法构造：" + err.Error(), trace + " → 构造失败"
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	if request.Header.Get("Accept") == "" {
		request.Header.Set("Accept", "application/json, text/plain, */*")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, ReasonUnreachable, "请求失败：" + trim(err.Error()), trace + " → 网络失败"
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	trace = fmt.Sprintf("%s → %d", trace, response.StatusCode)
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return nil, ReasonUnauthorized, fmt.Sprintf("HTTP %d：会话已失效或凭据不对", response.StatusCode), trace
	}
	if response.StatusCode == http.StatusNotFound || response.StatusCode == http.StatusMethodNotAllowed {
		return nil, ReasonEndpointGone, fmt.Sprintf("HTTP %d：该站点没有这个接口", response.StatusCode), trace
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, ReasonUnparsable, fmt.Sprintf("HTTP %d：%s", response.StatusCode, trim(string(raw))), trace
	}
	payload := any(nil)
	if json.Valid(bytes.TrimSpace(raw)) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var decoded any
		if err := decoder.Decode(&decoded); err == nil {
			payload = decoded
		}
	}
	return &stepResponse{status: response.StatusCode, header: response.Header, payload: payload, raw: raw}, "", "", trace
}

func renderHeaders(templates map[string]string, vars map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for key, value := range templates {
		rendered, err := RenderTemplate(value, vars)
		if err != nil {
			return nil, err
		}
		out[key] = rendered
	}
	return out, nil
}

func withHeader(headers map[string]string, key, value string) map[string]string {
	out := map[string]string{}
	for existing, existingValue := range headers {
		out[existing] = existingValue
	}
	if _, ok := out[key]; !ok {
		out[key] = value
	}
	return out
}

// extractFields 按规则把响应里的值取出来（键即变量名）。
func extractFields(rules []ExtractRule, status int, header http.Header, payload any, raw []byte) (map[string]string, error) {
	values := map[string]string{}
	for _, rule := range rules {
		switch rule.From {
		case FromStatus:
			values[rule.Field] = fmt.Sprintf("%d", status)
		case FromHeader:
			value := header.Get(rule.Path)
			if value == "" {
				return nil, fmt.Errorf("响应头 %s 不存在（字段 %s）", rule.Path, rule.Field)
			}
			values[rule.Field] = value
		case FromJSON:
			node, ok := LookupJSON(payload, rule.Path)
			if !ok {
				return nil, fmt.Errorf("响应里没有 %s（字段 %s）", rule.Path, rule.Field)
			}
			if text, ok := CoerceString(node); ok {
				values[rule.Field] = text
			}
		case FromRegex:
			compiled, err := regexp.Compile(rule.Regex)
			if err != nil {
				return nil, err
			}
			match := compiled.FindSubmatch(raw)
			if match == nil {
				return nil, fmt.Errorf("正文匹配不到 %s（字段 %s）", trim(rule.Regex), rule.Field)
			}
			index := rule.Group
			if index == 0 && len(match) > 1 {
				index = 1
			}
			if index >= len(match) {
				return nil, fmt.Errorf("正则分组 %d 不存在（字段 %s）", index, rule.Field)
			}
			values[rule.Field] = string(match[index])
		}
	}
	return values, nil
}

// proxyFuncFor 把「出口代理地址」翻译成传输层的 Proxy 函数：
//   - 非空：所有请求固定走这台出口（登录、读账、反代页面口径一致，不存在"一半直连"的混合出口）；
//   - 空：沿用环境变量代理（既有行为逐字不变）。
//
// 地址解析失败时退回环境变量而不是直连 —— 直连等于把真实 IP 暴露给站点，是这个功能唯一不可接受的失败方式；
// 调用方（op 层）解析节点端口时已经先拒绝过不可用节点，这里只是最后一道防线。
func proxyFuncFor(proxyURL string) func(*http.Request) (*url.URL, error) {
	trimmed := strings.TrimSpace(proxyURL)
	if trimmed == "" {
		return http.ProxyFromEnvironment
	}
	fixed, err := url.Parse(trimmed)
	if err != nil || fixed.Host == "" {
		return http.ProxyFromEnvironment
	}
	return func(*http.Request) (*url.URL, error) { return fixed, nil }
}

// NewHTTPClient 建受限客户端：不跟到采集包未声明的主机、超时、有界重定向。
func NewHTTPClient(hosts []string) *http.Client { return NewHTTPClientVia(hosts, "") }

// NewHTTPClientVia 与 NewHTTPClient 同规则，但可以指定出口代理：
//   - proxyURL 非空：所有请求（含登录与读账）都从该出口发出 —— 这是"不让我的真实 IP 被站点拉黑"的唯一实现方式；
//   - proxyURL 为空：沿用环境变量代理（既有行为，逐字不变）。
//
// 主机白名单与重定向约束在两种情况下都照旧生效：换出口不等于放宽边界。
func NewHTTPClientVia(hosts []string, proxyURL string) *http.Client {
	proxyFn := proxyFuncFor(proxyURL)
	allowed := map[string]bool{}
	for _, host := range hosts {
		allowed[strings.ToLower(host)] = true
	}
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			Proxy:               proxyFn,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout: 15 * time.Second,
		},
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("重定向次数超过 %d", maxRedirects)
			}
			if len(allowed) > 0 && !allowed[strings.ToLower(request.URL.Host)] {
				return fmt.Errorf("重定向到未声明主机 %s 被拒绝", request.URL.Host)
			}
			return nil
		},
	}
}

// safeURL 只保留 scheme+host+path：查询串里可能带令牌，不能进日志/接口出参。
func safeURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "<非法地址>"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

func trim(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > 160 {
		return text[:160] + "…"
	}
	return text
}

// sessionHasToken 判断会话里是否已经有可用的访问令牌（交互登录捞到的）。
func sessionHasToken(vars map[string]string) bool {
	for _, key := range []string{"token", "access_token", "accessToken"} {
		if strings.TrimSpace(vars[key]) != "" {
			return true
		}
	}
	return false
}

// refreshSessionToken 用会话里的 refresh_token 换一个新的访问令牌（并写回会话变量）。
//
// 返回 true 表示"令牌已换新，可以重试原请求"。任何一步不成立都返回 false，让调用方照原样报错，
// 不掩盖真实的 401（比如 refresh_token 也过期了——那种情况只能重新人工登录一次）。
func refreshSessionToken(ctx context.Context, client *http.Client, site string, session *Session, vars map[string]string) bool {
	refresh := strings.TrimSpace(vars["refresh_token"])
	if refresh == "" || site == "" {
		return false
	}
	payload, err := json.Marshal(map[string]string{"refresh_token": refresh})
	if err != nil {
		return false
	}
	endpoint := strings.TrimSuffix(site, "/") + "/api/v1/auth/refresh"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/124.0")
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode >= 400 {
		return false
	}
	var body struct {
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false
	}
	access := body.Data.AccessToken
	if access == "" {
		access = body.AccessToken
	}
	if access == "" {
		return false
	}
	values := map[string]string{VarToken: access}
	vars[VarToken] = access
	nextRefresh := body.Data.RefreshToken
	if nextRefresh == "" {
		nextRefresh = body.RefreshToken
	}
	if nextRefresh != "" {
		values["refresh_token"] = nextRefresh
		vars["refresh_token"] = nextRefresh
	}
	session.SetVars(values)
	return true
}
