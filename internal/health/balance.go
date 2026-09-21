package health

import "github.com/bestruirui/octopus/internal/model"

// T-quota-001 余额/额度采集与阈值告警（只读采集，不停用）。
// 设计来源：T-research-002 移植清单 P1/P2/P4/P5（Connector 三件套语义逐行重写，
// 未复制 api-monitor 文件）；new-api 系余额端点形状按其 connectors/newapi.go 的
// 语义重述：GET {base}/api/user/self 取剩余额度，宽容字段匹配。
//
// 本包只做"采集→指纹→事件"，不做通知通道（通知见 U-alert-001/T-research-004），
// 不做自动停用（停用见 T-quota-002）。失败只记事件，不阻断转发热路径。
const (
	// BalanceScanDefaultIntervalMinutes 是余额采集默认间隔（分钟）。
	// 低频避免烧上游额度；P5 口径。
	BalanceScanDefaultIntervalMinutes = 5

	// BalanceUserSelfPath 是 new-api 系余额端点相对路径。
	BalanceUserSelfPath = model.BalanceUserSelfPathDefault
)

// BalanceSnapshot 是一次余额采集的快照。
// Quota/Used/Remaining 单位为"额度点"（上游口径），本包不做币种换算。
type BalanceSnapshot struct {
	ChannelID int     `json:"channel_id"` // 被采集的渠道主键。
	Quota     float64 `json:"quota"`      // 总额度（上游口径）。
	Used      float64 `json:"used"`       // 已用额度（上游口径）。
	Remaining float64 `json:"remaining"`  // 剩余额度（上游口径）。
	Changed   bool    `json:"changed"`    // 相对上次指纹是否变化。
}

// ThresholdEvent 是余额低于阈值时产出的告警事件。
// 消费方为 U-alert-001；本包只产事件，不发通知。
type ThresholdEvent struct {
	ChannelID int     `json:"channel_id"` // 触发渠道主键。
	Remaining float64 `json:"remaining"`  // 触发时剩余额度。
	Threshold float64 `json:"threshold"`  // 触发阈值。
}
