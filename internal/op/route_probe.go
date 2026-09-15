package op

import (
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// R-probe-001 主动探活的两项设置读取入口 (与 quota_scan.go 同构)。
// 默认关闭 + 默认 300 秒: 每次探测都是一次真实计费请求, 花不花这笔钱由用户显式决定;
// 周期按秒计, 便于把间隔压到分钟级以下做联调, 0 表示停用探活任务。

// defaultRouteProbeIntervalSeconds 是设置缺失/非法时的探活周期兜底值。
const defaultRouteProbeIntervalSeconds = 300

// RouteProbeEnabled 读取主动探活开关; 设置缺失或非法一律按关闭处理 (宁可不探, 不可擅自花钱)。
func RouteProbeEnabled() bool {
	enabled, err := SettingGetBool(model.SettingKeyRouteProbeEnabled)
	if err != nil {
		return false
	}
	return enabled
}

// RouteProbeInterval 读取主动探活周期; 设置缺失/非法回退默认 300 秒, 负数按 0 (停用) 处理。
func RouteProbeInterval() time.Duration {
	seconds, err := SettingGetInt(model.SettingKeyRouteProbeInterval)
	if err != nil {
		seconds = defaultRouteProbeIntervalSeconds
	}
	if seconds < 0 {
		seconds = 0
	}
	return time.Duration(seconds) * time.Second
}
