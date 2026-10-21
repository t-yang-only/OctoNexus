package migrate

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/t-yang-only/OctoNexus/internal/secret"
	"gorm.io/gorm"
)

func init() {
	RegisterAfterAutoMigration(Migration{
		Version: 18,
		Up:      migrateEncryptChannelSensitiveHeaders,
	})
}

func isSensitiveHeaderName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	return lower == "authorization" ||
		lower == "cookie" ||
		lower == "x-api-key" ||
		lower == "api-key" ||
		lower == "x-auth-token" ||
		lower == "token" ||
		lower == "proxy-authorization"
}

// migrateEncryptChannelSensitiveHeaders 扫描并加密历史渠道 custom_header 中的明文敏感头 (R3-F6 历史清洗)
func migrateEncryptChannelSensitiveHeaders(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("db is nil")
	}
	if !db.Migrator().HasTable("channels") || !hasPhysicalColumn(db, "channels", "custom_header") {
		return nil
	}

	type channelHeaderRow struct {
		ID           int
		CustomHeader *string
	}
	var rows []channelHeaderRow
	if err := db.Table("channels").Select("id, custom_header").Find(&rows).Error; err != nil {
		return fmt.Errorf("failed to scan channels custom_header: %w", err)
	}

	for _, row := range rows {
		if row.CustomHeader == nil || *row.CustomHeader == "" || *row.CustomHeader == "null" {
			continue
		}
		var headers []model.CustomHeader
		if err := json.Unmarshal([]byte(*row.CustomHeader), &headers); err != nil {
			continue
		}
		modified := false
		for i := range headers {
			if isSensitiveHeaderName(headers[i].HeaderKey) && !secret.IsSealed(headers[i].HeaderValue) {
				sealed, err := secret.SealWith("channel:header", headers[i].HeaderValue)
				if err == nil {
					headers[i].HeaderValue = sealed
					modified = true
				}
			}
		}
		if modified {
			raw, _ := json.Marshal(headers)
			str := string(raw)
			_ = db.Table("channels").Where("id = ?", row.ID).Update("custom_header", str).Error
		}
	}
	return nil
}