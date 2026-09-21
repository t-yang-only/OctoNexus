package collector

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// basePack 是一份最小可用包：一份登录 + 一份读账。base 是站点根地址，host 是它的主机（含端口）。
func basePack(base, host string) string {
	return `{
		"name": "测试站",
		"units": "usd",
		"hosts": ["` + host + `"],
		"login": {
			"mode": "form",
			"url": "` + base + `/api/login",
			"body_type": "json",
			"body": "{\"username\":\"{username}\",\"password\":\"{password}\"}",
			"extract": [{"field": "token", "from": "json", "path": "data.token"}]
		},
		"read": [
			{
				"name": "读余额",
				"url": "` + base + `/api/user/self",
				"headers": {"Authorization": "Bearer {token}"},
				"extract": [
					{"field": "balance", "from": "json", "path": "data.quota"},
					{"field": "used", "from": "json", "path": "data.used_quota"},
					{"field": "currency", "from": "json", "path": "data.unit"}
				]
			}
		]
	}`
}

func TestParsePackAcceptsValidPackAndAppliesDefaults(t *testing.T) {
	pack, err := ParsePack([]byte(basePack("https://api.example.com", "api.example.com")))
	if err != nil {
		t.Fatalf("合法采集包被拒绝：%v", err)
	}
	if pack.Units != UnitsUSD {
		t.Errorf("units 默认值错误：%s", pack.Units)
	}
	if pack.Read[0].Method != MethodGet {
		t.Errorf("方法默认值应为 GET，实际 %s", pack.Read[0].Method)
	}
	if pack.Login.Mode != LoginModeForm || pack.Login.Method != MethodPost {
		t.Errorf("登录默认值错误：mode=%s method=%s", pack.Login.Mode, pack.Login.Method)
	}
}

// TestParsePackRejectsBadPacks 是装载门禁的判据矩阵：任一"看起来能用其实危险/无用"的包都必须被拒绝。
func TestParsePackRejectsBadPacks(t *testing.T) {
	cases := map[string]string{
		"没有声明主机": `{"name":"x","read":[{"url":"https://a.com/b","extract":[{"field":"balance","from":"json","path":"d.q"}]}]}`,
		"请求打到未声明主机": `{"name":"x","hosts":["a.com"],"read":[{"url":"https://b.com/b","extract":[{"field":"balance","from":"json","path":"d.q"}]}]}`,
		"非 http 协议":  `{"name":"x","hosts":["a.com"],"read":[{"url":"file:///etc/passwd","extract":[{"field":"balance","from":"json","path":"d.q"}]}]}`,
		"未知占位符":     `{"name":"x","hosts":["a.com"],"read":[{"url":"https://a.com/b","headers":{"X":"{passwrod}"},"extract":[{"field":"balance","from":"json","path":"d.q"}]}]}`,
		"单位非法":      `{"name":"x","units":"eur","hosts":["a.com"],"read":[{"url":"https://a.com/b","extract":[{"field":"balance","from":"json","path":"d.q"}]}]}`,
		"没有任何读取步骤":  `{"name":"x","hosts":["a.com"],"read":[]}`,
		"取不到余额或额度":   `{"name":"x","hosts":["a.com"],"read":[{"url":"https://a.com/b","extract":[{"field":"token","from":"json","path":"d.t"}]}]}`,
		"方法越权":       `{"name":"x","hosts":["a.com"],"read":[{"url":"https://a.com/b","method":"DELETE","extract":[{"field":"balance","from":"json","path":"d.q"}]}]}`,
		"正则非法":       `{"name":"x","hosts":["a.com"],"read":[{"url":"https://a.com/b","extract":[{"field":"balance","from":"regex","regex":"("}]}]}`,
		"提取来源非法":     `{"name":"x","hosts":["a.com"],"read":[{"url":"https://a.com/b","extract":[{"field":"balance","from":"cookie"}]}]}`,
		"字段名非法":      `{"name":"x","hosts":["a.com"],"read":[{"url":"https://a.com/b","extract":[{"field":"Balance-USD","from":"json","path":"d.q"}]}]}`,
		"未知顶层字段":     `{"name":"x","hosts":["a.com"],"whatever":1,"read":[{"url":"https://a.com/b","extract":[{"field":"balance","from":"json","path":"d.q"}]}]}`,
	}
	for name, raw := range cases {
		if _, err := ParsePack([]byte(raw)); err == nil {
			t.Errorf("%s：应当被拒绝，却通过了校验", name)
		}
	}
}

func TestLookupJSONAndCoerce(t *testing.T) {
	payload := map[string]any{
		"data": map[string]any{
			"items": []any{map[string]any{"balance": json.Number("42.5")}},
			"unit":  "usd",
		},
	}
	if value, ok := LookupJSON(payload, "data.items.0.balance"); !ok {
		t.Fatal("嵌套数组取值失败")
	} else if parsed, ok := CoerceNumber(value); !ok || parsed != 42.5 {
		t.Errorf("数字解析错误：%v %v", parsed, ok)
	}
	if _, ok := LookupJSON(payload, "data.items.9.balance"); ok {
		t.Error("越界下标应取不到")
	}
	for raw, want := range map[string]float64{"$1,234.5": 1234.5, "0": 0, " 7 ": 7} {
		got, ok := CoerceNumber(raw)
		if !ok || got != want {
			t.Errorf("字符串数字 %q 解析成 %v（ok=%v），期望 %v", raw, got, ok, want)
		}
	}
}

func TestRenderTemplateFailsOnMissingVar(t *testing.T) {
	if _, err := RenderTemplate(`{"a":"{username}"}`, map[string]string{"username": "u"}); err != nil {
		t.Fatalf("已给变量却报错：%v", err)
	}
	if _, err := RenderTemplate(`{"a":"{password}"}`, map[string]string{"username": "u"}); err == nil {
		t.Error("缺变量必须报错（送空串会变成\"密码为空\"这类误导性请求）")
	}
}

// TestRunLoginAndReadThroughHTTP 是"包"这条路的端到端判据：登录拿到令牌 → 用令牌读账。
func TestRunLoginAndReadThroughHTTP(t *testing.T) {
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/login":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Set-Cookie", "sid=abc123; Path=/")
			_, _ = w.Write([]byte(`{"code":0,"data":{"token":"T-1"}}`))
		case "/api/user/self":
			sawAuth = r.Header.Get("Authorization")
			if r.Header.Get("Cookie") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"message":"no cookie"}`))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"quota":12.5,"used_quota":3,"unit":"usd"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	host := strings.TrimPrefix(server.URL, "http://")
	pack, err := ParsePack([]byte(basePack(server.URL, host)))
	if err != nil {
		t.Fatalf("夹具包非法：%v", err)
	}
	session := NewSession()
	result := Run(t.Context(), pack, session, Credentials{Username: "u", Password: "p"}, "")
	if !result.OK() {
		t.Fatalf("采集失败：%s（步骤：%v）", result.ReasonText, result.Steps)
	}
	if result.Balance != 12.5 || result.Used != 3 || result.Currency != "usd" {
		t.Errorf("读数错误：balance=%v used=%v currency=%v", result.Balance, result.Used, result.Currency)
	}
	if sawAuth != "Bearer T-1" {
		t.Errorf("读取请求没有带上登录提取出的令牌，实际 Authorization=%q", sawAuth)
	}
	if len(result.Session.Cookies) != 1 || result.Session.Cookies[0].Name != "sid" {
		t.Errorf("会话 Cookie 没被捕获：%+v", result.Session.Cookies)
	}
	if result.Session.Vars["token"] != "T-1" {
		t.Errorf("令牌没进会话变量：%+v", result.Session.Vars)
	}
}

// TestRunRejectsLoginWithoutSession：登录接口回 200 但既没 Cookie 也没提取到令牌 = 其实没登进去。
// 这类"看起来成功"的假绿是最坑的（用户以为好了，下一轮扫描才发现读不到）。
func TestRunRejectsLoginWithoutSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok"}`))
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	// 这份包不提取任何令牌，只看 Cookie：登录接口回 200 但没下发 Cookie = 其实没登进去。
	raw := `{
		"name": "只认 Cookie 的站",
		"hosts": ["` + host + `"],
		"login": {"mode": "form", "url": "` + server.URL + `/api/login", "body_type": "json", "body": "{\"p\":\"{password}\"}"},
		"read": [{"url": "` + server.URL + `/api/self", "extract": [{"field": "balance", "from": "json", "path": "data.quota"}]}]
	}`
	pack, err := ParsePack([]byte(raw))
	if err != nil {
		t.Fatalf("夹具包非法：%v", err)
	}
	result := Run(t.Context(), pack, NewSession(), Credentials{Username: "u", Password: "bad"}, "")
	if result.OK() {
		t.Fatal("没有任何会话却报成功")
	}
	if result.ReasonKey != ReasonUnauthorized {
		t.Errorf("原因码应为 %s，实际 %s（%s）", ReasonUnauthorized, result.ReasonKey, result.ReasonText)
	}
}

// TestRunReportsPackShapeMismatchAsUnparsable：包说字段在 data.token，站点却没给 —— 这属于
// "包与站点对不上"，必须报 unparsable 并指出字段名，而不是含糊地说"凭据不对"。
func TestRunReportsPackShapeMismatchAsUnparsable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"message":"ok"}`))
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	pack, err := ParsePack([]byte(basePack(server.URL, host)))
	if err != nil {
		t.Fatalf("夹具包非法：%v", err)
	}
	result := Run(t.Context(), pack, NewSession(), Credentials{Username: "u", Password: "p"}, "")
	if result.OK() {
		t.Fatal("没有会话却报成功")
	}
	if result.ReasonKey != ReasonUnparsable || !strings.Contains(result.ReasonText, "data.token") {
		t.Errorf("应报 unparsable 并点名缺失字段，实际 %s：%s", result.ReasonKey, result.ReasonText)
	}
}

// TestRunRefusesUndeclaredRedirectHost：白名单外的跳转必须被拒绝（否则包声明的 hosts 形同虚设）。
func TestRunRefusesUndeclaredRedirectHost(t *testing.T) {
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"quota":999}}`))
	}))
	defer other.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Redirect(w, r, other.URL+"/api/user/self", http.StatusFound)
	}))
	defer redirector.Close()

	host := strings.TrimPrefix(redirector.URL, "http://")
	raw := `{"name":"x","hosts":["` + host + `"],"read":[{"url":"http://` + host + `/api/user/self","extract":[{"field":"balance","from":"json","path":"data.quota"}]}]}`
	pack, err := ParsePack([]byte(raw))
	if err != nil {
		t.Fatalf("夹具包非法：%v", err)
	}
	result := Run(t.Context(), pack, NewSession(), Credentials{}, "")
	if result.OK() {
		t.Fatal("跟到未声明主机还读到了数（白名单失效）")
	}
}

// TestApplyFieldsDerivesBalanceFromQuota：只给总额度与已用时，余额要能推出来。
func TestApplyFieldsDerivesBalanceFromQuota(t *testing.T) {
	result := Result{Units: UnitsPoints}
	applyFields(&result, map[string]string{FieldQuota: "100", FieldUsed: "30"})
	if !result.OK() || result.Balance != 70 {
		t.Errorf("额度 − 已用 推导失败：%+v", result)
	}
}
