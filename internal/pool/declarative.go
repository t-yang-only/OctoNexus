package pool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tidwall/gjson"
)

// 声明式适配器（R-pool-ext-001 第三批，用户对"四个边界"回复「我全都要」后落地）。
//
// 目标：不改代码、不重新编译，用一份 JSON 描述就能把一个外部反代工具包接进号池视图。
// 它同时是最危险的一个扩展点——等于"让 octopus 按 JSON 去请求任意 URL"，因此本文件把用户
// 拍板的边界**全部实现成硬约束**，而不是写在文档里：
//
//  1. 域名白名单：`pool_declarative_hosts` 设置项，**默认为空 = 一律拒绝**（fail closed）。
//     spec 自己不能带白名单，也不能绕过它——解析出的目标 host 必须命中白名单后缀。
//  2. 只读能力：声明式 spec 只允许 list/get。toggle/provision/revoke/sync/refresh/probe 一律
//     拒绝注册（它们要么改外部系统、要么需要按条目拼 URL，超出"只读清单"的范围）。
//     想接只读之外的能力 → 写内置适配器（那要过代码评审）。
//  3. 不跟跳转：3xx 一律当失败。否则白名单可以被一个 302 绕过。
//  4. 有界请求：超时 + 响应体上限（默认 10s / 1MiB），避免一个坏后端把号池页拖死。
//  5. 凭据永不回显：spec 里的 token/password 只进加密存储与内存，接口只回 `Secret{}`。
//
// 与既有口径一致：Entry 只带元数据，凭据不出这个包。

// DeclarativeSecret 是声明式适配器的凭据：**只在提交时出现**，落库加密，任何接口都不回显。
type DeclarativeSecret struct {
	Token    string `json:"token,omitempty"`    // 静态令牌（auth.type=bearer 时用）。
	Username string `json:"username,omitempty"` // 登录用户名（auth.type=login 时用）。
	Password string `json:"password,omitempty"` // 登录密码（同上）。
}

// DeclarativeAuth 是取值方式。三种：
//   - none：不加认证头（只适合本机/内网只读端点）。
//   - bearer：把 secret.token 放进 Authorization: Bearer <token>（header 可换成别的头名）。
//   - login：先 POST login_url 换 token（body 里 {{username}}/{{password}} 会被替换），
//     从 token_path 取出令牌，之后按 bearer 使用；遇到 401 会重新登录一次。
type DeclarativeAuth struct {
	Type      string            `json:"type"`
	Header    string            `json:"header,omitempty"`
	LoginURL  string            `json:"login_url,omitempty"`
	LoginBody map[string]string `json:"login_body,omitempty"`
	TokenPath string            `json:"token_path,omitempty"`
}

// DeclarativeList 描述"列出条目"这一次只读调用。
type DeclarativeList struct {
	Path      string            `json:"path"`       // 相对 base_url 的路径（也接受同白名单内的绝对 URL）。
	ItemsPath string            `json:"items_path"` // 条目数组的 gjson 路径，如 "data"；留空表示响应本身就是数组。
	Fields    map[string]string `json:"fields"`     // Entry 字段 → 该条目内的 gjson 路径。
}

// DeclarativeSpec 是一份完整的声明式适配器描述。
type DeclarativeSpec struct {
	Kind         string            `json:"kind"`
	Title        string            `json:"title"`
	BaseURL      string            `json:"base_url"`
	Capabilities []Capability      `json:"capabilities"`
	Auth         DeclarativeAuth   `json:"auth"`
	List         DeclarativeList   `json:"list"`
	Secret       DeclarativeSecret `json:"secret,omitempty"`
}

// declarativeCapabilities 是声明式 spec 允许声明的能力位：只读清单。
var declarativeCapabilities = map[Capability]bool{CapList: true, CapGet: true}

// DeclarativeGuards 是声明式适配器的运行时守卫，由装配层注入（读设置项），spec 不得自带。
// AllowedHosts 为空表示**全部拒绝**：宁可让用户显式列出域名，也不要默认敞着。
type DeclarativeGuards struct {
	AllowedHosts []string
	Timeout      time.Duration
	MaxBodyBytes int64
}

const (
	defaultDeclarativeTimeout      = 10 * time.Second
	defaultDeclarativeMaxBodyBytes = 1 << 20 // 1MiB：号池视图是元数据，不需要大响应。
)

var (
	declarativeGuardsMu sync.RWMutex
	declarativeGuards   = DeclarativeGuards{}
)

// SetDeclarativeGuards 由装配层在启动与设置变更时调用。
func SetDeclarativeGuards(guards DeclarativeGuards) {
	if guards.Timeout <= 0 {
		guards.Timeout = defaultDeclarativeTimeout
	}
	if guards.MaxBodyBytes <= 0 {
		guards.MaxBodyBytes = defaultDeclarativeMaxBodyBytes
	}
	normalized := make([]string, 0, len(guards.AllowedHosts))
	for _, host := range guards.AllowedHosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if host != "" {
			normalized = append(normalized, host)
		}
	}
	guards.AllowedHosts = normalized
	declarativeGuardsMu.Lock()
	declarativeGuards = guards
	declarativeGuardsMu.Unlock()
}

// DeclarativeGuardsOf 返回当前守卫（副本）。
func DeclarativeGuardsOf() DeclarativeGuards {
	declarativeGuardsMu.RLock()
	defer declarativeGuardsMu.RUnlock()
	guards := declarativeGuards
	guards.AllowedHosts = append([]string(nil), declarativeGuards.AllowedHosts...)
	if guards.Timeout <= 0 {
		guards.Timeout = defaultDeclarativeTimeout
	}
	if guards.MaxBodyBytes <= 0 {
		guards.MaxBodyBytes = defaultDeclarativeMaxBodyBytes
	}
	return guards
}

// ParseDeclarativeHosts 解析设置项里的域名白名单：逗号分隔，支持后缀（`example.com` 命中
// `api.example.com`），也接受写 `*.example.com`（星号只是可读性，语义与后缀一致）。
func ParseDeclarativeHosts(raw string) []string {
	hosts := make([]string, 0)
	for _, part := range strings.Split(raw, ",") {
		host := strings.ToLower(strings.TrimSpace(part))
		host = strings.TrimPrefix(host, "*.")
		host = strings.TrimPrefix(host, ".")
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// hostAllowed 判断 host 是否命中白名单后缀。白名单为空即拒绝（fail closed）。
func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return false
	}
	for _, entry := range allowed {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if entry == "" {
			continue
		}
		if host == entry || strings.HasSuffix(host, "."+entry) {
			return true
		}
	}
	return false
}

// ValidateDeclarativeSpec 校验一份 spec 是否可注册：形状、能力位、URL 与白名单。
// 校验失败的错误都会说明**为什么**，因为这是运维要照着改的输入。
func ValidateDeclarativeSpec(spec DeclarativeSpec) error {
	if strings.TrimSpace(spec.Kind) == "" {
		return fmt.Errorf("%w: kind 不能为空", ErrInvalidAdapter)
	}
	if strings.TrimSpace(spec.Title) == "" {
		return fmt.Errorf("%w: title 不能为空", ErrInvalidAdapter)
	}
	if !strings.HasPrefix(spec.Kind, "custom-") {
		// 前缀要求：内置 kind 一眼可分，也避免声明式适配器蹭内置名字。
		return fmt.Errorf("%w: 声明式适配器的 kind 必须以 custom- 开头（避免与内置 kind 混淆）", ErrInvalidAdapter)
	}
	base, err := url.Parse(strings.TrimSpace(spec.BaseURL))
	if err != nil || base.Host == "" {
		return fmt.Errorf("%w: base_url 必须是绝对 URL", ErrInvalidAdapter)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return fmt.Errorf("%w: base_url 只支持 http/https", ErrInvalidAdapter)
	}
	guards := DeclarativeGuardsOf()
	if len(guards.AllowedHosts) == 0 {
		return fmt.Errorf("%w: 未配置域名白名单（设置项 pool_declarative_hosts），声明式适配器一律拒绝注册", ErrHostNotAllowed)
	}
	if !hostAllowed(base.Hostname(), guards.AllowedHosts) {
		return fmt.Errorf("%w: base_url 的主机 %q 不在白名单 %v 内", ErrHostNotAllowed, base.Hostname(), guards.AllowedHosts)
	}
	if len(spec.Capabilities) == 0 {
		return fmt.Errorf("%w: 至少要声明 %q 能力", ErrInvalidAdapter, CapList)
	}
	for _, capability := range spec.Capabilities {
		if !allCapabilities[capability] {
			return fmt.Errorf("%w: 未知能力位 %q", ErrInvalidAdapter, capability)
		}
		if !declarativeCapabilities[capability] {
			return fmt.Errorf("%w: 声明式适配器只支持只读能力（list/get），%q 需要写内置适配器",
				ErrInvalidAdapter, capability)
		}
	}
	if strings.TrimSpace(spec.List.Path) == "" {
		return fmt.Errorf("%w: list.path 不能为空", ErrInvalidAdapter)
	}
	target, err := resolveDeclarativeURL(base, spec.List.Path)
	if err != nil {
		return err
	}
	if !hostAllowed(target.Hostname(), guards.AllowedHosts) {
		return fmt.Errorf("%w: list.path 解析出的主机 %q 不在白名单内", ErrHostNotAllowed, target.Hostname())
	}
	switch spec.Auth.Type {
	case "", "none":
	case "bearer":
		if strings.TrimSpace(spec.Secret.Token) == "" {
			return fmt.Errorf("%w: auth.type=bearer 必须提供 secret.token", ErrInvalidAdapter)
		}
	case "login":
		loginURL := strings.TrimSpace(spec.Auth.LoginURL)
		if loginURL == "" {
			return fmt.Errorf("%w: auth.type=login 必须提供 login_url", ErrInvalidAdapter)
		}
		loginTarget, err := resolveDeclarativeURL(base, loginURL)
		if err != nil {
			return err
		}
		if !hostAllowed(loginTarget.Hostname(), guards.AllowedHosts) {
			return fmt.Errorf("%w: login_url 解析出的主机 %q 不在白名单内", ErrHostNotAllowed, loginTarget.Hostname())
		}
		if strings.TrimSpace(spec.Auth.TokenPath) == "" {
			return fmt.Errorf("%w: auth.type=login 必须提供 token_path", ErrInvalidAdapter)
		}
		if strings.TrimSpace(spec.Secret.Username) == "" || spec.Secret.Password == "" {
			return fmt.Errorf("%w: auth.type=login 必须提供 secret.username/password", ErrInvalidAdapter)
		}
	default:
		return fmt.Errorf("%w: 不支持的 auth.type %q（none/bearer/login）", ErrInvalidAdapter, spec.Auth.Type)
	}
	return nil
}

// resolveDeclarativeURL 把 spec 里的路径解析成绝对 URL：相对路径接在 base 后面，
// 绝对 URL 必须自带 scheme 且与 base 同源策略一并交给白名单判断。
func resolveDeclarativeURL(base *url.URL, path string) (*url.URL, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return base, nil
	}
	parsed, err := url.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("%w: 路径无法解析: %v", ErrInvalidAdapter, err)
	}
	if parsed.IsAbs() {
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("%w: 只支持 http/https", ErrInvalidAdapter)
		}
		return parsed, nil
	}
	joined := *base
	joined.Path = strings.TrimSuffix(base.Path, "/") + "/" + strings.TrimPrefix(parsed.Path, "/")
	joined.RawQuery = parsed.RawQuery
	return &joined, nil
}

// ErrHostNotAllowed 表示目标主机不在声明式适配器白名单内（或白名单为空）。
var ErrHostNotAllowed = errors.New("pool declarative host not allowed")

// declarativeAdapter 把一份 spec 实现成 Adapter。令牌只在内存里，登录按需发生。
type declarativeAdapter struct {
	spec  DeclarativeSpec
	base  *url.URL
	http  *http.Client
	token string // 内存里的令牌（bearer 直接来自 spec；login 来自登录响应）。

	mu sync.Mutex // 保护 token 与登录过程。
}

// NewDeclarativeAdapter 校验并构造一个声明式适配器（不注册）。
func NewDeclarativeAdapter(spec DeclarativeSpec) (Adapter, error) {
	if err := ValidateDeclarativeSpec(spec); err != nil {
		return nil, err
	}
	base, err := url.Parse(strings.TrimSpace(spec.BaseURL))
	if err != nil {
		return nil, fmt.Errorf("%w: base_url 无法解析", ErrInvalidAdapter)
	}
	guards := DeclarativeGuardsOf()
	// 不跟跳转：否则白名单可以用一个 302 绕过去。
	client := &http.Client{
		Timeout: guards.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return &declarativeAdapter{spec: spec, base: base, http: client, token: spec.Secret.Token}, nil
}

// Info 实现 Adapter：自描述里**不含任何凭据**，字段清单由 fields 映射推导。
func (adapter *declarativeAdapter) Info() AdapterInfo {
	spec := adapter.spec
	fields := make([]FieldSpec, 0, len(spec.List.Fields))
	names := make([]string, 0, len(spec.List.Fields))
	for name := range spec.List.Fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fields = append(fields, FieldSpec{Name: name, Type: "string", Label: name})
	}
	capabilities := append([]Capability(nil), spec.Capabilities...)
	sort.Slice(capabilities, func(i, j int) bool { return capabilities[i] < capabilities[j] })
	return AdapterInfo{
		Kind:         spec.Kind,
		Title:        spec.Title,
		Capabilities: capabilities,
		Fields:       fields,
		Builtin:      false,
		Since:        "declarative",
	}
}

// Entries 实现 Adapter：一次只读 GET，按 items_path + fields 映射成统一视图。
func (adapter *declarativeAdapter) Entries(ctx context.Context) ([]Entry, error) {
	target, err := resolveDeclarativeURL(adapter.base, adapter.spec.List.Path)
	if err != nil {
		return nil, err
	}
	body, err := adapter.get(ctx, target, true)
	if err != nil {
		return nil, err
	}
	items := gjson.ParseBytes(body)
	if path := strings.TrimSpace(adapter.spec.List.ItemsPath); path != "" {
		items = items.Get(path)
	}
	if !items.IsArray() {
		return nil, fmt.Errorf("声明式适配器 %s: items_path=%q 没有指向数组",
			adapter.spec.Kind, adapter.spec.List.ItemsPath)
	}
	entries := make([]Entry, 0, len(items.Array()))
	for index, item := range items.Array() {
		entry, err := adapter.entry(item, index)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

// entry 把一个条目按 fields 映射成 Entry。id 缺失时用序号兜底（视图仍可用，但状态会说明）。
func (adapter *declarativeAdapter) entry(item gjson.Result, index int) (Entry, error) {
	pick := func(field string) string {
		path := strings.TrimSpace(adapter.spec.List.Fields[field])
		if path == "" {
			return ""
		}
		return strings.TrimSpace(item.Get(path).String())
	}
	id := pick("id")
	if id == "" {
		id = fmt.Sprintf("#%d", index+1)
	}
	entry := Entry{
		Kind:     adapter.spec.Kind,
		ID:       id,
		Name:     pick("name"),
		Provider: pick("provider"),
		Enabled:  true,
		Healthy:  true,
		PlanTier: pick("plan_tier"),
		Detail:   map[string]any{"source": "declarative"},
	}
	if entry.Name == "" {
		entry.Name = id
	}
	if status := pick("status"); status != "" {
		entry.Status = status
	} else if entry.Enabled {
		entry.Status = "enabled"
	}
	if raw := pick("enabled"); raw != "" {
		entry.Enabled = raw == "true" || raw == "1" || strings.EqualFold(raw, "enabled")
	}
	if raw := pick("healthy"); raw != "" {
		entry.Healthy = raw == "true" || raw == "1" || strings.EqualFold(raw, "healthy")
	}
	if raw := pick("expires_at"); raw != "" {
		if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
			entry.ExpiresAt = &parsed
		}
	}
	if raw := pick("last_error"); raw != "" {
		entry.LastError = raw
	}
	return entry, nil
}

// get 发一次只读请求；allowRelogin 控制 401 之后是否可以重新登录一次（避免无限递归）。
func (adapter *declarativeAdapter) get(ctx context.Context, target *url.URL, allowRelogin bool) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if token := adapter.currentToken(); token != "" {
		header := strings.TrimSpace(adapter.spec.Auth.Header)
		if header == "" {
			header = "Authorization"
		}
		if strings.EqualFold(header, "Authorization") {
			request.Header.Set("Authorization", "Bearer "+token)
		} else {
			request.Header.Set(header, token)
		}
	}
	response, err := adapter.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusUnauthorized && allowRelogin && adapter.spec.Auth.Type == "login" {
		if err := adapter.login(ctx); err != nil {
			return nil, err
		}
		return adapter.get(ctx, target, false)
	}
	guards := DeclarativeGuardsOf()
	body, err := io.ReadAll(io.LimitReader(response.Body, guards.MaxBodyBytes))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("声明式适配器 %s: 上游返回 %s", adapter.spec.Kind, response.Status)
	}
	return body, nil
}

// currentToken 读内存令牌（登录与首次构造都会写它）。
func (adapter *declarativeAdapter) currentToken() string {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	return adapter.token
}

// login 用 secret 里的用户名/密码换一次令牌，只保存在内存里。
func (adapter *declarativeAdapter) login(ctx context.Context) error {
	loginURL, err := resolveDeclarativeURL(adapter.base, adapter.spec.Auth.LoginURL)
	if err != nil {
		return err
	}
	payload := make(map[string]string, len(adapter.spec.Auth.LoginBody))
	for key, value := range adapter.spec.Auth.LoginBody {
		value = strings.ReplaceAll(value, "{{username}}", adapter.spec.Secret.Username)
		value = strings.ReplaceAll(value, "{{password}}", adapter.spec.Secret.Password)
		payload[key] = value
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, loginURL.String(), strings.NewReader(string(encoded)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := adapter.http.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	guards := DeclarativeGuardsOf()
	body, err := io.ReadAll(io.LimitReader(response.Body, guards.MaxBodyBytes))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return fmt.Errorf("声明式适配器 %s: 登录返回 %s", adapter.spec.Kind, response.Status)
	}
	token := strings.TrimSpace(gjson.GetBytes(body, adapter.spec.Auth.TokenPath).String())
	if token == "" {
		return fmt.Errorf("声明式适配器 %s: 登录响应里 token_path=%q 取不到令牌",
			adapter.spec.Kind, adapter.spec.Auth.TokenPath)
	}
	adapter.mu.Lock()
	adapter.token = token
	adapter.mu.Unlock()
	return nil
}
