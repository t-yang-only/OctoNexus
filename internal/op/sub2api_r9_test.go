package op

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R9-F1: 上次同步摘要必须持久可读 —— 运维刷新页面后仍能看到"上次新增几个/下架哪几个",
// 否则 toast 一闪即逝, 下架模型从此无入口可查。摘要含 added_count/stale_count/stale 列表。
func TestSub2APISyncModelsPersistsSummary(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	serve := `{"data":[{"id":"model-alpha"},{"id":"model-beta"}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(serve))
	}))
	defer upstream.Close()

	ctx := context.Background()
	created, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "摘要持久化账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-summary-test",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}

	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
		t.Fatalf("首次同步失败: %v", err)
	}

	// 从库里直读摘要(模拟前端刷新后拉账号列表的场景)
	var stored model.Sub2APIAccount
	if err := conn.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("读账号失败: %v", err)
	}
	if stored.LastSyncSummary == "" {
		t.Fatalf("同步成功后未写入摘要")
	}
	var summary struct {
		AddedCount int      `json:"added_count"`
		StaleCount int      `json:"stale_count"`
		Stale      []string `json:"stale"`
	}
	if err := json.Unmarshal([]byte(stored.LastSyncSummary), &summary); err != nil {
		t.Fatalf("摘要不是合法 JSON: %v", err)
	}
	if summary.AddedCount != 2 || summary.StaleCount != 0 {
		t.Fatalf("摘要计数不正确: added=%d stale=%d", summary.AddedCount, summary.StaleCount)
	}

	// 上游缩容再同步(先拨开最小间隔): 摘要必须反映本次的下架发现
	serve = `{"data":[{"id":"model-alpha"}]}`
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
		Update("last_sync_at", time.Now().Add(-11*time.Second)).Error; err != nil {
		t.Fatalf("拨动 LastSyncAt 失败: %v", err)
	}
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
		t.Fatalf("二次同步失败: %v", err)
	}
	if err := conn.First(&stored, created.ID).Error; err != nil {
		t.Fatalf("重读账号失败: %v", err)
	}
	if err := json.Unmarshal([]byte(stored.LastSyncSummary), &summary); err != nil {
		t.Fatalf("二次摘要不是合法 JSON: %v", err)
	}
	if summary.StaleCount != 1 || len(summary.Stale) != 1 || summary.Stale[0] != "model-beta" {
		t.Fatalf("二次摘要未反映下架: %+v", summary)
	}
}
