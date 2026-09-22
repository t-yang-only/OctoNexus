package model

import "time"

// RelayLog 是已结束转发请求的持久化快照, 与进程内 RequestState 同源但落库保存。
// 内存快照重启即失且只留 50 条, 本表按保留期清理, 供日志页历史查询与筛选。
type RelayLog struct {
	ID             uint64 `json:"id" gorm:"primaryKey;autoIncrement"`
	RequestID      uint64 `json:"request_id" gorm:"index"`
	Status         string `json:"status" gorm:"index"`
	Model          string `json:"model" gorm:"index"`
	GroupID        int    `json:"group_id" gorm:"index"`
	APIKeyName     string `json:"api_key_name" gorm:"index"`
	TargetChannel  string `json:"target_channel" gorm:"index"`
	TargetModel    string `json:"target_model"`
	TargetProtocol int    `json:"target_protocol"`
	// ReportedModel 是**上游响应体里回报的 model 字段**（T-verify-001）。
	//
	// 与 TargetModel 的区别是关键：TargetModel 是「我们发出去的名字」，代表不了上游实际用了什么；
	// ReportedModel 是「上游自己说它用了什么」。两者不一致就是上游偷换模型的直接证据
	// ——按 opus 收费却用 haiku 出货，只看我方记录永远发现不了。
	// 空串表示上游没回报该字段（部分站点响应里就没有），此时不做判定。
	ReportedModel string `json:"reported_model"`
	// ModelMismatch 标记「上游回报的模型与请求的不一致」。
	//
	// 只在双方都有值时判定：上游不回报模型名是普遍现象，把"没回报"当"不匹配"
	// 会让绝大多数正常请求被误标，这个标记就失去了意义。
	ModelMismatch bool      `json:"model_mismatch" gorm:"index"`
	StartedAt     time.Time `json:"started_at" gorm:"index"`
	FirstByteMs   int64     `json:"first_byte_ms"` // 请求到达至首字节写出的毫秒数, 未提交为 -1。
	DurationMs    int64     `json:"duration_ms"`   // 请求总耗时毫秒数。
	// Attempts 是本请求打向上游的轮次数: 1 表示第一次就出结果, >1 表示中途换过成员(重试/换人),
	// 0 表示还没发起过上游请求就结束了(分组不存在、成员解析失败等)。
	// 首字竞速的多路并发算**一轮**(它们同时发出, 抢的是同一个逻辑轮次), 与面板上的"第几轮"同口径。
	Attempts int `json:"attempts"`
	// Decision 是这次请求的选路判定（T-decision-001）: 形如
	// "mode=smart;tier=decision;reason=affinity;slot=1;attempt=2"。
	// 回答"为什么走了这个成员"——模式、命中的档位、决定这次选择的机制（亲和保持/冷却恢复探测/
	// 成员顺序/综合排序/人工指定）、成员在分组里的顶层序号与轮次。不含渠道名与凭据。
	// 未发起上游请求就结束的请求为空串。
	Decision       string    `json:"decision"`
	PromptTokens   int64     `json:"prompt_tokens"`
	CachedTokens   int64     `json:"cached_tokens"`
	CompletionToks int64     `json:"completion_tokens"`
	Cost           float64   `json:"cost"`
	Error          string    `json:"error"`
	CreatedAt      time.Time `json:"created_at" gorm:"autoCreateTime;index"`
}

// RelayLogFilter 是历史查询的筛选条件, 空值表示不过滤。
// Q 为关键字, 在模型/渠道/错误信息三列做 LIKE 匹配。
type RelayLogFilter struct {
	Status  string
	Model   string
	Channel string
	APIKey  string
	Q       string
	Limit   int
	Offset  int
}

// RelayLogRetentionDays 是历史日志保留天数, 清理任务按 CreatedAt 删除更早的记录。
const RelayLogRetentionDays = 7

// RelayLogPageMaxLimit 是单页最大条数, 防止无界查询。
const RelayLogPageMaxLimit = 200
