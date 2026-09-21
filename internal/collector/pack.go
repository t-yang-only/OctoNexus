// Package collector 让"自己逆向出来的登录包"成为一等公民：
// 一个站点只要能用一份声明式描述说清「怎么登录 + 怎么读余额/用量」，就能被 octopus 持续采集，
// 而不必为每一家写 Go 代码。
//
// 两条获取会话的路径（用户要的"两个办法"）：
//   - LoginMode.Form：包自己带账号密码（可选验证码/OTP），octopus 直接 POST 登录拿会话；
//   - LoginMode.Interactive：octopus 反代目标站点的登录页，用户在页面里自己输入账号/验证码，
//     octopus 在服务端捕获登录后的 Cookie 会话。
//
// 两者产出的会话（Cookie + 头 + 变量）都是同一形状，之后的读取步骤完全共用，
// 因此"人肉登录一次"与"全自动包"在实现上是同一个东西的两种入口。
package collector

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Units 是读数的单位口径：上游有报美元的，也有报 new-api 点数的，读成什么必须由包自己声明。
// VarToken 是「先登录换令牌、再用令牌查账」这条路里令牌的变量名（模板与校验共用一个字面量）。
const VarToken = "token"

const (
	UnitsUSD    = "usd"
	UnitsPoints = "points"
)

// 登录方式。
const (
	LoginModeForm        = "form"        // 包自带凭据，直接 POST
	LoginModeInteractive = "interactive" // 反代登录页，人在页面里登录
)

// 步骤方法与正文类型。
const (
	MethodGet  = "GET"
	MethodPost = "POST"

	BodyJSON = "json"
	BodyForm = "form"
)

// 提取来源。
const (
	FromJSON   = "json"   // 点号路径（data.items.0.balance）
	FromRegex  = "regex"  // 正文正则（可带分组）
	FromHeader = "header" // 响应头
	FromStatus = "status" // 状态码
)

// 读取结果里被识别为"账"的字段名；其余名字一律作为变量留给后续步骤使用（如 token）。
const (
	FieldBalance  = "balance"  // 剩余额度
	FieldUsed     = "used"     // 已用
	FieldQuota    = "quota"    // 总额度
	FieldCurrency = "currency" // 币种（usd / cny / point / 空）
	FieldUsername = "username" // 账号名（展示用）
	FieldNote     = "note"     // 备注（如套餐名、到期时间）
)

// LoginSpec 描述"怎么拿到会话"。
type LoginSpec struct {
	Mode        string            `json:"mode"`                   // form（默认）/ interactive
	URL         string            `json:"url,omitempty"`          // form 模式的登录接口地址
	Method      string            `json:"method,omitempty"`       // 默认 POST
	BodyType    string            `json:"body_type,omitempty"`    // json（默认）/ form
	Body        string            `json:"body,omitempty"`         // 正文模板，支持 {username} {password} {captcha} {otp} {site}
	Headers     map[string]string `json:"headers,omitempty"`      // 额外请求头（可含模板）
	Extract     []ExtractRule     `json:"extract,omitempty"`      // 从登录响应里捞变量（token 等）
	NeedCaptcha bool              `json:"need_captcha,omitempty"` // 面板据此提示"需要人工给验证码"
}

// ReadStep 是一次读取请求（余额/用量往往要两步：先换 token，再查账）。
type ReadStep struct {
	Name    string            `json:"name,omitempty"`
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`  // GET（默认）/ POST
	BodyType string           `json:"body_type,omitempty"`
	Body    string            `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Extract []ExtractRule     `json:"extract,omitempty"`
}

// ExtractRule 从响应里取一个值。field 除了账字段以外都可作为变量供后续步骤引用（如 token）。
type ExtractRule struct {
	Field string `json:"field"`
	From  string `json:"from"`            // json / regex / header / status
	Path  string `json:"path,omitempty"`  // json 路径或响应头名
	Regex string `json:"regex,omitempty"` // from=regex 时使用
	Group int    `json:"group,omitempty"` // 正则捕获组序号（默认 1，0 表示整个匹配）
}

// Pack 是一份完整的采集包。
type Pack struct {
	Name    string     `json:"name"`
	Version string     `json:"version,omitempty"`
	Units   string     `json:"units,omitempty"` // usd（默认）/ points
	Hosts   []string   `json:"hosts"`           // 必填：允许访问的主机（fail closed，绝不跟到未声明主机）
	Login   *LoginSpec `json:"login,omitempty"`
	Read    []ReadStep `json:"read"`
	Notes   string     `json:"notes,omitempty"`
}

var (
	hostPattern      = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?(:[0-9]{1,5})?$`)
	fieldPattern     = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,31}$`)
	placeholderRegex = regexp.MustCompile(`\{([a-z_][a-z0-9_]*)\}`)
)

// 模板里允许的占位符（其余一律报错，避免把拼错的 {passwrod} 静默送成空串）。
//
// token 在两种形态下都可能存在：
//   - form 形态：登录响应体里的访问令牌，由 login.extract 捞出来（new-api / AI Gateway 都这样）；
//   - interactive 形态：反代登录时站点返回的令牌被 LoginProxy 捕获进会话变量（见 captureToken）。
//
// 因此它必须无条件在白名单里 —— 否则"人在页面上登录、采集用令牌读账"这条路在装载门禁就被拒了。
var templateVars = []string{"username", "password", "captcha", "otp", "site", VarToken}

// ParsePack 解析并严格校验一份采集包：任何一条不满足就拒绝装载，不做"猜一半"的宽容解析。
func ParsePack(raw []byte) (Pack, error) {
	var pack Pack
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&pack); err != nil {
		return Pack{}, fmt.Errorf("采集包不是合法 JSON：%v", err)
	}
	if err := pack.Validate(); err != nil {
		return Pack{}, err
	}
	return pack, nil
}

// Validate 校验采集包。fail closed：主机、协议、方法、模板占位符、正则、字段名逐项检查。
func (p *Pack) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("采集包缺少 name")
	}
	switch p.Units {
	case "":
		p.Units = UnitsUSD
	case UnitsUSD, UnitsPoints:
	default:
		return fmt.Errorf("units 必须是 %s 或 %s", UnitsUSD, UnitsPoints)
	}
	if len(p.Hosts) == 0 {
		return fmt.Errorf("采集包必须声明 hosts（允许访问的主机），否则拒绝装载")
	}
	for i, host := range p.Hosts {
		host = strings.ToLower(strings.TrimSpace(host))
		if !hostPattern.MatchString(host) {
			return fmt.Errorf("hosts[%d] 不是合法主机名：%s", i, host)
		}
		p.Hosts[i] = host
	}
	hosts := map[string]bool{}
	for _, host := range p.Hosts {
		hosts[host] = true
	}
	extracted := map[string]bool{}
	if p.Login != nil {
		loginExtracted, err := validateLogin(p.Login, hosts, p.Hosts[0])
		if err != nil {
			return err
		}
		// 登录时捞到的变量（token 等）必须能被子步骤引用 —— 否则"先登录换令牌、再用令牌查账"
		// 这种最常见的形态根本没法写（这正是本包要支持的核心场景）。
		for field := range loginExtracted {
			extracted[field] = true
		}
	}
	if len(p.Read) == 0 {
		return fmt.Errorf("采集包必须至少有一个读取步骤")
	}
	for i := range p.Read {
		step := &p.Read[i]
		if step.Name == "" {
			step.Name = fmt.Sprintf("第 %d 步", i+1)
		}
		step.Method = strings.ToUpper(strings.TrimSpace(step.Method))
		if step.Method == "" {
			step.Method = MethodGet
		}
		if step.Method != MethodGet && step.Method != MethodPost {
			return fmt.Errorf("%s：方法只允许 GET/POST，收到 %s", step.Name, step.Method)
		}
		step.BodyType = strings.ToLower(strings.TrimSpace(step.BodyType))
		if step.BodyType != "" && step.BodyType != BodyJSON && step.BodyType != BodyForm {
			return fmt.Errorf("%s：body_type 只允许 json/form", step.Name)
		}
		if err := checkURL(step.URL, hosts, p.Hosts[0], step.Name); err != nil {
			return err
		}
		for k, v := range step.Headers {
			if err := checkTemplate(v, extracted, step.Name+" 的头 "+k); err != nil {
				return err
			}
		}
		if err := checkTemplate(step.Body, extracted, step.Name+" 的正文"); err != nil {
			return err
		}
		for j := range step.Extract {
			if err := validateRule(step.Extract[j], step.Name); err != nil {
				return err
			}
			extracted[step.Extract[j].Field] = true
		}
	}
	if !extracted[FieldBalance] && !extracted[FieldQuota] {
		return fmt.Errorf("采集包必须能取到 %s 或 %s（否则装上来也没有意义）", FieldBalance, FieldQuota)
	}
	return nil
}

func validateLogin(login *LoginSpec, hosts map[string]bool, defaultHost string) (map[string]bool, error) {
	extracted := map[string]bool{}
	login.Mode = strings.ToLower(strings.TrimSpace(login.Mode))
	if login.Mode == "" {
		login.Mode = LoginModeForm
	}
	switch login.Mode {
	case LoginModeForm:
		if err := checkURL(login.URL, hosts, defaultHost, "登录步骤"); err != nil {
			return nil, err
		}
		login.Method = strings.ToUpper(strings.TrimSpace(login.Method))
		if login.Method == "" {
			login.Method = MethodPost
		}
		if login.Method != MethodGet && login.Method != MethodPost {
			return nil, fmt.Errorf("登录步骤：方法只允许 GET/POST")
		}
		login.BodyType = strings.ToLower(strings.TrimSpace(login.BodyType))
		if login.BodyType != "" && login.BodyType != BodyJSON && login.BodyType != BodyForm {
			return nil, fmt.Errorf("登录步骤：body_type 只允许 json/form")
		}
		for k, v := range login.Headers {
			if err := checkTemplate(v, extracted, "登录头 "+k); err != nil {
				return nil, err
			}
		}
		if err := checkTemplate(login.Body, extracted, "登录正文"); err != nil {
			return nil, err
		}
		for _, rule := range login.Extract {
			if err := validateRule(rule, "登录步骤"); err != nil {
				return nil, err
			}
			extracted[rule.Field] = true
		}
	case LoginModeInteractive:
		if login.URL != "" {
			if err := checkURL(login.URL, hosts, defaultHost, "登录页"); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("login.mode 必须是 %s 或 %s", LoginModeForm, LoginModeInteractive)
	}
	return extracted, nil
}

func validateRule(rule ExtractRule, where string) error {
	if !fieldPattern.MatchString(rule.Field) {
		return fmt.Errorf("%s：提取字段名非法（只允许小写字母/数字/下划线）：%s", where, rule.Field)
	}
	switch rule.From {
	case FromJSON, FromRegex, FromHeader, FromStatus:
	case "":
		return fmt.Errorf("%s：提取规则 %s 缺少 from", where, rule.Field)
	default:
		return fmt.Errorf("%s：提取规则 %s 的 from 非法：%s", where, rule.Field, rule.From)
	}
	switch rule.From {
	case FromJSON, FromHeader:
		if strings.TrimSpace(rule.Path) == "" {
			return fmt.Errorf("%s：提取规则 %s 需要 path", where, rule.Field)
		}
	case FromRegex:
		if _, err := regexp.Compile(rule.Regex); err != nil {
			return fmt.Errorf("%s：提取规则 %s 的正则非法：%v", where, rule.Field, err)
		}
	}
	return nil
}

// checkURL：只允许 http/https，且主机必须在包自己声明的主机集合内（防"白名单写了 A、请求打 B"）。
func checkURL(raw string, hosts map[string]bool, defaultHost, where string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("%s：地址为空", where)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s：地址无法解析：%v", where, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s：只允许 http/https，收到 %s", where, parsed.Scheme)
	}
	host := strings.ToLower(parsed.Host)
	if host == "" {
		return fmt.Errorf("%s：地址缺少主机", where)
	}
	if !hosts[host] {
		return fmt.Errorf("%s：目标主机 %s 不在采集包声明的 hosts 里（拒绝访问未声明主机）", where, host)
	}
	return nil
}

func checkTemplate(tmpl string, known map[string]bool, where string) error {
	for _, match := range placeholderRegex.FindAllStringSubmatch(tmpl, -1) {
		name := match[1]
		if known[name] {
			continue
		}
		allowed := false
		for _, candidate := range templateVars {
			if name == candidate {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("%s：未知占位符 {%s}（可用：%s 或前序步骤提取出的变量）", where, name, strings.Join(templateVars, "/"))
		}
	}
	return nil
}

// RenderTemplate 用变量渲染模板；缺变量时报错而不是送空串（空串会变成"密码为空"这类误导性请求）。
func RenderTemplate(tmpl string, vars map[string]string) (string, error) {
	var missing []string
	out := placeholderRegex.ReplaceAllStringFunc(tmpl, func(match string) string {
		name := placeholderRegex.FindStringSubmatch(match)[1]
		if value, ok := vars[name]; ok {
			return value
		}
		missing = append(missing, name)
		return ""
	})
	if len(missing) > 0 {
		sort.Strings(missing)
		return "", fmt.Errorf("模板缺少变量：%s", strings.Join(missing, ", "))
	}
	return out, nil
}

// LookupJSON 按点号路径取值，支持数组下标：data.items.0.balance。
func LookupJSON(root any, path string) (any, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}
	current := root
	for _, segment := range strings.Split(path, ".") {
		switch node := current.(type) {
		case map[string]any:
			value, ok := node[segment]
			if !ok {
				return nil, false
			}
			current = value
		case []any:
			index := -1
			if _, err := fmt.Sscanf(segment, "%d", &index); err != nil || index < 0 || index >= len(node) {
				return nil, false
			}
			current = node[index]
		default:
			return nil, false
		}
	}
	return current, true
}

// CoerceNumber 把 JSON 里的数字（含字符串数字、json.Number）读成 float64。
func CoerceNumber(value any) (float64, bool) {
	switch node := value.(type) {
	case float64:
		return node, true
	case float32:
		return float64(node), true
	case int:
		return float64(node), true
	case int64:
		return float64(node), true
	case json.Number:
		parsed, err := node.Float64()
		return parsed, err == nil
	case string:
		trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(node), "$"))
		trimmed = strings.ReplaceAll(trimmed, ",", "")
		if trimmed == "" {
			return 0, false
		}
		var parsed float64
		if _, err := fmt.Sscanf(trimmed, "%g", &parsed); err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// CoerceString 把值读成字符串（用于 token/币种/账号名）。
func CoerceString(value any) (string, bool) {
	switch node := value.(type) {
	case string:
		return node, true
	case float64:
		return strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%f", node), "0"), "."), true
	case json.Number:
		return node.String(), true
	case bool:
		if node {
			return "true", true
		}
		return "false", true
	default:
		return "", false
	}
}
