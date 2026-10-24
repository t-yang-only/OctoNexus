package op

import (
	"context"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R18-F1: APIKeyCreate 创建 Key 时必须持久化 AllowedCIDRs 白名单。
// 此前 Select 列白名单遗漏了 allowed_cidrs，导致即便入参提供了白名单，落库时被 GORM 剔除。
func TestAPIKeyCreatePersistsAllowedCIDRs(t *testing.T) {
	conn := openAPIKeyTestDB(t)

	cidrs := []string{"192.168.1.0/24", "10.0.0.1"}
	key := &model.APIKey{
		Name:         "cidr-key",
		APIKey:       "sk-test-cidr-create",
		Enabled:      true,
		AllowedCIDRs: cidrs,
	}

	if err := APIKeyCreate(key, context.Background()); err != nil {
		t.Fatalf("APIKeyCreate: %v", err)
	}

	var stored model.APIKey
	if err := conn.First(&stored, key.ID).Error; err != nil {
		t.Fatalf("read stored key: %v", err)
	}

	if len(stored.AllowedCIDRs) != 2 || stored.AllowedCIDRs[0] != cidrs[0] || stored.AllowedCIDRs[1] != cidrs[1] {
		t.Fatalf("落库后的 AllowedCIDRs 不匹配: got %v, want %v", stored.AllowedCIDRs, cidrs)
	}

	cached, ok := apiKeyCache.Get(key.ID)
	if !ok {
		t.Fatalf("缓存中未找到新创建的 key: id=%d", key.ID)
	}
	if len(cached.AllowedCIDRs) != 2 || cached.AllowedCIDRs[0] != cidrs[0] || cached.AllowedCIDRs[1] != cidrs[1] {
		t.Fatalf("缓存中的 AllowedCIDRs 不匹配: got %v, want %v", cached.AllowedCIDRs, cidrs)
	}
}
