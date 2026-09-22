package model

import (
	"fmt"
	"time"
)

// 告警规则引擎（吸收上游 lingyuins/octopus 的 Alerts：错误率 / 延迟 / 渠道宕机）。
//
// 与既有通知的关系：本项目本来就会在**具体事件**上发通知（余额归零、探活恢复）。
// 这里补的是**指标型规则**——"某个渠道最近 15 分钟错误率超过 30%"这类判断，
// 靠事件驱动的通知覆盖不到（失败是散落的，没有单一事件可以挂钩）。
//
// 三条设计取舍，都是为了让告警"能用"而不是"吵人"：
//   - **样本量下限（MinRequests）**：窗口里只有 1 次请求且失败时，错误率是 100%，
//     但这只是"偶然"而不是"故障"。低于下限一律不判定——这是告警系统最常见的噪音来源。
//   - **冷却（CooldownMinutes）**：同一条规则触发后进入冷却，避免一分钟一条把人淹没。
//   - **按渠道分别判定**：一条规则可以覆盖全部渠道，但判定与冷却都是**逐渠道**独立的，
//     否则一个坏渠道会把好渠道的告警名额吃掉。
type AlertMetric string

const (
	AlertMetricErrorRate AlertMetric = "error_rate" // 窗口内失败占比（%）
	AlertMetricLatency   AlertMetric = "latency"    // 窗口内平均耗时（毫秒）
)

// IsValidAlertMetric 指标口径是否合法。
func IsValidAlertMetric(value string) bool {
	switch AlertMetric(value) {
	case AlertMetricErrorRate, AlertMetricLatency:
		return true
	}
	return false
}

// AlertScope 规则作用范围。
type AlertScope string

const (
	AlertScopeAll     AlertScope = "all"     // 覆盖全部渠道（逐渠道独立判定）
	AlertScopeChannel AlertScope = "channel" // 只盯指定渠道
)

// IsValidAlertScope 作用范围是否合法。
func IsValidAlertScope(value string) bool {
	switch AlertScope(value) {
	case AlertScopeAll, AlertScopeChannel:
		return true
	}
	return false
}

// AlertRule 是一条告警规则。
type AlertRule struct {
	ID     int         `json:"id" gorm:"primaryKey;autoIncrement"`
	Name   string      `json:"name" gorm:"size:255;not null"`
	Metric AlertMetric `json:"metric" gorm:"size:32;not null"`
	Scope  AlertScope  `json:"scope" gorm:"size:32;not null"`
	// ScopeValue 在 scope=channel 时是渠道名；scope=all 时忽略。
	ScopeValue string `json:"scope_value" gorm:"size:255"`
	// Threshold 是触发阈值，单位随 metric 变：错误率是百分比（30 = 30%），延迟是毫秒。
	Threshold float64 `json:"threshold" gorm:"not null"`
	// WindowMinutes 是滑动窗口长度。
	WindowMinutes int `json:"window_minutes" gorm:"not null;default:15"`
	// MinRequests 是样本量下限：窗口内请求数低于它就不判定。
	// 没有这一条，一次偶然失败就会被算成 100% 错误率，告警会变成噪音源。
	MinRequests int `json:"min_requests" gorm:"not null;default:5"`
	// CooldownMinutes 是同一规则同一渠道的冷却时长，避免持续故障刷屏。
	CooldownMinutes int  `json:"cooldown_minutes" gorm:"not null;default:60"`
	Enabled         bool `json:"enabled" gorm:"not null"`
	// LastFiredAt 是最近一次触发时间（用于冷却判定），0 表示从未触发。
	LastFiredAt int64     `json:"last_fired_at"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// AlertRuleCreateRequest 是创建规则的载荷。
type AlertRuleCreateRequest struct {
	Name            string      `json:"name" binding:"required"`
	Metric          AlertMetric `json:"metric" binding:"required,oneof=error_rate latency"`
	Scope           AlertScope  `json:"scope" binding:"required,oneof=all channel"`
	ScopeValue      string      `json:"scope_value"`
	Threshold       float64     `json:"threshold"`
	WindowMinutes   int         `json:"window_minutes"`
	MinRequests     int         `json:"min_requests"`
	CooldownMinutes int         `json:"cooldown_minutes"`
	Enabled         *bool       `json:"enabled"`
}

func (AlertRuleCreateRequest) TableName() string { return "-" }

// AlertRuleUpdateRequest 是更新规则的载荷（全指针，只改传了的字段）。
type AlertRuleUpdateRequest struct {
	Name            *string      `json:"name"`
	Metric          *AlertMetric `json:"metric" binding:"omitempty,oneof=error_rate latency"`
	Scope           *AlertScope  `json:"scope" binding:"omitempty,oneof=all channel"`
	ScopeValue      *string      `json:"scope_value"`
	Threshold       *float64     `json:"threshold"`
	WindowMinutes   *int         `json:"window_minutes"`
	MinRequests     *int         `json:"min_requests"`
	CooldownMinutes *int         `json:"cooldown_minutes"`
	Enabled         *bool        `json:"enabled"`
}

func (AlertRuleUpdateRequest) TableName() string { return "-" }

// AlertFire 是一次触发记录（落库供面板回溯"什么时候报过什么"）。
//
// 单独一张表而不是复用 LastFiredAt：面板要回答的是"这条规则最近报过几次、报的是哪个渠道"，
// 只留一个时间戳答不了；而告警历史本身也是排查上游问题的线索。
type AlertFire struct {
	ID        uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	RuleID    int       `json:"rule_id" gorm:"index"`
	RuleName  string    `json:"rule_name" gorm:"size:255"`
	Channel   string    `json:"channel" gorm:"size:255;index"`
	Metric    string    `json:"metric" gorm:"size:32"`
	Value     float64   `json:"value"`     // 触发时的实际值
	Threshold float64   `json:"threshold"` // 规则阈值
	Requests  int64     `json:"requests"`  // 窗口内样本量
	Message   string    `json:"message"`   // 人类可读说明
	FiredAt   time.Time `json:"fired_at" gorm:"index"`
}

// Validate 校验创建参数，并补齐越界/缺省的数值字段。
//
// 数值一律**夹回合法区间**而不是拒绝：这些是"填得不太对但意图清楚"的参数
// （窗口 0 分钟显然是想用默认值），当场拒绝只会让用户反复试。
// 唯独阈值必须 > 0——0 阈值的规则每次评估都会触发，那是配置错误而不是意图。
func (req *AlertRuleCreateRequest) Validate() error {
	req.Name = trimSpace(req.Name)
	req.ScopeValue = trimSpace(req.ScopeValue)
	if req.Name == "" {
		return fmt.Errorf("name is required")
	}
	if !IsValidAlertMetric(string(req.Metric)) {
		return fmt.Errorf("invalid metric: must be error_rate or latency")
	}
	if !IsValidAlertScope(string(req.Scope)) {
		return fmt.Errorf("invalid scope: must be all or channel")
	}
	if req.Scope == AlertScopeChannel && req.ScopeValue == "" {
		return fmt.Errorf("scope_value (channel name) is required when scope is channel")
	}
	if req.Threshold <= 0 {
		return fmt.Errorf("threshold must be greater than 0")
	}
	if req.Metric == AlertMetricErrorRate && req.Threshold > 100 {
		return fmt.Errorf("error_rate threshold must be between 0 and 100")
	}
	req.WindowMinutes = clampInt(req.WindowMinutes, 1, 1440, 15)
	req.MinRequests = clampInt(req.MinRequests, 1, 100000, 5)
	req.CooldownMinutes = clampInt(req.CooldownMinutes, 0, 10080, 60)
	return nil
}

func trimSpace(value string) string {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\t' || value[start] == '\n' || value[start] == '\r') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\t' || value[end-1] == '\n' || value[end-1] == '\r') {
		end--
	}
	return value[start:end]
}

// clampInt 把值夹回 [min, max]；value<=0 时取 fallback（"没填"的语义）。
func clampInt(value, min, max, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// 下面三个是给写路径（op）复用的夹回函数：更新规则时只传了某几个数值字段，
// 需要与创建路径用同一套区间口径，否则"创建时夹回、更新时不夹"会让区间形同虚设。
func ClampAlertWindow(value int) int      { return clampInt(value, 1, 1440, 15) }
func ClampAlertMinRequests(value int) int { return clampInt(value, 1, 100000, 5) }
func ClampAlertCooldown(value int) int    { return clampInt(value, 0, 10080, 60) }
