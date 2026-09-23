package relay

// T-trace-003 请求终止原因的结构化记录（吸收 new-api 的 PolicyDecision 设计）。
//
// ## 补的是哪个盲区
//
// octopus 已有两个"为什么"：
//
//	Decision（T-decision-001） 为什么**走了这个**成员（选路判定）
//	FaultKind（T-usability-007）这次失败**算谁的账**（归因分类）
//
// 但缺少第三个：**为什么停下来了**。请求最终 502 失败时，现有字段只能说明
// "最后一个成员失败了"，不能区分这几种完全不同的情形：
//
//	预算用尽     —— 成员在反复失败，试到上限才放弃（可能是上游大面积故障）
//	权威拒绝     —— 上游说请求本身非法，全部成员都拒绝（改请求才对）
//	成员故障     —— 成员自身问题（凭据/权限/模型不存在），重试无意义
//	客户端取消   —— 用户自己走了，不是任何一方的问题
//
// 这四种在日志上都长成"一次失败"，但处置动作完全不同：看预算用尽要查上游，
// 看权威拒绝要改请求，看成员故障要修凭据，看客户端取消什么都别做。
// 只说"失败了"，用户就得自己从错误文本里猜。
//
// ## 为什么必须与 new-api 一样带 Source
//
// Reason 说的是"哪条规则终止的"，Source 说的是"这条规则从哪来"。
// 同一个 Reason 可能来自系统硬编码或用户配置 —— 例如
// Reason=request_rejected 在 failfast 配置下是用户的设置终止的，
// 而在 failover 下是系统"全体成员都拒绝了"终止的。处置建议不同：
// 前者改设置、后者改请求。没有 Source 就分不清该改哪个。

// StopAction 是终止请求这一动作本身的取值。
// 目前只有 stop：这个结构记录的永远是"终止"，保留 Action 字段是为了
// 与 new-api 的 PolicyDecision 同形（它的决策既可能是 retry 也可能是 stop），
// 将来若要记录"继续重试"的决策时不必换结构。
const stopAction = "stop"

// 终止原因（Reason）取值。命名与项目既有口径对齐：
//   - 预算类与 attemptCap 同源
//   - 拒绝类与 RequestFaultAction（failover/failfast）同源
//   - 取消类与 classifyUpstreamFailure 的客户端取消口径同源
const (
	// stopReasonCompleted 请求正常结束（成功、或流式已提交后结束）。
	// 绝大多数请求都是这个值 —— 它不是异常，界面上不必特别提示。
	stopReasonCompleted = "request_completed"
	// stopReasonBudget 尝试次数用尽（attemptCap）。
	stopReasonBudget = "attempt_budget_exhausted"
	// stopReasonAllRejected 全部成员都判定该请求非法（failover 模式的终点）。
	stopReasonAllRejected = "all_members_rejected"
	// stopReasonFailFast 首个成员判定请求非法，配置要求立即结束。
	stopReasonFailFast = "request_fault_failfast"
	// stopReasonMemberFault 成员自身问题且已无可用成员（凭据/权限/模型不存在）。
	stopReasonMemberFault = "member_fault_no_alternative"
	// stopReasonNoMember 分组内没有可转发的成员。
	stopReasonNoMember = "no_available_member"
	// stopReasonClientCancel 客户端主动断开或人工中止。
	stopReasonClientCancel = "client_canceled"
	// stopReasonCommitGuard 已向上游提交（首字节已写出）后的失败，不能再换成员。
	stopReasonCommitGuard = "response_committed"
)

// 终止来源（Source）取值：这条规则从哪来。
const (
	stopSourceSystem   = "system"   // 代码里的固定规则
	stopSourceConfig   = "config"   // 用户设置（分组配置或全局设置）
	stopSourceClient   = "client"   // 客户端行为（主动断开）
	stopSourceUpstream = "upstream" // 上游的响应（如全部成员都返回 400）
)

// StopReason 是一次请求终止原因的结构化快照（T-trace-003）。
//
// 落库形态与 Decision/FaultKind 一致：随 relay_logs 一起持久化，
// 前端只做展示，不重新推断。
type StopReason struct {
	// Action 固定为 stop（保留字段是为了与既有决策记录同形）。
	Action string `json:"action"`
	// Reason 是终止规则本身，取值见上面常量。
	Reason string `json:"reason"`
	// Source 说明该规则由谁决定。
	Source string `json:"source"`
	// Attempts 终止时已发起的上游尝试次数，便于判断"试了几次才放弃"。
	Attempts int `json:"attempts,omitempty"`
}

// Text 返回一行式的可读文本，与 Decision.Text() 同样的口径：
// 既有结构化的键值（便于检索），也在一行里能读懂。
func (s StopReason) Text() string {
	if s.Reason == "" {
		return ""
	}
	return "action=" + s.Action + ";reason=" + s.Reason + ";source=" + s.Source
}

// stopReasonText 在只拿得到 reason/source 时构造文本（避免为了一行日志
// 去构造整个结构体）。
func stopReasonText(reason, source string) string {
	return "action=" + stopAction + ";reason=" + reason + ";source=" + source
}
