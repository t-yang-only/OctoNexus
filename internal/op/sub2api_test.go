package op

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestSub2APICRUD(t *testing.T) {
	t.Setenv("OCTOPUS_OFFICIAL_KEY", "test-secret-key-123")
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if err := conn.AutoMigrate(&model.Sub2APIAccount{}); err != nil {
		t.Fatalf("自动迁移失败: %v", err)
	}
	restore := db.SetDBForTest(conn)
	defer restore()

	ctx := context.Background()

	// 1. 测试创建账号
	req := Sub2APIAccountCreateRequest{
		Name:        "测试 Claude 订阅",
		Platform:    model.Sub2APIPlatformAnthropic,
		AccountType: model.Sub2APIAccountTypeOAuth,
		BaseURL:     "https://api.anthropic.com",
		ApiKey:      "sk-ant-test123456",
		Enabled:     true,
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

	// 2. 测试获取列表
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

	// 3. 测试删除账号
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
