package model

import "time"

// UsageHourly 是分模型×小时的用量明细桶（NM-CUR-025 简洁监控约束裁决）:
// 记录真实转发流量（被动聚合, 主动探活不进本表）, 保留 180 天滚动删除。
// 维度取整点到小时 + 目标模型名 + 渠道名（展示用冗余, 免前端联表）;
// 指标复用 StatsMetrics 口径, 前端成功率/平均延迟/费用计算逻辑与渠道统计一致。
type UsageHourly struct {
	Hour        string `json:"hour" gorm:"primaryKey;size:10"`          // 整点桶, 格式 2006010215。
	ModelName   string `json:"model_name" gorm:"primaryKey;size:255"`   // 实际请求上游的目标模型名。
	ChannelName string `json:"channel_name" gorm:"primaryKey;size:255"` // 承载调用的渠道名, 多渠道同名模型可分列展示。
	StatsMetrics
}

// UsageHourlyRetentionDays 是用量小时桶保留天数, 落库任务顺带清理更早数据（无独立 cron）。
const UsageHourlyRetentionDays = 180

// UsageRange 是用量查询的可选时间窗。
type UsageRange string

const (
	UsageRange24h UsageRange = "24h"
	UsageRange7d  UsageRange = "7d"
	UsageRange30d UsageRange = "30d"
)

// ValidUsageRange 校验时间窗取值。
func ValidUsageRange(r UsageRange) bool {
	switch r {
	case UsageRange24h, UsageRange7d, UsageRange30d:
		return true
	}
	return false
}

// UsageRangeCutoff 返回时间窗的起始整点（含）。
func UsageRangeCutoff(r UsageRange, now time.Time) time.Time {
	switch r {
	case UsageRange7d:
		return now.AddDate(0, 0, -7).Truncate(time.Hour)
	case UsageRange30d:
		return now.AddDate(0, 0, -30).Truncate(time.Hour)
	default:
		return now.Add(-24 * time.Hour).Truncate(time.Hour)
	}
}

// UsageHourKey 把时间截断到小时并格式化为桶主键。
func UsageHourKey(t time.Time) string {
	return t.Truncate(time.Hour).Format("2006010215")
}
