package op

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R5-F1: sync 的读-改-写窗口必须有渠道级互斥。
// 装置让目标只受该守卫保护: 多轮 8 路 sync 与 8 路面板式手动模型追加混跑。
// ChannelUpdate 是整体替换语义, 无锁时后写者的旧快照会静默抹掉先写者的手动模型;
// 有锁时 sync 读-写原子, 手动追加不会被吞。20 轮重复消除单轮交错的偶然性。
func TestSub2APISyncModelsConcurrentKeepsManualModels(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"model-alpha"},{"id":"model-beta"}]}`))
	}))
	defer upstream.Close()

	ctx := context.Background()
	created, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "并发同步账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-concurrent-test",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}

	for round := 0; round < 20; round++ {
		if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
			Update("last_sync_at", time.Now().Add(-11*time.Second)).Error; err != nil {
			t.Fatalf("拨动 LastSyncAt 失败: %v", err)
		}
		if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
			t.Fatalf("第 %d 轮基线同步失败: %v", round, err)
		}
		// 本用例专测读-改-写互斥(R5-F1): 把最小间隔拨开, 让 8 路 sync 真正并发进入 RMW。
		// 间隔保护本身由 TestSub2APISyncModelsMinIntervalGuard 覆盖, 两个守卫互不掩盖。
		if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
			Update("last_sync_at", time.Now().Add(-11*time.Second)).Error; err != nil {
			t.Fatalf("第 %d 轮拨动 LastSyncAt 失败: %v", round, err)
		}

		var wg sync.WaitGroup
		errCh := make(chan error, 16)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil && !strings.Contains(err.Error(), "不足") {
					// R8 最小间隔保护触发的拒绝是防误连打的合法结果, 不算并发写入失败
					errCh <- err
				}
			}()
		}
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				detail, err := ChannelDetailGet(created.ChannelID)
				if err != nil {
					errCh <- err
					return
				}
				for _, m := range detail.Models {
					if m == "manual-gamma" {
						return // 运维看到已存在就不再追加
					}
				}
				detail.Models = append(detail.Models, "manual-gamma")
				detail.Grants = append(detail.Grants, model.ChannelGrantConfig{
					ModelName: "manual-gamma",
					KeyName:   "default",
					Protocols: model.ProtocolOpenAIChatCompletion,
				})
				if _, err := ChannelUpdate(&detail, ctx); err != nil {
					errCh <- err
				}
			}()
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Fatalf("第 %d 轮并发写入失败: %v", round, err)
		}

		detail, err := ChannelDetailGet(created.ChannelID)
		if err != nil {
			t.Fatalf("第 %d 轮读渠道失败: %v", round, err)
		}
		have := make(map[string]struct{}, len(detail.Models))
		for _, m := range detail.Models {
			have[m] = struct{}{}
		}
		for _, want := range []string{"model-alpha", "model-beta", "manual-gamma"} {
			if _, ok := have[want]; !ok {
				t.Fatalf("第 %d 轮并发后模型 %s 丢失(读-改-写窗口未互斥): %v", round, want, detail.Models)
			}
		}
	}
}

// R5-F2: grant 必须引用渠道现有凭据名, 不得硬编码 default。
// 装置让目标只受该守卫保护: 物化创建的 default 凭据被运维改名为 custom-key 后,
// 上游新增模型的授权必须挂到 custom-key。改回硬编码时 ChannelUpdate 会以
// "channel key %q does not belong to channel" 拒绝, 本用例直接红。
func TestSub2APISyncModelsGrantFollowsRenamedKey(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	serveExtra := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serveExtra {
			_, _ = w.Write([]byte(`{"data":[{"id":"model-alpha"},{"id":"model-beta"},{"id":"model-gamma"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"model-alpha"},{"id":"model-beta"}]}`))
	}))
	defer upstream.Close()

	ctx := context.Background()
	created, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "凭据改名账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-rename-test",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err != nil {
		t.Fatalf("基线同步失败: %v", err)
	}

	// 运维在面板把物化创建的 default 凭据改名为 custom-key(凭据与既有授权一起改)
	detail, err := ChannelDetailGet(created.ChannelID)
	if err != nil {
		t.Fatalf("读渠道详情失败: %v", err)
	}
	if len(detail.Keys) == 0 {
		t.Fatalf("物化渠道没有任何凭据")
	}
	detail.Keys[0].Name = "custom-key"
	for i := range detail.Grants {
		detail.Grants[i].KeyName = "custom-key"
	}
	if _, err := ChannelUpdate(&detail, ctx); err != nil {
		t.Fatalf("凭据改名失败: %v", err)
	}

	// 上游新增 model-gamma, sync 必须成功且新授权挂到 custom-key(先拨开最小间隔)
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
		Update("last_sync_at", time.Now().Add(-11*time.Second)).Error; err != nil {
		t.Fatalf("拨动 LastSyncAt 失败: %v", err)
	}
	serveExtra = true
	synced, err := Sub2APIAccountSyncModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("改名后同步失败(授权仍按硬编码 default 寻址): %v", err)
	}
	foundGamma := false
	for _, m := range synced.Models {
		if m == "model-gamma" {
			foundGamma = true
		}
	}
	if !foundGamma {
		t.Fatalf("同步结果缺少 model-gamma: %v", synced.Models)
	}
	after, err := ChannelDetailGet(created.ChannelID)
	if err != nil {
		t.Fatalf("读渠道详情失败: %v", err)
	}
	for _, g := range after.Grants {
		if g.ModelName == "model-gamma" && g.KeyName != "custom-key" {
			t.Fatalf("model-gamma 的授权未跟随改名后的凭据: %q", g.KeyName)
		}
	}
}

// R5-F3: 无凭据账号必须得到可执行错误, 而不是发出无认证请求让上游 401 替我们报错。
func TestSub2APISyncModelsMissingCredentialsGuard(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()
	ctx := context.Background()

	acct := model.Sub2APIAccount{
		Name:        "无凭据账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     "https://example.com",
		ChannelID:   7, // 已物化但凭据被清空
		Enabled:     true,
	}
	if err := conn.Create(&acct).Error; err != nil {
		t.Fatalf("造数据失败: %v", err)
	}
	_, err := Sub2APIAccountSyncModels(ctx, acct.ID)
	if err == nil || !strings.Contains(err.Error(), "未配置凭据") {
		t.Fatalf("无凭据账号应报可执行错误, 实际: %v", err)
	}
}
