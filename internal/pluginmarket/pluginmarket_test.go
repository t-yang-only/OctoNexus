package pluginmarket

import (
	"encoding/json"
	"strings"
	"testing"
)

// 需求1/2/3 的判据：插件解析、凭据拒止、GitHub 链接形态归一化。
//
// # 这个功能要防住的事
//
//  1. **kind 缺失或写错时"猜一个"** —— 猜错会把号池规格当采集包装，
//     报错还指不到真正的问题（用户看到的是 read 缺失，实际是 kind 错）；
//  2. **插件文件携带凭据** —— "分享插件"会顺手分享别人的账号，
//     而这种泄露在"我就发给朋友看看"时几乎必然发生；
//  3. **GitHub 链接形态不被识别时静默尝试** —— 下载回来是 HTML，
//     用户看到"不是合法 JSON"，根本想不到是链接形态不对。

func TestParseRejectsUnknownKind(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"缺 kind", `{"spec":{"hosts":["a.com"]}}`},
		{"kind 拼错", `{"kind":"balances","spec":{}}`},
		{"kind 是第三类", `{"kind":"proxy","spec":{}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse([]byte(tc.raw)); err == nil {
				t.Fatalf("应拒绝：%s", tc.raw)
			}
		})
	}
}

func TestParseAcceptsBothKinds(t *testing.T) {
	balance := `{"kind":"balance","name":"某站","spec":{"hosts":["okai.la"],"units":"usd","read":[{"name":"balance"}]}}`
	pool := `{"kind":"pool","spec":{"kind":"custom-x","title":"X 站","base_url":"https://x.example","capabilities":["list","get"]}}`

	plugin, err := Parse([]byte(balance))
	if err != nil {
		t.Fatalf("balance 插件应被接受: %v", err)
	}
	if plugin.Kind != KindBalance || plugin.DisplayName() != "某站" {
		t.Errorf("kind=%v name=%q，want balance / 某站", plugin.Kind, plugin.DisplayName())
	}

	plugin, err = Parse([]byte(pool))
	if err != nil {
		t.Fatalf("pool 插件应被接受: %v", err)
	}
	// 没给 name 时用 spec 里的 title —— 安装列表里不能出现空白行。
	if plugin.DisplayName() != "X 站" {
		t.Errorf("name=%q，want 回落到 spec.title 的 X 站", plugin.DisplayName())
	}
}

// 插件文件携带凭据必须被拒：分享插件会连带分享凭据。
func TestParseRejectsEmbeddedCredentials(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"顶层 secret", `{"kind":"pool","spec":{"kind":"c","base_url":"https://x","secret":"sk-xxx"}}`},
		{"嵌套在 login 里的 password", `{"kind":"balance","spec":{"hosts":["a"],"read":[],"login":{"password":"p"}}}`},
		{"read 步骤里的 token", `{"kind":"balance","spec":{"hosts":["a"],"read":[{"headers":{"Authorization":"Bearer t"}}]}}`},
		{"大小写变体 api_key", `{"kind":"pool","spec":{"kind":"c","base_url":"https://x","API_KEY":"k"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.raw))
			if err == nil {
				t.Fatalf("应拒斥携带凭据的插件：%s", tc.raw)
			}
			if !strings.Contains(err.Error(), "凭据") {
				t.Errorf("错误信息应说明是凭据问题，实得：%v", err)
			}
		})
	}
}

// spec 形状不对要明确指出，而不是让用户看到 read 缺失之类的间接错误。
func TestParseRejectsMalformedSpec(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"spec 是数组", `{"kind":"balance","spec":[]}`, "spec 必须是 JSON 对象"},
		{"spec 是字符串", `{"kind":"pool","spec":"x"}`, "spec 必须是 JSON 对象"},
		{"spec 缺失", `{"kind":"pool"}`, "缺少 spec"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.raw))
			if err == nil {
				t.Fatalf("应拒绝：%s", tc.raw)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误应包含 %q，实得：%v", tc.want, err)
			}
		})
	}
}

func TestParseRejectsOversizedAndEmpty(t *testing.T) {
	if _, err := Parse(nil); err == nil {
		t.Error("空内容应被拒")
	}
	big := `{"kind":"balance","spec":{"hosts":["a"],"read":[],"pad":"` + strings.Repeat("x", MaxPluginBytes) + `"}}`
	if _, err := Parse([]byte(big)); err == nil {
		t.Error("超限内容应被拒")
	}
}

// GitHub 链接形态归一化：认得的三种要转成 raw，认不的要明确报错。
func TestNormalizeGitHubURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"blob 页面链接",
			"https://github.com/octo/plugins/blob/main/balance/okai.json",
			"https://raw.githubusercontent.com/octo/plugins/main/balance/okai.json",
		},
		{
			"raw 路径链接",
			"https://github.com/octo/plugins/raw/main/balance/okai.json",
			"https://raw.githubusercontent.com/octo/plugins/main/balance/okai.json",
		},
		{
			"raw 直链原样",
			"https://raw.githubusercontent.com/octo/plugins/main/balance/okai.json",
			"https://raw.githubusercontent.com/octo/plugins/main/balance/okai.json",
		},
		{
			"API contents 链接",
			"https://api.github.com/repos/octo/plugins/contents/balance/okai.json",
			"https://raw.githubusercontent.com/octo/plugins/HEAD/balance/okai.json",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeGitHubURL(tc.in)
			if err != nil {
				t.Fatalf("应识别：%v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeGitHubURLRejectsUnknownShapes(t *testing.T) {
	cases := []string{
		"",
		"http://github.com/octo/plugins/blob/main/x.json", // 非 https
		"https://example.com/x.json",                      // 非 GitHub
		"https://github.com/octo/plugins",                 // 缺文件路径
		"https://github.com/octo/plugins/tree/main/dir",   // tree 页面不是文件
	}
	for _, in := range cases {
		if _, err := normalizeGitHubURL(in); err == nil {
			t.Errorf("应拒绝 %q", in)
		}
	}
}

// Prepare 只做形状校验与转交，不落库 —— 调用方要先过门禁再决定写不写。
func TestPrepareValidatesSpecShape(t *testing.T) {
	// balance 缺 read：该说"必须声明 read"，而不是让它在采集侧报一个间接错误。
	plugin, err := Parse([]byte(`{"kind":"balance","spec":{"hosts":["a"]}}`))
	if err != nil {
		t.Fatalf("解析应通过（形状问题在 Prepare 报）: %v", err)
	}
	if _, err := plugin.Prepare(); err == nil || !strings.Contains(err.Error(), "read") {
		t.Errorf("应指出缺 read，实得：%v", err)
	}

	// pool 缺 kind（适配器标识）。
	plugin, err = Parse([]byte(`{"kind":"pool","spec":{"base_url":"https://x"}}`))
	if err != nil {
		t.Fatalf("解析应通过: %v", err)
	}
	if _, err := plugin.Prepare(); err == nil || !strings.Contains(err.Error(), "kind") {
		t.Errorf("应指出缺 kind，实得：%v", err)
	}

	// 合法的一份要能转交，且载荷原样带回（供界面预览）。
	plugin, err = Parse([]byte(`{"kind":"pool","name":"X","spec":{"kind":"custom-x","title":"X","base_url":"https://x"}}`))
	if err != nil {
		t.Fatalf("解析应通过: %v", err)
	}
	result, err := plugin.Prepare()
	if err != nil {
		t.Fatalf("Prepare 应通过: %v", err)
	}
	if result.Kind != KindPool || result.Name != "X" || len(result.Spec) == 0 {
		t.Errorf("转交结果不完整：%+v", result)
	}
}

// 载荷要能被 JSON  round-trip：界面预览与后续落库都依赖它。
func TestInstallResultRoundTrips(t *testing.T) {
	result := InstallResult{Kind: KindBalance, Name: "n", Target: "collector source 12"}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back InstallResult
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Kind != KindBalance || back.Name != "n" || back.Target != "collector source 12" {
		t.Errorf("round-trip 丢失字段：%+v", back)
	}
}
