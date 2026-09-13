package op

import (
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// dbConn 是可注入的数据库连接抽象: *gorm.DB 天然满足, 测试传入直连内存库隔离。
type dbConn interface {
	Model(value any) *gorm.DB
	Create(value any) *gorm.DB
	Where(query any, args ...any) *gorm.DB
}

func connOrDefault(conn dbConn) dbConn {
	if conn != nil {
		return conn
	}
	return db.GetDB()
}

// RelayLogSave 落库一条已结束请求快照; FirstByteMs 未提交时记 -1。
// conn 为空时走全局库, 测试可传入直连内存库隔离。
func RelayLogSave(entry model.RelayLog) {
	relayLogSaveOn(nil, entry)
}

func relayLogSaveOn(conn dbConn, entry model.RelayLog) {
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now()
	}
	_ = connOrDefault(conn).Create(&entry).Error
}

// RelayLogList 按筛选条件倒序分页查询历史日志。
// Limit<=0 时取默认 50, 上限 RelayLogPageMaxLimit; Offset<0 时按 0 处理。
// conn 为空时走全局库, 测试可传入直连内存库隔离。
func RelayLogList(filter model.RelayLogFilter) ([]model.RelayLog, int64) {
	return relayLogListOn(nil, filter)
}

func relayLogListOn(conn dbConn, filter model.RelayLogFilter) ([]model.RelayLog, int64) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > model.RelayLogPageMaxLimit {
		limit = model.RelayLogPageMaxLimit
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}

	query := connOrDefault(conn).Model(&model.RelayLog{})
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.Model != "" {
		query = query.Where("model = ?", filter.Model)
	}
	if filter.Channel != "" {
		query = query.Where("target_channel = ?", filter.Channel)
	}
	if filter.APIKey != "" {
		query = query.Where("api_key_name = ?", filter.APIKey)
	}
	if q := strings.TrimSpace(filter.Q); q != "" {
		like := "%" + q + "%"
		query = query.Where("model LIKE ? OR target_channel LIKE ? OR error LIKE ?", like, like, like)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0
	}
	var logs []model.RelayLog
	if err := query.Order("id DESC").Limit(limit).Offset(offset).Find(&logs).Error; err != nil {
		return nil, total
	}
	return logs, total
}

// RelayLogClean 删除 CreatedAt 早于保留期的历史日志, 返回删除条数。
func RelayLogClean(retentionDays int) int64 {
	return relayLogCleanOn(nil, retentionDays)
}

func relayLogCleanOn(conn dbConn, retentionDays int) int64 {
	if retentionDays <= 0 {
		retentionDays = model.RelayLogRetentionDays
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	result := connOrDefault(conn).Where("created_at < ?", cutoff).Delete(&model.RelayLog{})
	if result.Error != nil {
		return 0
	}
	return result.RowsAffected
}
