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

// R11-F1: 下架模型清理闭环 —— 发现(R6/R9)之后必须有安全的清理动作。
// 安全边界即判据: 只删名单与渠道现状的交集; 名单外模型(含人工手填)绝不动;
// 空名单/已清理时明确拒绝而非静默成功。
func TestSub2APICleanStaleModelsRemovesOnlyListed(t *testing.T) {
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
		Name:        "清理闭环账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-clean-test",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
		t.Fatalf("基线同步失败: %v", err)
	}

	// 人工追加一个名单外模型, 它必须存活
	seed, err := ChannelDetailGet(created.ChannelID)
	if err != nil {
		t.Fatalf("读渠道失败: %v", err)
	}
	seed.Models = append(seed.Models, "manual-keep")
	seed.Grants = append(seed.Grants, model.ChannelGrantConfig{
		ModelName: "manual-keep", KeyName: "default", Protocols: model.ProtocolOpenAIChatCompletion,
	})
	if _, err := ChannelUpdate(&seed, ctx); err != nil {
		t.Fatalf("写入人工模型失败: %v", err)
	}

	// 上游缩容制造下架: beta 进了 stale 名单
	serve = `{"data":[{"id":"model-alpha"}]}`
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
		Update("last_sync_at", time.Now().Add(-11*time.Second)).Error; err != nil {
		t.Fatalf("拨动 LastSyncAt 失败: %v", err)
	}
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
		t.Fatalf("二次同步失败: %v", err)
	}

	// 清理: beta 应被移除, manual-keep 与 alpha 必须存活
	result, err := Sub2APIAccountCleanStaleModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("清理失败: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "model-beta" {
		t.Fatalf("清理名单不正确: %v", result.Removed)
	}
	detail, err := ChannelDetailGet(created.ChannelID)
	if err != nil {
		t.Fatalf("读渠道失败: %v", err)
	}
	have := make(map[string]struct{}, len(detail.Models))
	for _, m := range detail.Models {
		have[m] = struct{}{}
	}
	for _, must := range []string{"model-alpha", "manual-keep"} {
		if _, ok := have[must]; !ok {
			t.Fatalf("模型 %s 被误删: %v", must, detail.Models)
		}
	}
	if _, ok := have["model-beta"]; ok {
		t.Fatalf("model-beta 未被清理: %v", detail.Models)
	}
	// 授权同步收敛: beta 的授权必须一并移除
	for _, g := range detail.Grants {
		if g.ModelName == "model-beta" {
			t.Fatalf("model-beta 的授权未清理")
		}
	}

	// 重复清理必须明确拒绝(摘要已归零), 而不是静默成功
	if _, err := Sub2APIAccountCleanStaleModels(ctx, created.ID); err == nil || !strings.Contains(err.Error(), "没有待清理") {
		t.Fatalf("重复清理应明确拒绝, 实际: %v", err)
	}

	// 名单非空但渠道里已没有(被手动清理过): 必须走"已不在渠道中"拒绝, 不静默成功
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
		Update("last_sync_summary", `{"added_count":0,"stale_count":1,"stale":["model-beta"]}`).Error; err != nil {
		t.Fatalf("写回模拟摘要失败: %v", err)
	}
	if _, err := Sub2APIAccountCleanStaleModels(ctx, created.ID); err == nil || !strings.Contains(err.Error(), "已不在渠道中") {
		t.Fatalf("名单与渠道无交集时应明确拒绝, 实际: %v", err)
	}

	// 无下架模型的账号必须明确拒绝
	fresh, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "无下架账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-clean-fresh",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建干净账号失败: %v", err)
	}
	if _, err := Sub2APIAccountCleanStaleModels(ctx, fresh.ID); err == nil || !strings.Contains(err.Error(), "没有待清理") {
		t.Fatalf("无下架模型应明确拒绝, 实际: %v", err)
	}
}
