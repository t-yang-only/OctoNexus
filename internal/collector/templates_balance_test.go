package collector

import (
	"testing"
)

// TestNewAPITemplateTreatsQuotaAsRemaining 钉住 2026-09-20 真实站点实测发现的余额口径缺陷：
//
// new-api / one-api 的 data.quota 是**剩余额度**本身，data.used_quota 是累计已用。
// 若把它当"总额度"交给 applyFields 走「总额度 − 已用」的推导分支，读数会变成负数
// （实测 api.uu6.top：6300000 − 194200000 = -187900000 点 ≈ -375.8 美元）。
//
// 因此模板必须：① 直接以 balance 收取 data.quota；② 不再声明 quota 规则（否则推导分支被激活）；
// ③ 用 Bearer 令牌读账（只带 Cookie 实测读不到）。
func TestNewAPITemplateTreatsQuotaAsRemaining(t *testing.T) {
	for _, kind := range []string{"new-api", "one-api"} {
		pack, err := BuildTemplate(kind, "https://relay.example.com/api", LoginModeForm)
		if err != nil {
			t.Fatalf("%s 模板生成失败：%v", kind, err)
		}
		if len(pack.Read) != 1 {
			t.Fatalf("%s 期望 1 个读步骤，实得 %d", kind, len(pack.Read))
		}
		step := pack.Read[0]
		var balancePath, quotaPath, usedPath string
		for _, rule := range step.Extract {
			switch rule.Field {
			case FieldBalance:
				balancePath = rule.Path
			case FieldQuota:
				quotaPath = rule.Path
			case FieldUsed:
				usedPath = rule.Path
			}
		}
		if balancePath != "data.quota" {
			t.Errorf("%s：余额应直接取 data.quota（剩余额度），实得 %q", kind, balancePath)
		}
		if quotaPath != "" {
			t.Errorf("%s：不应声明 quota 规则，否则会走「总额度 − 已用」推导，实得 %q", kind, quotaPath)
		}
		if usedPath != "data.used_quota" {
			t.Errorf("%s：已用应取 data.used_quota，实得 %q", kind, usedPath)
		}
		if kind == "new-api" {
			if got := step.Headers["Authorization"]; got != "Bearer {"+VarToken+"}" {
				t.Errorf("new-api：读账必须带 Bearer 令牌，实得 %q", got)
			}
			if pack.Login == nil || len(pack.Login.Extract) != 1 || pack.Login.Extract[0].Path != "data.access_token" {
				t.Errorf("new-api：登录步骤必须从 data.access_token 提取令牌")
			}
		}
		if pack.Units != UnitsPoints {
			t.Errorf("%s：单位必须是点口径，实得 %q", kind, pack.Units)
		}
	}
}

// TestAIGatewayTemplateReadsBalance 钉住新增的 SPA 站点族契约：
// 六家实测同一套产品 —— POST /api/v1/auth/login（邮箱）换 data.access_token，
// GET /api/v1/auth/me（Bearer）的 data.balance 就是美元余额。
func TestAIGatewayTemplateReadsBalance(t *testing.T) {
	pack, err := BuildTemplate("ai-gateway", "https://relay.example.com/login", LoginModeForm)
	if err != nil {
		t.Fatalf("ai-gateway 模板生成失败：%v", err)
	}
	if pack.Units != UnitsUSD {
		t.Fatalf("ai-gateway 必须是美元口径（站点报的就是美元），实得 %q", pack.Units)
	}
	if pack.Login == nil || pack.Login.URL != "https://relay.example.com/api/v1/auth/login" {
		t.Fatalf("登录端点应为 /api/v1/auth/login，实得 %+v", pack.Login)
	}
	if len(pack.Read) != 1 {
		t.Fatalf("期望 1 个读步骤，实得 %d", len(pack.Read))
	}
	step := pack.Read[0]
	if step.URL != "https://relay.example.com/api/v1/auth/me" {
		t.Errorf("读账端点应为 /api/v1/auth/me，实得 %q", step.URL)
	}
	if got := step.Headers["Authorization"]; got != "Bearer {"+VarToken+"}" {
		t.Errorf("读账必须带 Bearer 令牌，实得 %q", got)
	}
	if len(step.Extract) != 1 || step.Extract[0].Field != FieldBalance || step.Extract[0].Path != "data.balance" {
		t.Errorf("余额应取 data.balance，实得 %+v", step.Extract)
	}
	// 交互登录形态也必须能装载：它在登录步骤里不进 URL，但读账仍引用 {token} ——
	// 令牌由反代在服务端捕获（见 LoginProxy.captureToken）。这条路径曾在门禁处被拒过。
	if _, err := BuildTemplate("ai-gateway", "https://relay.example.com", LoginModeInteractive); err != nil {
		t.Fatalf("ai-gateway 交互形态必须能过装载门禁（令牌由反代捕获）：%v", err)
	}
}
