package op

import (
	"context"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
)

// R24-F1: 导入设置时如果包含非法值的设置项（如非布尔/非法枚举/非法正则），必须被校验过滤，杜绝脏设置写入数据库。
func TestDBImportIncrementalFiltersInvalidSettings(t *testing.T) {
	conn := openBackupTestDB(t)
	restore := db.SetDBForTest(conn)
	defer restore()

	if err := conn.AutoMigrate(
		&model.APIKey{},
		&model.StatsAPIKey{},
		&model.Setting{},
		&model.LLMInfo{},
		&model.OfficialAccount{},
		&model.ProxyNode{},
		&model.ProxySubscription{},
		&model.ManualSubscription{},
		&model.Sub2APIAccount{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	dump := &model.DBDump{
		Settings: []model.Setting{
			{
				Key:   model.SettingKeyUsageReportEnabled,
				Value: "not-a-boolean", // 非法布尔值
			},
			{
				Key:   model.SettingKeyModelInfoUpdateInterval,
				Value: "24", // 合法值
			},
		},
	}

	res, err := DBImportIncremental(context.Background(), dump)
	if err != nil {
		t.Fatalf("DBImportIncremental: %v", err)
	}

	if res.RowsAffected["settings"] != 1 {
		t.Fatalf("有效设置导入条数应为 1, 实际: %d", res.RowsAffected["settings"])
	}

	var validSetting model.Setting
	if err := conn.Where("key = ?", model.SettingKeyModelInfoUpdateInterval).First(&validSetting).Error; err != nil {
		t.Fatalf("合法设置项应落库: %v", err)
	}
	if validSetting.Value != "24" {
		t.Fatalf("合法设置项值应为 24, 实际: %s", validSetting.Value)
	}

	var invalidSetting model.Setting
	if err := conn.Where("key = ?", model.SettingKeyUsageReportEnabled).First(&invalidSetting).Error; err == nil {
		t.Fatalf("非法设置项不应落库: %v", invalidSetting)
	}
}
