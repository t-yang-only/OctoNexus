package collector

import (
	"fmt"
	"net/url"
	"strings"
)

// 内置站点模板：把"输入账号密码登录站点看余额"这件事从"你自己写包"降成"你选类型、填网址"。
//
// 为什么要有模板：四类上游的登录/查账接口是**业界惯例**而不是秘密，让人手抄一遍只是在制造抄错的机会；
// 真正各家不同的只有路径细节，所以模板给的是"能直接装载的起点"，而不是"保证通吃的适配器"。
//
// 两条纪律：
//  1. 模板必须能过装载门禁（Validate）才允许发给前端 —— 生成即校验，不合格的模板等于让用户白填一次表单；
//  2. hosts 由用户填的网址推导，**绝不预置域名白名单**（预置等于绕过 fail closed 的主机约束）。
//
// 关于验证码：new-api / one-api 常挂 Turnstile 或图形验证码，此时 form 模式必然登录失败 ——
// 所以每类站点都出两版：form（直接 POST 拿会话）与 interactive（走反代登录页，人在页面里输验证码，
// 会话由 octopus 捕获）。面板据此在"需要验证码"时把人引到 interactive 版本。

// TemplateInfo 是一条模板的元信息（给前端列表用，不含具体主机）。
type TemplateInfo struct {
	Kind        string   `json:"kind"`         // 站点类型标识，形如 new-api / one-api / sub2api / litellm
	Title       string   `json:"title"`        // 中文名
	Note        string   `json:"note"`         // 适用面 + 已知差异（诚实说明哪里可能要微调）
	Units       string   `json:"units"`        // usd / points —— 决定读数要不要按点数折算
	Mode        string   `json:"mode"`         // form / interactive
	NeedCaptcha bool     `json:"need_captcha"` // 该形态常需要验证码（面板提示改走反代登录）
	Paths       []string `json:"paths"`        // 登录与读账端点（给人看的）
	Fields      []string `json:"fields"`       // 模板会填的提取字段
}

// TemplateKinds 是内置站点类型的顺序（面板下拉按这个顺序展示）。
var TemplateKinds = []string{"new-api", "one-api", "ai-gateway", "sub2api", "litellm"}

func Templates() []TemplateInfo {
	var items []TemplateInfo
	for _, kind := range TemplateKinds {
		for _, mode := range []string{LoginModeForm, LoginModeInteractive} {
			spec, ok := templateSpecs[kind]
			if !ok {
				continue
			}
			info := spec.info
			info.Mode = mode
			if mode == LoginModeInteractive {
				info.NeedCaptcha = false // interactive 本身就是为了验证码场景，不再单独提示
				info.Paths = spec.readPaths
			}
			items = append(items, info)
		}
	}
	return items
}

// TemplateNames 返回可读的站点类型名（错误提示用）。
func TemplateNames() string {
	return strings.Join(TemplateKinds, " / ")
}

// BuildTemplate 按站点类型 + 用户填的网址生成一份**已通过装载门禁**的采集包。
func BuildTemplate(kind, baseURL, mode string) (Pack, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = LoginModeForm
	}
	if mode != LoginModeForm && mode != LoginModeInteractive {
		return Pack{}, fmt.Errorf("登录形态只允许 %s 或 %s", LoginModeForm, LoginModeInteractive)
	}
	spec, ok := templateSpecs[kind]
	if !ok || kind == "" {
		return Pack{}, fmt.Errorf("未知的站点类型 %q（内置：%s）", kind, TemplateNames())
	}
	base, host, err := normalizeBaseURL(baseURL)
	if err != nil {
		return Pack{}, err
	}
	pack := Pack{
		Name:    spec.info.Title,
		Version: "1.0.0",
		Units:   spec.info.Units,
		Hosts:   []string{host},
		Notes:   spec.info.Note,
	}
	pack.Read = renderReadSteps(spec.read, base)
	if mode == LoginModeForm {
		login := spec.login
		login.Mode = LoginModeForm
		login.URL = base + login.URL
		if login.Method == "" {
			login.Method = MethodPost
		}
		if login.BodyType == "" {
			login.BodyType = BodyJSON
		}
		pack.Login = &login
	} else {
		// 反代登录：登录页就是站点自己（不是某个 API 端点），人在页面里登录，会话由 octopus 捕获。
		pack.Login = &LoginSpec{Mode: LoginModeInteractive}
	}
	if err := pack.Validate(); err != nil {
		return Pack{}, fmt.Errorf("内置模板不合格（这是程序缺陷，请上报）：%v", err)
	}
	return pack, nil
}

type templateSpec struct {
	info      TemplateInfo
	login     LoginSpec
	read      []ReadStep
	readPaths []string
}

var templateSpecs = map[string]templateSpec{
	// new-api：one-api 的商业 fork，当前主流。登录 /api/user/login，查账 /api/user/self。
	// 额度单位是**点**（quota / used_quota），不是美元 —— 单位填错会让余额差 50 万倍。
	"new-api": {
		info: TemplateInfo{
			Kind: "new-api", Title: "New API（当前主流）", Units: UnitsPoints, NeedCaptcha: true,
			Note:   "one-api 的商业 fork，商业中转站首选。额度是点（quota / used_quota），octopus 会按 balance_points_per_unit 折算；若站点开了 Turnstile 或图形验证码，form 模式会失败，请改用反代登录。部分版本要求请求头 New-Api-User，若读账 401 就在包里给 read 步骤加该头。",
			Paths:  []string{"POST /api/user/login", "GET /api/user/self"},
			Fields: []string{FieldBalance, FieldUsed},
		},
		login: LoginSpec{
			URL:  "/api/user/login",
			Body: `{"username":"{username}","password":"{password}"}`,
			// 实测（2026-09-20，api.uu6.top / pipixia1.online）：登录响应会带 access_token，
			// 而 /api/user/self 只认 Authorization: Bearer —— 只带 Cookie 读不到额度。
			// 所以这里必须把令牌捞出来，下一步带着它读账。
			Extract: []ExtractRule{{Field: VarToken, From: FromJSON, Path: "data.access_token"}},
		},
		read: []ReadStep{{
			Name:    "账户额度",
			URL:     "/api/user/self",
			Headers: map[string]string{"Authorization": "Bearer {" + VarToken + "}"},
			// 口径要点（2026-09-20 真实站点实测踩过）：new-api 的 data.quota 就是**剩余额度**本身，
			// data.used_quota 是累计已用。把 quota 当"总额度"再减 used_quota 会算出
			// -187900000 点（≈-375.8 美元）这种负数余额 —— 所以这里直接把 quota 当余额读，
			// 绝不走"总额度 − 已用"的推导分支。
			Extract: []ExtractRule{
				{Field: FieldBalance, From: FromJSON, Path: "data.quota"},
				{Field: FieldUsed, From: FromJSON, Path: "data.used_quota"},
			},
		}},
		readPaths: []string{"GET /api/user/self"},
	},

	// one-api：老牌鼻祖，接口同源，差异只在返回体版本。
	"one-api": {
		info: TemplateInfo{
			Kind: "one-api", Title: "One API（老牌鼻祖）", Units: UnitsPoints, NeedCaptcha: true,
			Note:   "songquanpeng/one-api 初代统一网关，存量最多。接口与 new-api 同源，额度同样是点；停更版本不返回 used_quota 的话，把 read 里的 used 规则删掉即可（只留 quota 也能入账）。老版本只认 Cookie（不含 access_token），所以这一版刻意不取令牌；若你的站点登录响应里有 access_token，请改用 new-api 模板（Bearer 读法）。",
			Paths:  []string{"POST /api/user/login", "GET /api/user/self"},
			Fields: []string{FieldQuota, FieldUsed},
		},
		login: LoginSpec{
			URL:  "/api/user/login",
			Body: `{"username":"{username}","password":"{password}"}`,
		},
		read: []ReadStep{{
			Name: "账户额度",
			URL:  "/api/user/self",
			// 同样把 data.quota 当剩余额度读（one-api 与 new-api 同源）。
			Extract: []ExtractRule{
				{Field: FieldBalance, From: FromJSON, Path: "data.quota"},
				{Field: FieldUsed, From: FromJSON, Path: "data.used_quota"},
			},
		}},
		readPaths: []string{"GET /api/user/self"},
	},

	// ai-gateway：实测六家（api.53hk.cn / apikey.fun / okai.la / cochacode.com / aiaaa.cc / sub.tohoqing.com）
	// 是**同一套 SPA 产品**：登录页是 /login 的 React 应用，接口族完全一致。
	//   POST /api/v1/auth/login  {"email","password"} → data.access_token / refresh_token；
	//   GET  /api/v1/auth/me （Bearer）→ data.balance 就是美元余额；
	//   站点普遍挂 geetest / 腾讯验证码：form 模式会被拒（reason 含 captcha verification failed），
	//   这时只能用反代登录（人在页面里过验证码，会话由 octopus 捕获）。
	"ai-gateway": {
		info: TemplateInfo{
			Kind: "ai-gateway", Title: "AI Gateway（SPA 平台）", Units: UnitsUSD, NeedCaptcha: true,
			Note:   "一套 SPA 中转平台（登录页 /login，账号是邮箱）。余额在 /api/v1/auth/me 的 data.balance，美元口径不折算。若站点开了 geetest / 腾讯验证码，form 模式必然被拒，请改用反代登录形态。",
			Paths:  []string{"POST /api/v1/auth/login", "GET /api/v1/auth/me"},
			Fields: []string{FieldBalance},
		},
		login: LoginSpec{
			URL:  "/api/v1/auth/login",
			Body: `{"email":"{username}","password":"{password}"}`,
			Extract: []ExtractRule{{Field: VarToken, From: FromJSON, Path: "data.access_token"}},
		},
		read: []ReadStep{{
			Name:    "账户余额",
			URL:     "/api/v1/auth/me",
			Headers: map[string]string{"Authorization": "Bearer {" + VarToken + "}"},
			Extract: []ExtractRule{{Field: FieldBalance, From: FromJSON, Path: "data.balance"}},
		}},
		readPaths: []string{"GET /api/v1/auth/me"},
	},

	// sub2api：扒网页会话把订阅账号转成 API，站点前端各改各的 —— 只保证"主机约束 + 字段形状"。
	"sub2api": {
		info: TemplateInfo{
			Kind: "sub2api", Title: "sub2api（订阅拼车）", Units: UnitsUSD, NeedCaptcha: true,
			Note:   "订阅账号逆向转 API（Claude Plus / ChatGPT Plus 拼车）。这类站点前端各改各的，模板只保证主机约束与美元口径；登录端点与读账路径请按站点实际接口微调，或直接用反代登录（会话也能被 octopus 捕获）。",
			Paths:  []string{"POST /api/auth/login", "GET /api/user/info"},
			Fields: []string{FieldBalance, FieldQuota},
		},
		login: LoginSpec{
			URL:     "/api/auth/login",
			Body:    `{"email":"{username}","password":"{password}"}`,
			Headers: map[string]string{"Content-Type": "application/json"},
		},
		read: []ReadStep{{
			Name: "订阅余额",
			URL:  "/api/user/info",
			Extract: []ExtractRule{
				{Field: FieldBalance, From: FromJSON, Path: "data.balance"},
				{Field: FieldQuota, From: FromJSON, Path: "data.quota"},
			},
		}},
		readPaths: []string{"GET /api/user/info"},
	},

	// LiteLLM Proxy：Python 网关，预算口径本来就是美元（max_budget / spend）。
	"litellm": {
		info: TemplateInfo{
			Kind: "litellm", Title: "LiteLLM Proxy（海外 / 企业向）", Units: UnitsUSD, NeedCaptcha: false,
			Note:   "Python 写的 LLM 网关，预算本来就是美元：max_budget 是总额度、spend 是已用。若登录要邮箱而非用户名，把 login.body 的 username 换成站点要求的字段名。",
			Paths:  []string{"POST /user/login", "GET /user/info"},
			Fields: []string{FieldQuota, FieldUsed},
		},
		login: LoginSpec{
			URL:  "/user/login",
			Body: `{"username":"{username}","password":"{password}"}`,
		},
		read: []ReadStep{{
			Name: "用户预算",
			URL:  "/user/info",
			Extract: []ExtractRule{
				{Field: FieldQuota, From: FromJSON, Path: "user_info.max_budget"},
				{Field: FieldUsed, From: FromJSON, Path: "user_info.spend"},
			},
		}},
		readPaths: []string{"GET /user/info"},
	},
}

func renderReadSteps(steps []ReadStep, base string) []ReadStep {
	out := make([]ReadStep, 0, len(steps))
	for _, step := range steps {
		clone := step
		clone.URL = base + step.URL
		out = append(out, clone)
	}
	return out
}

// normalizeBaseURL 把用户填的网址归一成 "scheme://host"（去掉路径与末尾斜杠），并返回主机名。
// 这些站点的管理接口挂在站点根，用户填 "https://api.example.com/" 还是带路径都应能得到同一结果。
func normalizeBaseURL(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("站点地址不能为空")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("站点地址无法解析：%v", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("站点地址只允许 http/https，收到 %s", parsed.Scheme)
	}
	host := strings.ToLower(parsed.Host)
	if host == "" {
		return "", "", fmt.Errorf("站点地址缺少主机")
	}
	return parsed.Scheme + "://" + parsed.Host, host, nil
}
