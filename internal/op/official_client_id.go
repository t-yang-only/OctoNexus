package op

import (
	"net/url"
	"os"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
)

// 官方账号接入（T-official-*）的 OAuth 客户端 ID 来源。
//
// 这些是**公开客户端**（PKCE 流程、无 client_secret）：官方 CLI 与同类开源工具用的是同一份值
// （参考实现在 reference/api-monitor/internal/api/account_oauth.go 里也是硬编码同一组），
// 所以它们不是密钥，内置不构成泄密。
//
// 为什么必须内置：只认环境变量时，用户点"接入官方账号"看到的是
// `official OAuth client id not configured for openai (set OCTOPUS_OFFICIAL_CLIENT_ID_OPENAI)`
// —— 一句他无从下手的话（去哪注册？填什么？），功能等于不可用。内置后开箱即用，
// 需要换成自家注册的客户端 ID 时用环境变量覆盖即可。
var officialOAuthClientIDs = map[model.OfficialAccountProvider]string{
	model.OfficialAccountProviderOpenAI: "app_EMoamEEZ73f0CkXaXp7hrann",
	model.OfficialAccountProviderClaude: "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
	model.OfficialAccountProviderGemini: "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com",
}

// officialOAuthClientID 解析某 provider 的 OAuth 客户端 ID：环境变量优先，其次内置缺省。
// 两者都没有时返回空串，由调用方给出可操作的错误。
func officialOAuthClientID(provider model.OfficialAccountProvider) string {
	env := "OCTOPUS_OFFICIAL_CLIENT_ID_" + strings.ToUpper(string(provider))
	if v := strings.TrimSpace(os.Getenv(env)); v != "" {
		return v
	}
	return officialOAuthClientIDs[provider]
}

// officialOAuthClientIDEnv 返回该 provider 对应的环境变量名，用于错误提示。
func officialOAuthClientIDEnv(provider model.OfficialAccountProvider) string {
	return "OCTOPUS_OFFICIAL_CLIENT_ID_" + strings.ToUpper(string(provider))
}

// officialOAuthScopes 是三家公开 PKCE 客户端各自要求的授权范围。
//
// 为什么必须显式给：Google 的授权端点在缺 scope 时直接回
// `400 invalid_request: Missing required parameter: scope`（用户实测）。
// 这三个值分别对应各自 CLI 官方登录流程使用的范围：
//   - OpenAI（Codex CLI）：openid/profile/email + offline_access 才拿得到 refresh_token
//   - Claude（Claude Code）：user:profile / user:inference / user:sessions:claude_code
//   - Gemini（Gemini CLI）：openid + cloud-platform + 邮箱与资料
var officialOAuthScopes = map[model.OfficialAccountProvider]string{
	model.OfficialAccountProviderOpenAI: "openid profile email offline_access",
	model.OfficialAccountProviderClaude: "user:profile user:inference user:sessions:claude_code",
	model.OfficialAccountProviderGemini: "openid https://www.googleapis.com/auth/cloud-platform " +
		"https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile",
}

// officialOAuthExtraParams 是 provider 特有的额外授权参数。
// Google 必须 access_type=offline（否则不发 refresh_token）且 prompt=consent（否则复用旧授权时也不发）。
func officialOAuthExtraParams(provider model.OfficialAccountProvider, q url.Values) {
	if provider == model.OfficialAccountProviderGemini {
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
	}
}

// officialOAuthScopeFor 返回该 provider 的授权范围（未知 provider 返回空串，不编造）。
func officialOAuthScopeFor(provider model.OfficialAccountProvider) string {
	return officialOAuthScopes[provider]
}
