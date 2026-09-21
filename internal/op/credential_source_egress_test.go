package op

import (
	"context"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// TestCredentialSourceEgressPersistsOnUpdate 钉住 2026-09-20 实测复现的缺陷：
//
// 只改「出口节点」而不重发 site 时，出口必须落库。
// 原实现把 row.ProxyNodeID = in.ProxyNodeID 写在 if in.Site != "" 分支里，
// 于是面板那个"改绑出口"的下拉返回 200 却什么也没改 —— 再点采集还是走到旧节点/报同一句错。
//
// 同时守住反向的坑：**没给** proxy_node_id 的更新不得把出口清成直连
// （那等于把使用者的真实 IP 悄悄暴露给站点，是这个功能唯一不可接受的失败方式）。
func TestCredentialSourceEgressPersistsOnUpdate(t *testing.T) {
	ctx := context.Background()
	collectorTestDB(t)

	pack := `{"name":"t","version":"1.0.0","hosts":["relay.example.com"],"units":"usd","read":[{"name":"b","url":"https://relay.example.com/api/user/self","extract":[{"field":"balance","from":"json","path":"data.balance"}]}]}`
	first := 11
	created, err := CredentialSourceSave(ctx, 0, model.CredentialSourceInput{
		Name: "出口测试", Kind: model.CredentialKindPack, Site: "https://relay.example.com",
		Pack: pack, ProxyNodeID: &first,
	})
	if err != nil {
		t.Fatalf("建源失败：%v", err)
	}
	if created.ProxyNodeID != 11 {
		t.Fatalf("建源应落出口 11，实得 %d", created.ProxyNodeID)
	}

	second := 22
	updated, err := CredentialSourceSave(ctx, created.ID, model.CredentialSourceInput{ProxyNodeID: &second})
	if err != nil {
		t.Fatalf("改出口失败：%v", err)
	}
	if updated.ProxyNodeID != 22 {
		t.Fatalf("只改出口应落库为 22，实得 %d（面板会显示成功但实际没改）", updated.ProxyNodeID)
	}

	// 不回传该字段的更新（例如只切换开关）不得清掉出口
	if _, err := CredentialSourceSave(ctx, created.ID, model.CredentialSourceInput{Kind: model.CredentialKindPack}); err != nil {
		t.Fatalf("改形态失败：%v", err)
	}
	after, err := CredentialSourceGet(ctx, created.ID)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if after.ProxyNodeID != 22 {
		t.Fatalf("没给 proxy_node_id 的更新不得清出口，实得 %d", after.ProxyNodeID)
	}

	// 显式传 0 = 改回直连（必须允许，否则改不回来）
	zero := 0
	if _, err := CredentialSourceSave(ctx, created.ID, model.CredentialSourceInput{ProxyNodeID: &zero}); err != nil {
		t.Fatalf("改回直连失败：%v", err)
	}
	final, err := CredentialSourceGet(ctx, created.ID)
	if err != nil {
		t.Fatalf("读回失败：%v", err)
	}
	if final.ProxyNodeID != 0 {
		t.Fatalf("显式 0 应改回直连，实得 %d", final.ProxyNodeID)
	}
}
