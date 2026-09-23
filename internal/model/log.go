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
	// AttemptDetail 是**每一轮尝试的明细链**（T-trace-001），按轮次顺序记录打向上游的每个成员
	// 及其结果。只存已经结束的轮次 —— 最后一轮（成功/终态失败的那轮）由既有字段
	// TargetChannel / TargetModel / DurationMs / Error 表达，因此不进本字段，避免同一事实存两份。
	//
	// 为什么必须落库而不是靠既有字段反推：Attempts 只告诉你"试了几次"，TargetChannel 只告诉你
	// "最后用了谁"。**中间试过谁、各自为什么失败，在既有结构里没有任何位置** ——
	// 而"换个成员就好了"的判断恰恰只能从这里得出：某个成员每次都失败、每次都要绕开它，
	// 从最终结果上看和"这个分组有点慢"毫无区别。实测动机：生产某次请求 attempts=6、
	// 另一次 attempts=10，事后完全无法回答"是哪几个成员在拖"。
	//
	// 与 RelayLog.FaultKind 同源：这里的 fault_kind 也用产生错误那一刻的状态码分类，
	// 不从错误文本反推（措辞千变万化，必然误判）。Error 存上游原文供人看原文。
	AttemptDetail []RelayAttemptDetail `json:"attempt_detail" gorm:"serializer:json"`
	// AttemptsTruncated 标记尝试链是否被截断（只保留前 RelayAttemptDetailMax 轮）。
	// 存在的理由：截断后链条不再完整，若静默丢弃，读的人会以为"就试了这些"。
	AttemptsTruncated bool `json:"attempts_truncated"`
	// Decision 是这次请求的选路判定（T-decision-001）: 形如
	// "mode=smart;tier=decision;reason=affinity;slot=1;attempt=2"。
	// 回答"为什么走了这个成员"——模式、命中的档位、决定这次选择的机制（亲和保持/冷却恢复探测/
	// 成员顺序/综合排序/人工指定）、成员在分组里的顶层序号与轮次。不含渠道名与凭据。
	// 未发起上游请求就结束的请求为空串。
	Decision       string  `json:"decision"`
	PromptTokens   int64   `json:"prompt_tokens"`
	CachedTokens   int64   `json:"cached_tokens"`
	CompletionToks int64   `json:"completion_tokens"`
	Cost           float64 `json:"cost"`
	Error          string  `json:"error"`
	// FaultKind 是这次失败的**归因分类**（T-usability-007），空串表示非失败或未分类。
	//
	// 取值与 relay 的失败处置口径一一对应（internal/relay/retry.go 的 classifyUpstreamFailure）：
	//   "request"   —— 请求本身非法（400/405/406/413/414/415/422/501）。
	//                  任何成员都会同样拒绝，**不该算进渠道的通过率** ——
	//                  它不是渠道故障，是请求或配置的问题。
	//   "member"    —— 成员自身问题（401/402/403/404/407）：凭据无效、无权限、模型不存在。
	//                  算渠道故障，换一个成员可能就好了。
	//   "transient" —— 可恢复（408/409/429/5xx/网络/超时）：算渠道故障。
	//
	// 为什么必须落库而不是查询时从 Error 文本反推：Error 存的是**上游原文**，
	// 措辞千变万化（同一个 400 在不同站点长得完全不同），从文本反推必然误判。
	// 分类只在产生错误的那一刻能准确拿到（那里有状态码），所以在那时记下来。
	//
	// 实测动机：senseaudio 的通过率一度显示 31.6%，看起来像渠道坏了，
	// 实际上那 25 次失败全是「用 chat 接口调 TTS/图像等专用模型」造成的请求非法 ——
	// 把它算进渠道通过率，会让用户去修一个根本没坏的东西。
	FaultKind string    `json:"fault_kind" gorm:"index"`
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime;index"`
}

// RelayAttemptDetail 是单轮尝试的落库快照（T-trace-001）。
//
// 只记"谁 + 什么原因"，不记响应体与请求体：排查"为什么换人"用不到正文，
// 落了反而让日志体积与隐私面一起膨胀。
type RelayAttemptDetail struct {
	// Round 是这一轮在请求内的序号, 从 1 起, 与面板上的"第几轮"同口径。
	Round int `json:"round"`
	// Channel / Model 是这一轮实际打向上游的渠道名与模型名。
	Channel string `json:"channel"`
	Model   string `json:"model"`
	// WaitMs 是这一轮从发起到判定的耗时毫秒数。
	WaitMs int64 `json:"wait_ms"`
	// FaultKind 取值与 RelayLog.FaultKind 一致（request / member / transient）；
	// 人工中止的轮次为空串 —— 那不是上游的问题，不该算进任何一类失败。
	FaultKind string `json:"fault_kind,omitempty"`
	// Error 是上游返回的错误原文；空串表示本轮没有报错（由下一轮接管说明本轮实际未成功提交）。
	Error string `json:"error,omitempty"`
}

// RelayAttemptDetailMax 是尝试链最多保留的轮数。
//
// 上限存在的理由是"一次异常请求不该让单行日志无界增长"：生产实测最大轮次到过 10，
// 保留 32 轮已有 3 倍余量；超出时从**头部**截断并置 AttemptsTruncated，
// 因为排查换人问题时靠近终态的几轮信息量更大。
const RelayAttemptDetailMax = 32

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
