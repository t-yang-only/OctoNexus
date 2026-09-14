package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/charmbracelet/log"
)

const (
	TaskPriceUpdate   = "price_update"
	TaskStatsSave     = "stats_save"
	TaskRelayLogClean = "relay_log_clean"
	TaskCleanLLM      = "clean_llm"
	TaskQuotaScan     = "quota_scan"
)

func Init() {
	priceUpdateIntervalHours, err := op.SettingGetInt(model.SettingKeyModelInfoUpdateInterval)
	if err != nil {
		log.Errorf("failed to get model info update interval: %v", err)
		return
	}
	priceUpdateInterval := time.Duration(priceUpdateIntervalHours) * time.Hour
	// 注册价格更新任务
	Register(string(model.SettingKeyModelInfoUpdateInterval), priceUpdateInterval, true, func() {
		if err := price.UpdateLLMPrice(context.Background()); err != nil {
			log.Warnf("failed to update price info: %v", err)
		}
	})

	// 注册统计保存任务
	statsSaveIntervalMinutes, err := op.SettingGetInt(model.SettingKeyStatsSaveInterval)
	if err != nil {
		log.Warnf("failed to get stats save interval: %v", err)
		return
	}
	statsSaveInterval := time.Duration(statsSaveIntervalMinutes) * time.Minute
	Register(TaskStatsSave, statsSaveInterval, false, op.StatsSaveDBTask)

	// 注册历史日志清理任务: 与统计落库同周期, 按保留期删除过期 relay_logs。
	Register(TaskRelayLogClean, statsSaveInterval, false, func() {
		op.RelayLogClean(model.RelayLogRetentionDays)
	})

	// 注册余额采集扫描任务 (T-quota-001): 默认 5 分钟, quota_scan_interval 可配, 0 表示停用 (Register 自动跳过)。
	Register(TaskQuotaScan, op.QuotaScanInterval(), false, quotaScanOnce)
}
