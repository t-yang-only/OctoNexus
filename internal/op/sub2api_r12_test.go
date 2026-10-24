package op

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R12-F1: 停用账号必须在三个入口(probe/sync/clean-stale)一致拒绝。
// 停用意味着上游欠费/故障/主动下线, 白打上游只会浪费配额并可能触发对方风控。
// 装置让目标只受该守卫保护: 同一账号 enabled=false 时三条路径各自报可执行错误。
func TestSub2APIDisabledAccountGuardsAllPaths(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-alpha"}]}`))
	}))
	defer upstream.Close()

	ctx := context.Background()
	created, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "停用守卫账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-disabled-test",
		Enabled:     boolPtr(false), // 直接建成停用态
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}

	// probe: 停用即拒
	if _, err := Sub2APIAccountProbe(ctx, created.ID); err == nil || !strings.Contains(err.Error(), "已停用") {
		t.Fatalf("停用账号探测应被拒绝, 实际: %v", err)
	}

	// sync: 停用即拒(注意守卫在凭据检查之前, 停用态账号也可能配了凭据)
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err == nil || !strings.Contains(err.Error(), "已停用") {
		t.Fatalf("停用账号同步应被拒绝, 实际: %v", err)
	}

	// clean-stale: 停用即拒(先给一个非空 stale 摘要, 排除"无待清理"分支干扰)
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
		Update("last_sync_summary", `{"added_count":0,"stale_count":1,"stale":["model-alpha"]}`).Error; err != nil {
		t.Fatalf("写入摘要失败: %v", err)
	}
	if _, err := Sub2APIAccountCleanStaleModels(ctx, created.ID); err == nil || !strings.Contains(err.Error(), "已停用") {
		t.Fatalf("停用账号清理应被拒绝, 实际: %v", err)
	}

	// 启用后放行(守卫不误伤正常账号)
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
		Update("enabled", true).Error; err != nil {
		t.Fatalf("启用账号失败: %v", err)
	}
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
		t.Fatalf("启用后同步应放行: %v", err)
	}
}
