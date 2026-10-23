package op

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R8-F1: 账号级最小间隔保护 —— 误连打不得把请求反复打到上游。
// 装置让目标只受该守卫保护: 首次同步放行; 刚成功后立即再同步必须被拒且告知剩余等待;
// 越过间隔后放行。零值 LastSyncAt 不拦截。
func TestSub2APISyncModelsMinIntervalGuard(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-alpha"}]}`))
	}))
	defer upstream.Close()

	ctx := context.Background()
	created, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "最小间隔账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-interval-test",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}

	// 首次同步: 从未同步过, 必须放行
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
		t.Fatalf("首次同步被误拦: %v", err)
	}

	// 间隔内立即再同步: 必须拒绝, 且错误可执行(含剩余秒数)
	_, err = Sub2APIAccountSyncModels(ctx, created.ID)
	if err == nil || !strings.Contains(err.Error(), "不足 10 秒") {
		t.Fatalf("间隔内二次同步应被拒绝并提示剩余等待, 实际: %v", err)
	}

	// 把 LastSyncAt 拨到 11 秒前: 必须放行
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
		Update("last_sync_at", time.Now().Add(-11*time.Second)).Error; err != nil {
		t.Fatalf("拨动 LastSyncAt 失败: %v", err)
	}
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
		t.Fatalf("越过间隔后同步应放行: %v", err)
	}
}
