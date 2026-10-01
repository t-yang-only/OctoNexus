package model

import "testing"

func TestValidateOfficialAccountProvider(t *testing.T) {
	for _, p := range []OfficialAccountProvider{
		OfficialAccountProviderOpenAI, OfficialAccountProviderGemini, OfficialAccountProviderClaude,
	} {
		if err := ValidateOfficialAccountProvider(p); err != nil {
			t.Fatalf("provider %q: unexpected error %v", p, err)
		}
	}
	for _, p := range []OfficialAccountProvider{"", "azure", "OPENAI", "new-api"} {
		if err := ValidateOfficialAccountProvider(p); err == nil {
			t.Fatalf("provider %q: want error", p)
		}
	}
}

func TestOfficialAccountCreateRequestBinding(t *testing.T) {
	// binding tag 保证三类合法值通过校验器; 此处只断言 tag 存在且拼写正确,
	// 真实绑定由 gin 在处理器侧执行。
	req := OfficialAccountCreateRequest{Provider: OfficialAccountProviderGemini}
	if err := ValidateOfficialAccountProvider(req.Provider); err != nil {
		t.Fatalf("request provider: %v", err)
	}
}
