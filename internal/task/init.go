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
	TaskUsageReport   = "usage_report"
	TaskAlertRule     = "alert_rule"
	TaskWebDAVBackup  = "webdav_backup"
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
	//
	// 探活之后紧接着检查"有没有渠道被坏节点挡住"（T-proxy-002）：这一步必须放在探活**之后**，
	// 否则拿到的是上一轮的结论，会在节点刚恢复时仍然报坏、刚坏时报好。
	Register(TaskNodeProbe, 5*time.Minute, false, func() {
		op.ProxyNodeProbeAll(context.Background())
		proxyBlockedChannelNotify()
	})

	// 注册用量报告任务：按小时轮询，只有"当前整点 == 配置时刻 且 本周期没发过"才真的发。
	// 任务恒注册（开关与时刻每轮现读），用户改配置无需重启。
	Register(TaskUsageReport, time.Hour, false, usageReportOnce)

	// 注册告警规则评估：默认 1 分钟一轮。窗口是分钟级的（默认 15 分钟），
	// 评估太稀会让"渠道已经坏了十分钟"这种事实迟迟报不出来。
	// 任务恒注册，规则是否启用每轮现读。
	Register(TaskAlertRule, time.Minute, false, alertRuleOnce)

	// 注册 WebDAV 云备份（T-backup-001）：轮询间隔取设置里的最小粒度（1 小时），
	// 真正的"到点没到点"由 webDAVBackupDue 按 webdav_interval_hours 判断。
	// 任务恒注册，开关与地址每轮现读，用户改配置无需重启。
	Register(TaskWebDAVBackup, time.Hour, false, webDAVBackupOnce)
	// 首跑必须延迟：服务刚起来时出口内核还没把节点端口准备好，立刻探活会把**整池**判成不通
	// （实测 115 个节点全红），反而让健康筛选变成摆设。给内核 90 秒再探第一轮。
	go func() {
		time.Sleep(90 * time.Second)
		op.ProxyNodeProbeAll(context.Background())
		// 与定时路径保持一致：首跑也要检查一次，否则"刚启动就有渠道被坏节点挡住"
		// 这件事要等到下一个 5 分钟周期才被告知。
		proxyBlockedChannelNotify()
	}()
}
