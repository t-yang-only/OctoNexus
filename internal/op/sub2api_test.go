package op

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
		Name:        "禁用账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		BaseURL:     "https://api.openai.com",
		ApiKey:      "sk-test",
		Enabled:     boolPtr(false),
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