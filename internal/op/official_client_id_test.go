package op

import (
	"os"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// 官方账号接入的 OAuth 客户端 ID 解析（R-official-001）。
//
// 起因是一次实测失败：用户点"接入官方账号"得到
// `official OAuth client id not configured for openai (set OCTOPUS_OFFICIAL_CLIENT_ID_OPENAI)`
// —— 一句他无从下手的话（去哪注册？填什么？），功能等于不可用。
// 现在缺省内置公开客户端 ID（PKCE、无 secret，官方 CLI 与同类开源工具用同一份），
// 环境变量仍可覆盖成自家注册的客户端。
func TestOfficialOAuthClientIDDefaultsAndOverride(t *testing.T) {
	for _, provider := range []model.OfficialAccountProvider{
		model.OfficialAccountProviderOpenAI,
		model.OfficialAccountProviderClaude,
		model.OfficialAccountProviderGemini,
	} {
		env := officialOAuthClientIDEnv(provider)
		t.Setenv(env, "")
		if got := officialOAuthClientID(provider); got == "" {
			t.Fatalf("provider %s: 内置缺省为空，用户仍会看到无从下手的配置错误", provider)
		}
		t.Setenv(env, "  my-own-client-id  ")
		if got := officialOAuthClientID(provider); got != "my-own-client-id" {
			t.Fatalf("provider %s: 环境变量未生效（去除空白后应优先），got=%q", provider, got)
		}
		t.Setenv(env, "")
	}
}

// 未登记的 provider 不编造客户端 ID：宁可报错，也不能拿别的 provider 的 ID 去换令牌。
func TestOfficialOAuthClientIDUnknownProvider(t *testing.T) {
	if got := officialOAuthClientID(model.OfficialAccountProvider("nope")); got != "" {
		t.Fatalf("未知 provider 不应有内置客户端 ID，got=%q", got)
	}
}

// 授权 URL 必须真的带上 client_id：内置缺省生效后，这一条是"点得下去"的判据。
func TestOfficialAuthorizeURLCarriesBuiltinClientID(t *testing.T) {
	conn := openImportTestDB(t)
	t.Setenv("OCTOPUS_OFFICIAL_CLIENT_ID_OPENAI", "")
	// 加密密钥：授权流程要求先能加密（缺密钥时它按设计先拒绝），这里给一把临时密钥。
	keyPath := t.TempDir() + "/credential.key"
	t.Setenv("OCTOPUS_OFFICIAL_KEY", strings.Repeat("k", 32))
	_ = keyPath

	account, authorizeURL, state, err := OfficialAccountAuthorize(conn, model.OfficialAccountProviderOpenAI)
	if err != nil {
		t.Fatalf("缺省客户端 ID 下授权仍被拒: %v", err)
	}
	if account.ID == 0 || state == "" {
		t.Fatalf("pending 账号未落库: account=%+v state=%q", account, state)
	}
	if !strings.Contains(authorizeURL, "client_id=app_") {
		t.Fatalf("授权 URL 没带内置 client_id: %s", authorizeURL)
	}
	if !strings.Contains(authorizeURL, "code_challenge=") {
		t.Fatalf("授权 URL 缺 PKCE challenge: %s", authorizeURL)
	}
}

// 余额读不到时"原因"必须落到渠道上——面板只报"未读到"而说不出为什么，用户无从下手（本轮实测）。
func TestBalanceReasonRecordedAndCleared(t *testing.T) {
	before := ChannelBalanceReasons()
	t.Cleanup(func() {
		for id := range ChannelBalanceReasons() {
			RecordChannelBalanceReason(id, "", "")
		}
		for id, row := range before {
			RecordChannelBalanceReason(id, row.Key, row.Text)
		}
	})

	RecordChannelBalanceReason(4242, "no_endpoint", "该站点没有余额接口（HTTP 404）")
	key, text := ChannelBalanceReason(4242)
	if key != "no_endpoint" || !strings.Contains(text, "404") {
		t.Fatalf("原因未落上: key=%q text=%q", key, text)
	}
	if row, ok := ChannelBalanceReasons()[4242]; !ok || row.Key != "no_endpoint" {
		t.Fatalf("汇总未带走该原因: %+v", ChannelBalanceReasons())
	}
	// 读到之后必须清除，否则面板会一直显示上一轮的解释。
	RecordChannelBalanceReason(4242, "", "")
	if key, text := ChannelBalanceReason(4242); key != "" || text != "" {
		t.Fatalf("成功读取后原因未清除: key=%q text=%q", key, text)
	}
}

// 余额接口路径设置：空/非法一律回落 new-api 默认路径，不接受半截配置。
func TestBalanceEndpointPathFallback(t *testing.T) {
	if got := BalanceEndpointPath(); got != model.BalanceUserSelfPathDefault {
		t.Fatalf("默认路径 = %q, want %q", got, model.BalanceUserSelfPathDefault)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
