package op

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openAPIKeyTestDB 打开一块内存 SQLite 并建出 api_keys 表 (与业务同驱动, 不依赖 cgo)。
// 用例结束后恢复连接与两个全局索引缓存的原有内容, 避免与同包其他用例互相污染。
func openAPIKeyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:apikey_%d?mode=memory&cache=shared", time.Now().UnixNano())
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := conn.AutoMigrate(&model.APIKey{}); err != nil {
		t.Fatalf("migrate api_keys: %v", err)
	}
	restore := db.SetDBForTest(conn)
	cacheBefore := apiKeyCache.GetAll()
	idMapBefore := apiKeyIDMap.GetAll()
	t.Cleanup(func() {
		restore()
		apiKeyCache.Clear()
		for k, v := range cacheBefore {
			apiKeyCache.Set(k, v)
		}
		apiKeyIDMap.Clear()
		for k, v := range idMapBefore {
			apiKeyIDMap.Set(k, v)
		}
	})
	return conn
}

// TestAPIKeyCreatePersistsDisabled 回归 NM-DS-002 续查的缺陷: Enabled 带 `gorm:"default:true"`,
// 原实现直接 Create(&key), GORM 会跳过零值字段 → 传 enabled=false 落库成 true (接口返回 true,
// 该"禁用"Key 照样能转发)。Select("*") 之后必须原样落库。
func TestAPIKeyCreatePersistsDisabled(t *testing.T) {
	conn := openAPIKeyTestDB(t)
	ctx := context.Background()

	key := &model.APIKey{Name: "disabled-key", APIKey: "sk-test-disabled", Enabled: false, RPM: 3, TPM: 9}
	if err := APIKeyCreate(key, ctx); err != nil {
		t.Fatalf("create: %v", err)
	}
	if key.ID == 0 {
		t.Fatalf("create did not assign an id (Select(*) must not break auto-increment)")
	}

	var stored model.APIKey
	if err := conn.Where("id = ?", key.ID).First(&stored).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.Enabled {
		t.Fatalf("disabled key was persisted as enabled: %+v", stored)
	}
	if stored.RPM != 3 || stored.TPM != 9 {
		t.Fatalf("limits not persisted: %+v", stored)
	}

	cached, err := APIKeyGet(key.ID, ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cached.Enabled {
		t.Fatalf("cache reports the key as enabled: %+v", cached)
	}
}

// TestAPIKeyCreateEnabledStaysEnabled 对照组: 传 enabled=true 仍然启用, 且更新路径能把 Key 停用。
func TestAPIKeyCreateEnabledStaysEnabled(t *testing.T) {
	conn := openAPIKeyTestDB(t)
	ctx := context.Background()

	key := &model.APIKey{Name: "enabled-key", APIKey: "sk-test-enabled", Enabled: true}
	if err := APIKeyCreate(key, ctx); err != nil {
		t.Fatalf("create: %v", err)
	}
	var stored model.APIKey
	if err := conn.Where("id = ?", key.ID).First(&stored).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !stored.Enabled {
		t.Fatalf("enabled key was persisted as disabled: %+v", stored)
	}

	stored.Enabled = false
	if err := APIKeyUpdate(&stored, ctx); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := APIKeyGet(key.ID, ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Enabled {
		t.Fatalf("update did not persist the disabled state: %+v", got)
	}
}
