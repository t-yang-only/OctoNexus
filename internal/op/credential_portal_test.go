package op

import (
	"context"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestCollectorPortalTokenFlow 钉住 2026-09-20 用户实测的缺陷：
//
// 面板给出的登录临时地址挂在 middleware.Auth 下，用户在自己浏览器里打开
// 只看到 `401 {"code":401,"message":"Authentication failed"}`（表现为一张空白页）。
// 现在地址改成带一次性令牌的公开路径：拿到链接就能打开，令牌按源绑定、完成登录后立即失效。
func TestCollectorPortalTokenFlow(t *testing.T) {
	ctx := context.Background()
	collectorTestDB(t)

	pack := `{"name":"t","version":"1.0.0","hosts":["relay.example.com"],"units":"usd","login":{"mode":"interactive"},"read":[{"name":"b","url":"https://relay.example.com/api/v1/user/profile","headers":{"Authorization":"Bearer {token}"},"extract":[{"field":"balance","from":"json","path":"data.balance"}]}]}`
	detail, err := CredentialSourceSave(ctx, 0, model.CredentialSourceInput{
		Name: "门户令牌", Kind: model.CredentialKindLogin, Site: "https://relay.example.com", Pack: pack,
	})
	if err != nil {
		t.Fatalf("建源失败：%v", err)
	}

	started, err := CredentialSourceStartLogin(ctx, detail.ID)
	if err != nil {
		t.Fatalf("开始登录失败：%v", err)
	}
	if !strings.Contains(started.LoginURL, "/api/v1/collector/portal/") {
		t.Fatalf("临时地址必须走公开门户（带令牌），实得 %q", started.LoginURL)
	}
	parts := strings.Split(strings.TrimSuffix(started.LoginURL, "/"), "/")
	token := parts[len(parts)-1]
	if len(token) < 24 {
		t.Fatalf("令牌太短，容易被猜：%q", token)
	}
	if !CollectorLoginTokenMatches(detail.ID, token) {
		t.Fatalf("刚发出的令牌必须能校验通过：%q", token)
	}
	if CollectorLoginTokenMatches(detail.ID, "not-the-token") {
		t.Fatal("错误令牌必须被拒")
	}
	if CollectorLoginTokenMatches(detail.ID, "") {
		t.Fatal("空令牌必须被拒")
	}
	if CollectorLoginTokenMatches(detail.ID+999, token) {
		t.Fatal("令牌必须按源绑定：别的源不能拿它打开")
	}

	// 令牌必须能撤销：显式清除会话后旧地址立即失效（一次性地址）。
	//
	// 注意"完成登录失败"时**不**撤销令牌是有意的：站点还没捕获到会话时，
	// 用户往往需要刷新登录页重试，此时把链接作废等于逼他回面板重新生成。
	if _, err := CredentialSourceClearSession(ctx, detail.ID); err != nil {
		t.Fatalf("清除会话失败：%v", err)
	}
	if CollectorLoginTokenMatches(detail.ID, token) {
		t.Fatal("清除会话后旧令牌必须失效（一次性地址）")
	}
}
