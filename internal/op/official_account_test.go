package op

import (
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var officialAccountTestDBSeq int64

func openOfficialAccountTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	seq := atomic.AddInt64(&officialAccountTestDBSeq, 1)
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), seq)
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.OfficialAccount{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}
	return conn
}

// TestOfficialAccountUniqueness 同服务商内账号标识唯一, 跨服务商可重名。
func TestOfficialAccountUniqueness(t *testing.T) {
	conn := openOfficialAccountTestDB(t)
	seed := []model.OfficialAccount{
		{Provider: model.OfficialAccountProviderOpenAI, ExternalName: "ops@example.com", Status: model.OfficialAccountStatusPending, AccessCipher: "c1"},
		{Provider: model.OfficialAccountProviderGemini, ExternalName: "ops@example.com", Status: model.OfficialAccountStatusPending, AccessCipher: "c2"},
	}
	if err := conn.Create(&seed).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	dup := model.OfficialAccount{Provider: model.OfficialAccountProviderOpenAI, ExternalName: "ops@example.com", Status: model.OfficialAccountStatusPending, AccessCipher: "c3"}
	if err := conn.Create(&dup).Error; err == nil {
		t.Fatalf("duplicate (provider, external_name): want unique error")
	}
}

// TestOfficialAccountCipherNeverExposed 密文字段不出 JSON: 序列化后无 access_cipher / refresh_cipher。
func TestOfficialAccountCipherNeverExposed(t *testing.T) {
	conn := openOfficialAccountTestDB(t)
	acc := model.OfficialAccount{Provider: model.OfficialAccountProviderClaude, ExternalName: "a@x.io", Status: model.OfficialAccountStatusActive, AccessCipher: "SECRET", RefreshCipher: "SECRET2", PlanTier: "Pro"}
	if err := conn.Create(&acc).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	var loaded model.OfficialAccount
	if err := conn.First(&loaded, acc.ID).Error; err != nil {
		t.Fatalf("load: %v", err)
	}
	// json:"-" 的字段在任何序列化路径下都不应出现: 直接按 tag 断言, 不走真实 JSON 编码也成立。
	typ := reflect.TypeOf(loaded)
	for _, field := range []string{"AccessCipher", "RefreshCipher"} {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Fatalf("field %s missing", field)
		}
		if f.Tag.Get("json") != "-" {
			t.Fatalf("field %s json tag = %q, want \"-\"", field, f.Tag.Get("json"))
		}
	}
}
