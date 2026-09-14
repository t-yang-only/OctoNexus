package model

import "time"

// QuotaAction 是余额归零停用/手动恢复的审计记录。
// T-quota-002 只做停用+恢复+审计：停用粒度为渠道凭据（ChannelKey.Enabled=false），
// 转发侧经 ChannelGrantGet（凭据停用即不可转发）自然生效；不删授权行，不动渠道整行。
type QuotaAction struct {
	ID           uint64    `json:"id" gorm:"primaryKey;autoIncrement"`
	CreatedAt    time.Time `json:"created_at" gorm:"autoCreateTime;index"`
	ChannelID    int       `json:"channel_id" gorm:"index"`
	ChannelKeyID int       `json:"channel_key_id" gorm:"index"`
	Action       string    `json:"action" gorm:"index"`
	Remaining    float64   `json:"remaining"`
	Actor        string    `json:"actor"`
	Note         string    `json:"note"`
}

// Quota 停用/恢复的动作常量。
const (
	QuotaActionAutoStop      = "auto_stop"
	QuotaActionManualRestore = "manual_restore"
)

// QuotaZeroThreshold 是归零判定线：剩余额度 <= 0 即归零。
// 注意 health.BelowThreshold 在阈值<=0 时永不触发（未配置语义），
// 故归零判定不走阈值事件阈值，在 op 层直接按剩余值判定。
const QuotaZeroThreshold = 0

// QuotaActionPageMaxLimit 是审计查询单页最大条数，防止无界查询。
const QuotaActionPageMaxLimit = 200
