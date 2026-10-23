package op

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/t-yang-only/OctoNexus/internal/secret"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openSub2APITestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "test-secret-key-sub2api")
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.Sub2APIAccount{},
		&model.Channel{},
		&model.ChannelKey{},
		&model.ChannelModel{},
		&model.ChannelGrant{},
		&model.Group{},
		&model.GroupItem{},
		&model.ProxyNode{},
	); err != nil {
		t.Fatalf("自动迁移失败: %v", err)
	}
	restore := db.SetDBForTest(conn)
	return conn, restore
}

// boolPtr reused from channel_key_state_test.go

func TestSub2APICRUD(t *testing.T) {
	_, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	// 1. 创建账号
	req := Sub2APIAccountCreateRequest{
		Name:        "测试 Claude 订阅",
		Platform:    model.Sub2APIPlatformAnthropic,
		AccountType: model.Sub2APIAccountTypeOAuth,
		BaseURL:     "https://api.anthropic.com",
		ApiKey:      "sk-ant-test123456",
		Enabled:     boolPtr(true),
		Notes:       "用于自动化反代测试",
	}

	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建 Sub2API 账号失败: %v", err)
	}
	if created.ID == 0 {
		t.Errorf("创建后的账号 ID 不应为 0")
	}
	if created.Platform != model.Sub2APIPlatformAnthropic {
		t.Errorf("期望平台为 %s, 实际为 %s", model.Sub2APIPlatformAnthropic, created.Platform)
	}

	// 2. 获取列表
	list, err := Sub2APIAccountList(ctx)
	if err != nil {
		t.Fatalf("查询列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("期望列表长度为 1, 实际为 %d", len(list))
	}
	if list[0].Name != "测试 Claude 订阅" {
		t.Errorf("列表首项名称不匹配: %s", list[0].Name)
	}

	// 3. 更新账号
	newBase := "https://api.anthropic.com/v1"
	updated, err := Sub2APIAccountUpdate(ctx, created.ID, Sub2APIAccountUpdateRequest{
		BaseURL: &newBase,
	})
	if err != nil {
		t.Fatalf("更新账号失败: %v", err)
	}
	if updated.BaseURL != newBase {
		t.Errorf("更新后的 BaseURL 不匹配: %s", updated.BaseURL)
	}

	// 4. 删除账号
	if err := Sub2APIAccountDelete(ctx, created.ID); err != nil {
		t.Fatalf("删除账号失败: %v", err)
	}

	listAfter, err := Sub2APIAccountList(ctx)
	if err != nil {
		t.Fatalf("查询列表失败: %v", err)
	}
	if len(listAfter) != 0 {
		t.Errorf("删除后期望列表长度为 0, 实际为 %d", len(listAfter))
	}
}

func TestSub2APIEnabledFalsePreserved(t *testing.T) {
	_, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	// 显式传 false，必须在落库后依然保持 false（守住 F4）
	req := Sub2APIAccountCreateRequest{
		Name:     "禁用账号",
		Platform: model.Sub2APIPlatformOpenAI,
		BaseURL:  "https://api.openai.com",
		ApiKey:   "sk-test",
		Enabled:  boolPtr(false),
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if created.Enabled {
		t.Fatalf("显式传 false 却被反转为 true，触发 GORM default 陷阱！")
	}

	list, err := Sub2APIAccountList(ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("读回失败: %v", err)
	}
	if list[0].Enabled {
		t.Fatalf("库内读回也是 true，显式 false 丢失！")
	}
}

func TestSub2APISensitiveHeaderEncryption(t *testing.T) {
	_, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	req := Sub2APIAccountCreateRequest{
		Name:     "敏感头测试",
		Platform: model.Sub2APIPlatformOpenAI,
		BaseURL:  "https://api.openai.com",
		ApiKey:   "sk-test-secret-123",
		Headers: map[string]string{
			"Authorization": "Bearer secret-bearer-token",
			"Cookie":        "session=secret-cookie-val",
			"X-Custom-Env":  "production",
		},
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	// 验证 Extra 明文中绝对不能包含 Authorization 或 Cookie 中的私密 Token（守住 F8）
	if strings.Contains(created.Extra, "secret-bearer-token") || strings.Contains(created.Extra, "secret-cookie-val") {
		t.Fatalf("Extra 明文中泄露了敏感凭据！Extra: %s", created.Extra)
	}

	// 验证解密后的凭据块确实包含这些敏感字段
	plain, err := secret.OpenWith(sub2apiSecretAAD, created.Credentials)
	if err != nil {
		t.Fatalf("解密凭据失败: %v", err)
	}
	var credsMap map[string]any
	if err := json.Unmarshal([]byte(plain), &credsMap); err != nil {
		t.Fatalf("反序列化凭据失败: %v", err)
	}
	hdrs, ok := credsMap["headers"].(map[string]any)
	if !ok || hdrs["Authorization"] != "Bearer secret-bearer-token" {
		t.Fatalf("解密后未找到正确的敏感头！credsMap: %v", credsMap)
	}
}

func TestSub2APISyncChannelWithModelsAndGrants(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	req := Sub2APIAccountCreateRequest{
		Name:        "自动物化账号",
		Platform:    model.Sub2APIPlatformAnthropic,
		APIProtocol: "anthropic",
		BaseURL:     "https://api.anthropic.com",
		ApiKey:      "sk-ant-test",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if created.ChannelID == 0 {
		t.Fatalf("SyncChannel=true 但未分配 ChannelID")
	}

	// 验证物化渠道的模型与授权是否已建好（守住 F1/F2）
	var modelsCount int64
	conn.Model(&model.ChannelModel{}).Where("channel_id = ?", created.ChannelID).Count(&modelsCount)
	if modelsCount == 0 {
		t.Fatalf("物化渠道未建立任何模型！")
	}

	var grantsCount int64
	conn.Model(&model.ChannelGrant{}).Where("channel_key_id IN (SELECT id FROM channel_keys WHERE channel_id = ?)", created.ChannelID).Count(&grantsCount)
	if grantsCount == 0 {
		t.Fatalf("物化渠道未建立任何授权！")
	}

	// 验证删除账号时关联渠道被连带删除（守住 F3）
	if err := Sub2APIAccountDelete(ctx, created.ID); err != nil {
		t.Fatalf("删除账号失败: %v", err)
	}
	var chanCount int64
	conn.Model(&model.Channel{}).Where("id = ?", created.ChannelID).Count(&chanCount)
	if chanCount != 0 {
		t.Fatalf("删除账号后关联渠道依然残留！")
	}
}

func TestSub2APIProbePathsAndErrors(t *testing.T) {
	_, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	var requestedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	// 1. 测试 BaseURL 带 /v1，探测地址应规范为 /v1/models，而不是 /v1/v1/models（守住 F6）
	req := Sub2APIAccountCreateRequest{
		Name:     "探测测试",
		Platform: model.Sub2APIPlatformOpenAI,
		BaseURL:  srv.URL + "/v1",
		ApiKey:   "valid-key",
		Enabled:  boolPtr(true),
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	status, err := Sub2APIAccountProbe(ctx, created.ID)
	if err != nil {
		t.Fatalf("正常探测应返回成功，实得: %v", err)
	}
	if requestedPath != "/v1/models" {
		t.Fatalf("探测路径不正确: %s, 期望 /v1/models", requestedPath)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("期望状态码包含 200, 实得: %s", status)
	}

	// 2. 模拟上游 401 失败，Probe 必须报错而不是返回 nil error（守住 F6）
	reqFail := Sub2APIAccountCreateRequest{
		Name:     "失败探测",
		Platform: model.Sub2APIPlatformOpenAI,
		BaseURL:  srv.URL + "/v1",
		ApiKey:   "", // 无 key 导致 401
		Enabled:  boolPtr(true),
	}
	createdFail, err := Sub2APIAccountCreate(ctx, reqFail)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	_, err = Sub2APIAccountProbe(ctx, createdFail.ID)
	if err == nil {
		t.Fatalf("上游返回 401 时 Probe 必须返回 error，实得 nil！")
	}
}
func TestSub2APIAccountUpdateSyncsChannel(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	req := Sub2APIAccountCreateRequest{
		Name:        "联动更新测试",
		Platform:    model.Sub2APIPlatformOpenAI,
		BaseURL:     "https://api.openai.com",
		ApiKey:      "sk-old-key",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if created.ChannelID == 0 {
		t.Fatalf("物化未成功生成 ChannelID")
	}

	// 验证初始渠道状态
	var chanRow model.Channel
	if err := conn.First(&chanRow, created.ChannelID).Error; err != nil {
		t.Fatalf("查询关联渠道失败: %v", err)
	}
	if chanRow.BaseURL != "https://api.openai.com" {
		t.Fatalf("渠道初始 BaseURL 不匹配: %s", chanRow.BaseURL)
	}

	// 执行账号更新（更新 BaseURL 与 APIKey 与 Enabled）
	newBase := "https://gateway.example.com/v1"
	newKey := "sk-new-super-key"
	updated, err := Sub2APIAccountUpdate(ctx, created.ID, Sub2APIAccountUpdateRequest{
		BaseURL: &newBase,
		ApiKey:  &newKey,
		Enabled: boolPtr(false),
	})
	if err != nil {
		t.Fatalf("更新账号失败: %v", err)
	}
	if updated.Enabled {
		t.Fatalf("更新后账号 Enabled 应为 false")
	}

	// 核心断言：物化渠道的 BaseURL 与 Enabled 以及 Key 必须实时同步！（守住 R3-03 核心契约）
	var chanUpdated model.Channel
	if err := conn.First(&chanUpdated, created.ChannelID).Error; err != nil {
		t.Fatalf("查询更新后渠道失败: %v", err)
	}
	if chanUpdated.BaseURL != newBase {
		t.Fatalf("物化渠道未能同步更新 BaseURL！期望 %s, 实得 %s", newBase, chanUpdated.BaseURL)
	}
	if chanUpdated.Enabled {
		t.Fatalf("物化渠道未能同步更新 Enabled 为 false！")
	}

	var keyRow model.ChannelKey
	if err := conn.Where("channel_id = ?", created.ChannelID).First(&keyRow).Error; err != nil {
		t.Fatalf("查询渠道凭据失败: %v", err)
	}
	plainKey, err := secret.Open(keyRow.Key)
	if err != nil {
		t.Fatalf("解密渠道凭据失败: %v", err)
	}
	if plainKey != newKey {
		t.Fatalf("物化渠道未能同步更新 Key！期望 %s, 实得 %s", newKey, plainKey)
	}
}

func TestSub2APIAccountChannelCustomHeaderNoSensitive(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	req := Sub2APIAccountCreateRequest{
		Name:     "头安全测试",
		Platform: model.Sub2APIPlatformAnthropic,
		BaseURL:  "https://api.anthropic.com",
		ApiKey:   "sk-ant-test",
		Headers: map[string]string{
			"Authorization": "Bearer secret-auth-header-leak",
			"Cookie":        "secret-cookie-leak",
			"X-Org-Id":      "org-public-123",
		},
		Enabled:     boolPtr(true),
		SyncChannel: true,
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	// 核心断言：渠道表的 CustomHeader 绝对禁止二次明文存储 Authorization 或 Cookie！（守住 R3-04 凭据闭环）
	var chanRow model.Channel
	if err := conn.First(&chanRow, created.ChannelID).Error; err != nil {
		t.Fatalf("查询渠道失败: %v", err)
	}
	for _, h := range chanRow.CustomHeader {
		if strings.EqualFold(h.HeaderKey, "Authorization") {
			t.Fatalf("Authorization 不得进入渠道 CustomHeader！")
		}
		if strings.EqualFold(h.HeaderKey, "Cookie") {
			if strings.Contains(h.HeaderValue, "secret-cookie-leak") {
				t.Fatalf("Cookie 在 CustomHeader 中明文泄露！")
			}
			if !secret.IsSealed(h.HeaderValue) {
				t.Fatalf("Cookie 必须为 AES-256 密文（enc:v1:）！实得: %s", h.HeaderValue)
			}
		}
	}
}
func TestSub2APIAccountUpdatePreservesManualModelsAndSettings(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	// 1. 创建账号并物化渠道
	req := Sub2APIAccountCreateRequest{
		Name:        "保留人工配置测试",
		Platform:    model.Sub2APIPlatformOpenAI,
		BaseURL:     "https://api.openai.com",
		ApiKey:      "sk-origin",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	// 2. 模拟运维人员在渠道上手动追加模型、修改自定义路径与计费
	d, err := ChannelDetailGet(created.ChannelID)
	if err != nil {
		t.Fatalf("获取渠道详情失败: %v", err)
	}
	d.OpenAIChatCompletionPath = "/custom/chat/v1"
	d.BillingMode = "subscription"
	d.MonthlyQuota = 500
	d.Models = append(d.Models, "my-custom-fine-tuned-model")
	d.Grants = append(d.Grants, model.ChannelGrantConfig{
		ModelName: "my-custom-fine-tuned-model",
		KeyName:   "default",
		Protocols: model.ProtocolOpenAIChatCompletion,
	})
	if _, err := ChannelUpdate(&d, ctx); err != nil {
		t.Fatalf("更新渠道手工配置失败: %v", err)
	}

	// 3. 此时用户只更新账号的 Notes 字段
	newNotes := "仅更新备注"
	if _, err := Sub2APIAccountUpdate(ctx, created.ID, Sub2APIAccountUpdateRequest{Notes: &newNotes}); err != nil {
		t.Fatalf("更新账号备注失败: %v", err)
	}

	// 4. 核心断言：手工配置的路径、计费与自定义模型必须完整保留，绝不能被全量冲掉！（攻克 R3-F1）
	var c model.Channel
	conn.First(&c, created.ChannelID)
	if c.OpenAIChatCompletionPath != "/custom/chat/v1" || c.MonthlyQuota != 500 {
		t.Fatalf("渠道手工配置的端点路径或配额被冲掉了！Path: %s, Quota: %f", c.OpenAIChatCompletionPath, c.MonthlyQuota)
	}

	var customModelCount int64
	conn.Model(&model.ChannelModel{}).Where("channel_id = ? AND name = ?", created.ChannelID, "my-custom-fine-tuned-model").Count(&customModelCount)
	if customModelCount != 1 {
		t.Fatalf("渠道手工追加的自定义模型被无辜删除了！")
	}
}

func TestSub2APIAccountCorruptCredentialsGuard(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	req := Sub2APIAccountCreateRequest{
		Name:     "坏密文保护",
		Platform: model.Sub2APIPlatformOpenAI,
		BaseURL:  "https://api.openai.com",
		ApiKey:   "sk-test",
		Enabled:  boolPtr(true),
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	// 注入破坏密文
	conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).Update("credentials", "enc:v1:corrupted-value")

	// 用户只更新备注，原坏密文不得被静默置空（攻克 R3-F5）
	newNotes := "修改备注"
	updated, err := Sub2APIAccountUpdate(ctx, created.ID, Sub2APIAccountUpdateRequest{Notes: &newNotes})
	if err != nil {
		t.Fatalf("只改备注应成功: %v", err)
	}
	if updated.Credentials != "enc:v1:corrupted-value" {
		t.Fatalf("坏密文在未提供新凭据时被静默清空抹除了！实得: %s", updated.Credentials)
	}
}
func TestSub2APIDisabledAccountMaterializesDisabledChannel(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	ctx := context.Background()

	req := Sub2APIAccountCreateRequest{
		Name:        "初始禁用物化",
		Platform:    model.Sub2APIPlatformAnthropic,
		BaseURL:     "https://api.anthropic.com",
		ApiKey:      "sk-test",
		Enabled:     boolPtr(false),
		SyncChannel: true,
	}
	created, err := Sub2APIAccountCreate(ctx, req)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if created.Enabled {
		t.Fatalf("创建后账号应为禁用 (false)")
	}
	if created.ChannelID == 0 {
		t.Fatalf("物化渠道未分配 ID")
	}

	// 核心断言：物化渠道的 Enabled 也必须严格为 false，绝不能被 GORM default:true 反转！（攻克 R3-F7）
	var c model.Channel
	if err := conn.First(&c, created.ChannelID).Error; err != nil {
		t.Fatalf("查询渠道失败: %v", err)
	}
	if c.Enabled {
		t.Fatalf("初始禁用账号物化出的渠道 Enabled 变为 true！触发 GORM default 覆盖！")
	}

	cached, err := ChannelGet(created.ChannelID)
	if err != nil {
		t.Fatalf("读渠道缓存失败: %v", err)
	}
	if cached.Enabled {
		t.Fatalf("渠道缓存中的 Enabled 也必须为 false！")
	}
}

// 动态模型发现的判据必须落在"上游收到了什么"与"库里落成了什么"两层：
// 前者用假上游记录 Authorization 头与请求路径（mock 不校验密钥，HTTP 200 不代表解密生效），
// 后者按名称逐条断言模型与授权，避免被物化时预置的默认模型数干扰。
func TestSub2APISyncModelsDynamicDiscovery(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()

	var gotAuth, gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		// 故意返回重复 ID：去重是本功能的硬性质
		_, _ = w.Write([]byte(`{"data":[{"id":"model-alpha"},{"id":"model-beta"},{"id":"model-alpha"}]}`))
	}))
	defer upstream.Close()

	ctx := context.Background()
	created, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "动态模型同步账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     upstream.URL,
		ApiKey:      "sk-sync-test-001",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}
	if created.ChannelID == 0 {
		t.Fatalf("SyncChannel=true 但未分配 ChannelID")
	}

	synced, err := Sub2APIAccountSyncModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("动态模型同步失败: %v", err)
	}

	// 上游指纹：解密后的 api_key 必须以 Bearer 送达，且路径归一化为 /v1/models
	if gotAuth != "Bearer sk-sync-test-001" {
		t.Fatalf("上游收到的 Authorization 不正确: %q", gotAuth)
	}
	if gotPath != "/v1/models" {
		t.Fatalf("上游收到的模型路径不正确: %q", gotPath)
	}

	countAlpha, countBeta := 0, 0
	for _, m := range synced.Models {
		switch m {
		case "model-alpha":
			countAlpha++
		case "model-beta":
			countBeta++
		}
	}
	if countAlpha != 1 || countBeta != 1 {
		t.Fatalf("去重后 model-alpha/beta 应各出现一次: %v", synced.Models)
	}

	// 落库层：渠道模型与授权按名称逐条核对
	detail, err := ChannelDetailGet(created.ChannelID)
	if err != nil {
		t.Fatalf("读渠道详情失败: %v", err)
	}
	haveModels := make(map[string]struct{}, len(detail.Models))
	for _, m := range detail.Models {
		haveModels[m] = struct{}{}
	}
	for _, want := range []string{"model-alpha", "model-beta"} {
		if _, ok := haveModels[want]; !ok {
			t.Fatalf("渠道模型缺少 %s: %v", want, detail.Models)
		}
	}
	haveGrants := make(map[string]struct{}, len(detail.Grants))
	for _, g := range detail.Grants {
		haveGrants[g.ModelName] = struct{}{}
	}
	for _, m := range detail.Models {
		if _, ok := haveGrants[m]; !ok {
			t.Fatalf("模型 %s 缺少授权", m)
		}
	}

	// 幂等：再次同步不得重复追加(先拨开 R8 最小间隔)
	if err := conn.Model(&model.Sub2APIAccount{}).Where("id = ?", created.ID).
		Update("last_sync_at", time.Now().Add(-11*time.Second)).Error; err != nil {
		t.Fatalf("拨动 LastSyncAt 失败: %v", err)
	}
	again, err := Sub2APIAccountSyncModels(ctx, created.ID)
	if err != nil {
		t.Fatalf("二次同步失败: %v", err)
	}
	againAlpha, againBeta := 0, 0
	for _, m := range again.Models {
		switch m {
		case "model-alpha":
			againAlpha++
		case "model-beta":
			againBeta++
		}
	}
	if againAlpha != 1 || againBeta != 1 {
		t.Fatalf("二次同步后模型被重复追加: %v", again.Models)
	}
}

// 守卫分支：未物化、上游故障、账号不存在三条路径必须各自可辨识地失败。
func TestSub2APISyncModelsGuards(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()
	ctx := context.Background()

	unsynced := model.Sub2APIAccount{
		Name:     "未物化账号",
		Platform: model.Sub2APIPlatformOpenAI,
		BaseURL:  "https://example.com",
		Enabled:  true,
	}
	if err := conn.Create(&unsynced).Error; err != nil {
		t.Fatalf("造数据失败: %v", err)
	}
	if _, err := Sub2APIAccountSyncModels(ctx, unsynced.ID); err == nil {
		t.Fatalf("ChannelID=0 的账号未被拒绝")
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer failing.Close()

	created, err := Sub2APIAccountCreate(ctx, Sub2APIAccountCreateRequest{
		Name:        "上游故障账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     failing.URL,
		ApiKey:      "sk-fail-test",
		Enabled:     boolPtr(true),
		SyncChannel: true,
	})
	if err != nil {
		t.Fatalf("创建账号失败: %v", err)
	}
	if _, err := Sub2APIAccountSyncModels(ctx, created.ID); err == nil {
		t.Fatalf("上游 500 未被识别为错误")
	}
	// 上游故障后渠道详情仍可读（未被写坏）
	if _, err := ChannelDetailGet(created.ChannelID); err != nil {
		t.Fatalf("上游故障后渠道详情不可读: %v", err)
	}

	if _, err := Sub2APIAccountSyncModels(ctx, 999999); !errors.Is(err, ErrSub2APIAccountNotFound) {
		t.Fatalf("不存在的账号应返回 ErrSub2APIAccountNotFound，实际: %v", err)
	}
}
