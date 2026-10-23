package op

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R6-F1: 同步结果必须可观测 —— Added 只报本次新并入, Stale 只报上游已下架。
// 关键不变量: Stale 仅罗列, 绝不在 sync 内删除对应模型(人工配置保护, R3-F1 教训)。
func TestSub2APISyncModelsReportsAddedAndStale(t *testing.T) {
	_, restore := openSub2APITestDB(t)
	defer restore()

	serve := `{"data":[{"id":"model-alpha"},{"id":"model-beta"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(serve))
	}))
	defer upstream.Close()

	ctx := context.Background()
	created, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "可观测同步账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-observe-test",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}

	// 首次: 两个全新模型, Stale 必须为空
	first, err := Sub2APIAccountSyncModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("首次同步失败: %v", err)
	}
	if len(first.Added) != 2 || len(first.Stale) != 0 {
		t.Fatalf("首次同步 Added 应为 2、Stale 应为 0, 实际 added=%v stale=%v", first.Added, first.Stale)
	}

	// 上游扩容: Added 只报新增的那一个, 不得把旧模型算成新增
	serve = `{"data":[{"id":"model-alpha"},{"id":"model-beta"},{"id":"model-gamma"}]}`
	second, err := Sub2APIAccountSyncModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("二次同步失败: %v", err)
	}
	if len(second.Added) != 1 || second.Added[0] != "model-gamma" {
		t.Fatalf("二次同步 Added 应仅含 model-gamma, 实际: %v", second.Added)
	}
	if len(second.Stale) != 0 {
		t.Fatalf("上游扩容时不应有 Stale, 实际: %v", second.Stale)
	}

	// 上游缩容: Stale 报出下架模型, 且这些模型必须仍留在渠道里(只报告不删除)
	serve = `{"data":[{"id":"model-alpha"}]}`
	third, err := Sub2APIAccountSyncModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("三次同步失败: %v", err)
	}
	if len(third.Added) != 0 {
		t.Fatalf("上游缩容时 Added 应为空, 实际: %v", third.Added)
	}
	if len(third.Stale) != 2 {
		t.Fatalf("上游缩容时 Stale 应报出 beta/gamma 两个, 实际: %v", third.Stale)
	}
	staleSet := make(map[string]struct{}, len(third.Stale))
	for _, m := range third.Stale {
		staleSet[m] = struct{}{}
	}
	for _, want := range []string{"model-beta", "model-gamma"} {
		if _, ok := staleSet[want]; !ok {
			t.Fatalf("Stale 缺少 %s: %v", want, third.Stale)
		}
	}
	have := make(map[string]struct{}, len(third.Models))
	for _, m := range third.Models {
		have[m] = struct{}{}
	}
	for _, want := range []string{"model-alpha", "model-beta", "model-gamma"} {
		if _, ok := have[want]; !ok {
			t.Fatalf("下架模型 %s 被 sync 删除(违反只报告不删除): %v", want, third.Models)
		}
	}
}
