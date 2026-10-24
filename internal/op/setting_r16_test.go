package op

import (
	"context"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R16-F1: 凭据型敏感设置项绝不通过设置列表明文回显 (pool_declarative_adapters 存放声明式适配器全量 JSON 密文)。
func TestSettingListRedactsPoolDeclarativeAdapters(t *testing.T) {
	SettingSetStringForTest(model.SettingKeyPoolDeclarativeAdapters, "enc:v1:fake-secret-adapters-ciphertext", true)
	SettingSetStringForTest(model.SettingKeyBalanceCurrency, "USD", true)
	t.Cleanup(func() {
		SettingSetStringForTest(model.SettingKeyPoolDeclarativeAdapters, "", false)
		SettingSetStringForTest(model.SettingKeyBalanceCurrency, "", false)
	})

	list, err := SettingList(context.Background())
	if err != nil {
		t.Fatalf("SettingList: %v", err)
	}

	foundNormal := false
	for _, s := range list {
		if s.Key == model.SettingKeyPoolDeclarativeAdapters {
			t.Fatalf("SettingList 泄露了 pool_declarative_adapters 敏感配置: value=%q", s.Value)
		}
		if s.Key == model.SettingKeyBalanceCurrency {
			foundNormal = true
		}
	}
	if !foundNormal {
		t.Fatalf("SettingList 应正常返回普通配置 balance_currency")
	}
}
