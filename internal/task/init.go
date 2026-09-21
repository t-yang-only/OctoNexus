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
	TaskRouteProbe    = "route_probe"
	TaskNodeProbe     = "node_probe"
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

	// 注册冷却成员主动探活任务 (R-probe-001): 周期按秒配置, 默认 300 秒, 0 表示停用;
	// 任务本身恒注册, 开关 route_probe_enabled 每轮读取, 用户改开关无需重启。
	Register(TaskRouteProbe, op.RouteProbeInterval(), false, routeProbeOnce)

	// 注册出口节点定时探活：把不通的节点标记出来（选路与备用出口据此跳过），
	// 恢复后再自动可用。默认 5 分钟一轮。
	Register(TaskNodeProbe, 5*time.Minute, false, func() {
		op.ProxyNodeProbeAll(context.Background())
	})
	// 首跑必须延迟：服务刚起来时出口内核还没把节点端口准备好，立刻探活会把**整池**判成不通
	// （实测 115 个节点全红），反而让健康筛选变成摆设。给内核 90 秒再探第一轮。
	go func() {
		time.Sleep(90 * time.Second)
		op.ProxyNodeProbeAll(context.Background())
	}()
}
