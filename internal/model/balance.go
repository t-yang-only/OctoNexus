package model

import "strings"

// 总余额聚合（T-balance-001）的数据形状: 一份快照回答「上游还能用多少钱、还剩多少次」。
//
// 口径（只用既有事实, 不新增采集）:
//   - 渠道余额 = 配额扫描/号池刷新读到的剩余额度（上游点数, op.ChannelBalance 的内存快照）,
//     按 balance_points_per_unit 折算成货币值; 默认 500000 点 = 1 个货币单位（new-api 惯例）。
//   - 剩余次数 = 渠道配置的包月额度 - 已用量; 未配置包月的渠道不计入, 不编 0 当"用完了"。
//   - 客户端 Key 的额度 = MaxCost（上限）与累计已花; MaxCost<=0 视为不限。它是"你允许花多少",
//     不是"上游还有多少", 因此不计入 Total, 只作为明细与调用方自查。
//
// 未知余额的渠道不计入 Total、只计入 UnknownChannels: 把"没读到"当 0 会显示成"没钱了",
// 当作有值又会虚高, 两者都会让用户做出错误判断。
const (
	// BalanceDefaultPointsPerUnit 是缺省换算口径: new-api 系 1 个货币单位 = 500000 额度点。
	BalanceDefaultPointsPerUnit = 500000.0
	// BalanceDefaultCurrency 是缺省货币名, 仅用于显示。
	BalanceDefaultCurrency = "USD"
)

// NormalizeBalanceUnit 归一化换算口径: 点数非正回落默认, 货币留空回落默认。
//
// 恒返回可用口径而不报错: 总额必须永远算得出来, 坏配置按默认口径走（面板会提示用户修正）。
func NormalizeBalanceUnit(pointsPerUnit float64, currency string) (float64, string) {
	if pointsPerUnit <= 0 {
		pointsPerUnit = BalanceDefaultPointsPerUnit
	}
	currency = strings.TrimSpace(currency)
	if currency == "" {
		currency = BalanceDefaultCurrency
	}
	return pointsPerUnit, currency
}

// ConvertBalancePoints 把上游额度点折算成货币值; 换算口径非法时按默认口径, 0 点仍是 0。
func ConvertBalancePoints(points, pointsPerUnit float64) float64 {
	if pointsPerUnit <= 0 {
		pointsPerUnit = BalanceDefaultPointsPerUnit
	}
	return points / pointsPerUnit
}

// ChannelBalanceRow 是一条渠道（中转/站点）的余额与剩余次数明细。
type ChannelBalanceRow struct {
	ChannelID        int     `json:"channel_id"`
	ChannelName      string  `json:"channel_name"`
	Enabled          bool    `json:"enabled"`
	Known            bool    `json:"known"`     // 是否读到过余额; false 时 Remaining/Balance 无意义
	Remaining        float64 `json:"remaining"` // 上游口径的剩余额度点
	Balance          float64 `json:"balance"`   // 折算后的货币值
	BillingMode      string  `json:"billing_mode"`
	Multiplier       float64 `json:"multiplier"`
	PerCallPrice     float64 `json:"per_call_price"`
	MonthlyQuota     float64 `json:"monthly_quota"`
	MonthlyUsed      float64 `json:"monthly_used"`
	MonthlyRemaining float64 `json:"monthly_remaining"` // 剩余次数（包月额度 - 已用）
	// BalanceSource 说明这条余额是哪来的：api = 从上游读到的，manual = 人在面板里录的（无接口站点）。
	BalanceSource string `json:"balance_source,omitempty"`
	KeyCount         int     `json:"key_count"`         // 该渠道的凭据总数
	KeyEnabled       int     `json:"key_enabled"`       // 其中启用中的凭据数
}

// APIKeyBalanceRow 是一个客户端 API Key 的额度自查（上限/已花/剩余）。
type APIKeyBalanceRow struct {
	ID        int     `json:"id"`
	Name      string  `json:"name"`
	Enabled   bool    `json:"enabled"`
	Limit     float64 `json:"limit"`     // MaxCost; 0 表示不限
	Used      float64 `json:"used"`      // 累计已花（输入 + 输出费用）
	Remaining float64 `json:"remaining"` // 剩余额度; 不限额度时为 0
	Unlimited bool    `json:"unlimited"`
	Requests  int64   `json:"requests"` // 成功 + 失败请求数
	Tokens    int64   `json:"tokens"`   // 输入 + 输出词元数
}

// BalanceSummary 是总余额快照: 总额 + 逐渠道明细 + 逐 Key 明细。
type BalanceSummary struct {
	Total                 float64             `json:"total"`                   // 总余额（折算后）
	TotalMonthlyRemaining float64             `json:"total_monthly_remaining"` // 总剩余次数（包月口径求和）
	Currency              string              `json:"currency"`
	PointsPerUnit         float64             `json:"points_per_unit"`
	KnownChannels         int                 `json:"known_channels"`
	UnknownChannels       int                 `json:"unknown_channels"`
	Channels              []ChannelBalanceRow `json:"channels"`
	// ManualTotal 是手动订阅贡献的余额（已计入 Total）；ManualSubscriptions 是全部手动记录的明细。
	ManualTotal         float64                 `json:"manual_total"`
	ManualSubscriptions []ManualSubscriptionRow `json:"manual_subscriptions"`
	ManualExpired       int                     `json:"manual_expired"`
	Keys                  []APIKeyBalanceRow  `json:"keys"`
	GeneratedAt           int64               `json:"generated_at"`
}
