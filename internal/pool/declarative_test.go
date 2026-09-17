package pool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 声明式适配器的判据分两块：**边界**（什么不许注册）与**映射**（注册之后能不能把外部形状读成统一视图）。
// 边界部分刻意覆盖"白名单为空"，因为那是默认状态——默认必须是谁都接不进来。

func withGuards(t *testing.T, hosts ...string) {
	t.Helper()
	previous := DeclarativeGuardsOf()
	SetDeclarativeGuards(DeclarativeGuards{AllowedHosts: hosts})
	t.Cleanup(func() { SetDeclarativeGuards(previous) })
}

func baseSpec() DeclarativeSpec {
	return DeclarativeSpec{
		Kind:         "custom-test",
		Title:        "测试工具包",
		BaseURL:      "https://api.example.com",
		Capabilities: []Capability{CapList, CapGet},
		List: DeclarativeList{
			Path:      "/api/token/?p=0",
			ItemsPath: "data.items",
			Fields:    map[string]string{"id": "id", "name": "name"},
		},
	}
}

// 默认（白名单为空）必须拒绝：这是这个扩展点的安全基线。
func TestDeclarativeRejectsEverythingWithoutAllowlist(t *testing.T) {
	withGuards(t) // 空名单
	err := ValidateDeclarativeSpec(baseSpec())
	if !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("白名单为空时应拒绝注册, 实际: %v", err)
	}
	if _, err := NewDeclarativeAdapter(baseSpec()); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("构造也应被拒绝, 实际: %v", err)
	}
}

// 白名单要按后缀命中，且不能靠"看起来像"绕过。
func TestDeclarativeHostAllowlistMatching(t *testing.T) {
	withGuards(t, "example.com")
	if err := ValidateDeclarativeSpec(baseSpec()); err != nil {
		t.Fatalf("example.com 应命中白名单: %v", err)
	}
	suffix := baseSpec()
	suffix.BaseURL = "https://relay.example.com"
	if err := ValidateDeclarativeSpec(suffix); err != nil {
		t.Fatalf("后缀应命中白名单: %v", err)
	}
	other := baseSpec()
	other.BaseURL = "https://api.example.org"
	if err := ValidateDeclarativeSpec(other); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("其它域名应被拒绝, 实际: %v", err)
	}
	// 只有 list.path 指向白名单外时也要拒绝（一个 spec 不能借绝对 URL 跳出白名单）。
	escape := baseSpec()
	escape.List.Path = "http://evil.example.net/steal"
	if err := ValidateDeclarativeSpec(escape); !errors.Is(err, ErrHostNotAllowed) {
		t.Fatalf("list.path 的绝对 URL 越界应被拒绝, 实际: %v", err)
	}
}

// 声明式只读：写能力位（含 sync）一律拒绝，指向"要写就写内置适配器"。
func TestDeclarativeRejectsWriteCapabilities(t *testing.T) {
	withGuards(t, "example.com")
	for _, capability := range []Capability{CapSync, CapToggle, CapProvision, CapRevoke, CapRefresh, CapProbe} {
		spec := baseSpec()
		spec.Capabilities = []Capability{CapList, capability}
		err := ValidateDeclarativeSpec(spec)
		if !errors.Is(err, ErrInvalidAdapter) || !strings.Contains(err.Error(), "只读能力") {
			t.Fatalf("能力位 %s 应被拒绝, 实际: %v", capability, err)
		}
	}
}

// 形状校验：kind 前缀、协议、必填项与认证三型。
func TestDeclarativeSpecShapeValidation(t *testing.T) {
	withGuards(t, "example.com")
	cases := []struct {
		name   string
		mutate func(*DeclarativeSpec)
		expect string
	}{
		{"kind 前缀", func(s *DeclarativeSpec) { s.Kind = "new-api" }, "custom-"},
		{"协议", func(s *DeclarativeSpec) { s.BaseURL = "ftp://api.example.com" }, "http/https"},
		{"列表路径", func(s *DeclarativeSpec) { s.List.Path = "" }, "list.path"},
		{"bearer 缺令牌", func(s *DeclarativeSpec) { s.Auth.Type = "bearer" }, "token"},
		{"login 缺 token_path", func(s *DeclarativeSpec) {
			s.Auth = DeclarativeAuth{Type: "login", LoginURL: "/api/user/login"}
			s.Secret = DeclarativeSecret{Username: "u", Password: "p"}
		}, "token_path"},
		{"login 缺凭据", func(s *DeclarativeSpec) {
			s.Auth = DeclarativeAuth{Type: "login", LoginURL: "/api/user/login", TokenPath: "data.token"}
		}, "username"},
		{"未知认证类型", func(s *DeclarativeSpec) { s.Auth.Type = "magic" }, "auth.type"},
	}
	for _, tc := range cases {
		spec := baseSpec()
		tc.mutate(&spec)
		err := ValidateDeclarativeSpec(spec)
		if err == nil {
			t.Fatalf("%s: 应报错", tc.name)
		}
		if !strings.Contains(err.Error(), tc.expect) {
			t.Fatalf("%s: 错误信息应提到 %q, 实际: %v", tc.name, tc.expect, err)
		}
	}
}

// 注册表：运行时适配器可替换、可移除；内置 kind 既不能被覆盖也不能被移除。
func TestRegisterRuntimeReplacesAndUnregisters(t *testing.T) {
	withGuards(t, "127.0.0.1")
	spec := baseSpec()
	spec.Kind = "custom-runtime-test"
	spec.BaseURL = "http://127.0.0.1:9"
	adapter, err := NewDeclarativeAdapter(spec)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	if err := RegisterRuntime(adapter); err != nil {
		t.Fatalf("注册失败: %v", err)
	}
	unregistered := false
	defer func() {
		if !unregistered {
			_ = UnregisterRuntime(spec.Kind)
		}
	}()
	if !Has(spec.Kind) {
		t.Fatal("注册后应能在注册表里找到")
	}
	// 同名替换：声明式 spec 是用户可改的，改完要能当场生效。
	spec.Title = "改过的标题"
	replacement, err := NewDeclarativeAdapter(spec)
	if err != nil {
		t.Fatalf("重建失败: %v", err)
	}
	if err := RegisterRuntime(replacement); err != nil {
		t.Fatalf("替换失败: %v", err)
	}
	for _, info := range Kinds() {
		if info.Kind == spec.Kind && info.Title != "改过的标题" {
			t.Fatalf("替换后标题应更新, 实际: %s", info.Title)
		}
	}
	if BuiltinKind(spec.Kind) {
		t.Fatal("运行时适配器不该被当成内置")
	}
	if err := UnregisterRuntime(spec.Kind); err != nil {
		t.Fatalf("移除失败: %v", err)
	}
	unregistered = true
	if Has(spec.Kind) {
		t.Fatal("移除后不该还在注册表里")
	}
	if err := UnregisterRuntime(spec.Kind); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("重复移除应报未知 kind, 实际: %v", err)
	}
}

// 内置适配器（官方账号 / 渠道凭据）不能被一份 JSON 顶掉，也不能被删掉。
func TestRegisterRuntimeRefusesBuiltinKind(t *testing.T) {
	var builtin AdapterInfo
	for _, info := range Kinds() {
		if info.Builtin {
			builtin = info
			break
		}
	}
	if builtin.Kind == "" {
		t.Skip("没有内置适配器可测")
	}
	withGuards(t, "127.0.0.1")
	spec := baseSpec()
	spec.Kind = builtin.Kind // 故意用内置 kind（前缀校验会先拦，这里绕过以测注册表本身）。
	adapter := &stubAdapter{info: AdapterInfo{Kind: builtin.Kind, Title: "冒名顶替", Capabilities: []Capability{CapList}}}
	if err := RegisterRuntime(adapter); !errors.Is(err, ErrDuplicateKind) {
		t.Fatalf("内置 kind 应被拒绝覆盖, 实际: %v", err)
	}
	if err := UnregisterRuntime(builtin.Kind); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("内置 kind 应被拒绝移除, 实际: %v", err)
	}
	_ = spec
}

type stubAdapter struct{ info AdapterInfo }

func (stub *stubAdapter) Info() AdapterInfo { return stub.info }
func (stub *stubAdapter) Entries(context.Context) ([]Entry, error) {
	return []Entry{{Kind: stub.info.Kind, ID: "stub", Name: "stub"}}, nil
}

// 映射：外部 JSON 形状 → 统一视图，且认证头确实带上了（但不回显）。
func TestDeclarativeEntriesMapsFieldsAndAuth(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"items":[
			{"id":"k1","name":"key-one","status":1,"expired_time":"2030-01-02T03:04:05Z"},
			{"id":"k2","name":"key-two","status":2}
		]}}`))
	}))
	defer server.Close()

	withGuards(t, "127.0.0.1")
	spec := DeclarativeSpec{
		Kind:         "custom-map-test",
		Title:        "映射测试",
		BaseURL:      server.URL,
		Capabilities: []Capability{CapList},
		Auth:         DeclarativeAuth{Type: "bearer"},
		Secret:       DeclarativeSecret{Token: "tok-123"},
		List: DeclarativeList{
			Path:      "/api/token/",
			ItemsPath: "data.items",
			Fields: map[string]string{
				"id": "id", "name": "name", "status": "status",
				"enabled": "status", "expires_at": "expired_time",
			},
		},
	}
	adapter, err := NewDeclarativeAdapter(spec)
	if err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	entries, err := adapter.Entries(context.Background())
	if err != nil {
		t.Fatalf("取条目失败: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("应取到 2 条, 实际 %d", len(entries))
	}
	if entries[0].ID != "k1" || entries[0].Name != "key-one" || !entries[0].Enabled {
		t.Fatalf("第一条映射不对: %+v", entries[0])
	}
	if entries[0].ExpiresAt == nil || entries[0].ExpiresAt.Year() != 2030 {
		t.Fatalf("到期时间应被解析: %+v", entries[0].ExpiresAt)
	}
	if entries[1].Enabled {
		t.Fatal("status=2 不应算启用（enabled 映射的字段值不是 1/true/enabled）")
	}
	if gotAuth != "Bearer tok-123" {
		t.Fatalf("认证头应带上 bearer 令牌, 实际: %q", gotAuth)
	}
	// 自描述里不能出现凭据。
	encoded, err := json.Marshal(adapter.Info())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "tok-123") {
		t.Fatalf("自描述里泄漏了令牌: %s", encoded)
	}
}

// 3xx 不跟跳：否则白名单可以被一个 302 绕过。
func TestDeclarativeDoesNotFollowRedirect(t *testing.T) {
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer redirectTarget.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/elsewhere", http.StatusFound)
	}))
	defer server.Close()

	withGuards(t, "127.0.0.1")
	spec := baseSpec()
	spec.Kind = "custom-redirect-test"
	spec.BaseURL = server.URL
	spec.List.Path = "/api/token/"
	spec.List.ItemsPath = "data"
	if _, err := NewDeclarativeAdapter(spec); err != nil {
		t.Fatalf("构造失败: %v", err)
	}
	adapter, _ := NewDeclarativeAdapter(spec)
	if _, err := adapter.Entries(context.Background()); err == nil {
		t.Fatal("3xx 应当作失败，不能跟跳")
	}
}

// 守卫：非法能力位与未知能力位都不能进注册表（配置改了也要拦住）。
func TestDeclarativeRejectsUnknownCapability(t *testing.T) {
	withGuards(t, "example.com")
	spec := baseSpec()
	spec.Capabilities = []Capability{CapList, Capability("teleport")}
	if err := ValidateDeclarativeSpec(spec); !errors.Is(err, ErrInvalidAdapter) {
		t.Fatalf("未知能力位应被拒绝, 实际: %v", err)
	}
}
