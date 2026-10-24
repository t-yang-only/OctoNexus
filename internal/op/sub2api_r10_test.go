package op

import (
	"context"
	"strings"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R10-F1: probe 与 sync 必须走同一套凭据语义 (R5-F3 的同款交叉点)。
// R5-F3 修掉了 sync 的"无凭据静默发无认证请求", probe 路径被漏掉 —— 这正是
// "新增守卫要把同类路径逐个过一遍"的教训 (T-smart-004 同源)。
// 装置让目标只受该守卫保护: 无凭据账号 probe 必须报可执行错误,
// 而不是打到上游让 401 替我们报错。
func TestSub2APIProbeMissingCredentialsGuard(t *testing.T) {
	conn, restore := openSub2APITestDB(t)
	defer restore()
	ctx := context.Background()

	acct := model.Sub2APIAccount{
		Name:        "无凭据探测账号",
		Platform:    model.Sub2APIPlatformOpenAI,
		APIProtocol: "chat_completions",
		BaseURL:     "https://example.com",
		Enabled:     true,
	}
	if err := conn.Create(&acct).Error; err != nil {
		t.Fatalf("造数据失败: %v", err)
	}
	status, err := Sub2APIAccountProbe(ctx, acct.ID)
	if err == nil || !strings.Contains(err.Error(), "未配置凭据") {
		t.Fatalf("无凭据账号探测应报可执行错误, 实际 status=%q err=%v", status, err)
	}
}
