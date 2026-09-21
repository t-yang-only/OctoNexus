package op

import (
	"net/url"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestOfficialAuthorizeURLCarriesScope 钉住 2026-09-20 用户实测的缺陷：
//
// 点「接入官方账号（Gemini）」被 Google 直接拒：
// `400 invalid_request: Missing required parameter: scope`。
// 原因是 authorize URL 里一个 scope 参数都没有 —— 授权页根本走不过去。
//
// 三家都必须带 scope；Gemini 还必须带 access_type=offline（否则不发 refresh_token）
// 与 prompt=consent（否则复用旧授权时也不发）。
func TestOfficialAuthorizeURLCarriesScope(t *testing.T) {
	conn := openImportTestDB(t)
	t.Setenv("OCTOPUS_OFFICIAL_KEY", strings.Repeat("k", 32))
	for _, provider := range []model.OfficialAccountProvider{
		model.OfficialAccountProviderOpenAI,
		model.OfficialAccountProviderClaude,
		model.OfficialAccountProviderGemini,
	} {
		_, authorizeURL, _, err := OfficialAccountAuthorize(conn, provider)
		if err != nil {
			t.Fatalf("provider %s 授权被拒：%v", provider, err)
		}
		parsed, err := url.Parse(authorizeURL)
		if err != nil {
			t.Fatalf("provider %s 授权 URL 不可解析：%v", provider, err)
		}
		query := parsed.Query()
		scope := query.Get("scope")
		if strings.TrimSpace(scope) == "" {
			t.Errorf("provider %s 的授权 URL 缺 scope（授权端点会直接 400）：%s", provider, authorizeURL)
		}
		if want := officialOAuthScopeFor(provider); want != "" && scope != want {
			t.Errorf("provider %s 的 scope 应为 %q，实得 %q", provider, want, scope)
		}
		if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
			t.Errorf("provider %s 仍必须是 PKCE(S256)", provider)
		}
		if provider == model.OfficialAccountProviderGemini {
			if query.Get("access_type") != "offline" {
				t.Errorf("Gemini 必须 access_type=offline（否则不发 refresh_token），实得 %q", query.Get("access_type"))
			}
			if query.Get("prompt") != "consent" {
				t.Errorf("Gemini 必须 prompt=consent，实得 %q", query.Get("prompt"))
			}
		}
	}
}

// 未知 provider 不编造 scope（与客户端 ID 同一条纪律）。
func TestOfficialOAuthScopeUnknownProvider(t *testing.T) {
	if got := officialOAuthScopeFor(model.OfficialAccountProvider("nope")); got != "" {
		t.Fatalf("未知 provider 不应有 scope，got=%q", got)
	}
}
