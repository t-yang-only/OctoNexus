package relay

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/looplj/axonhub/llm"
)

// 客户端请求在转发过程中的当前状态。
type Status string

const (
	StatusRunning   Status = "running"   // 循环中: 正在选目标, 等待或请求上游。
	StatusCommitted Status = "committed" // 首字节已写出客户端, 此后不可再重试。
	StatusSuccess   Status = "success"   // 响应已完整交付客户端。
	StatusFailed    Status = "failed"    // 请求以错误结束。
	StatusCanceled  Status = "canceled"  // 客户端提前断开或取消。
)

// 客户端请求的完整进程内状态, 同时作为状态流的消息形状; 上半部分在请求到达时写入并在结束时定稿, 下半部分每轮循环覆盖。
type RequestState struct {
	ID         uint64         `json:"id"`           // 请求在当前进程内的唯一标识。
	Status     Status         `json:"status"`       // 请求当前状态。
	StartedAt  time.Time      `json:"started_at"`   // 请求到达时间。
	Duration   time.Duration  `json:"duration"`     // 请求总耗时, 未结束时为零。
	Model      string         `json:"model"`        // 客户端请求的模型名称, 即分组名称。
	Protocol   model.Protocol `json:"protocol"`     // 客户端请求使用的协议, 由入站格式定出, 单个协议位而非掩码组合。
	GroupID    int            `json:"group_id"`     // 承载本请求的分组 ID, 供界面按主键直接定位分组而不必按名称回查。
	APIKeyName string         `json:"api_key_name"` // 发起请求时的 API Key 名称。
	// ReasoningEffort 是客户端在请求里指定的思考强度（T-insight-001），空串表示没指定。
	//
	// 它是**请求侧**的属性，与 Usage 里的推理 token 数（结果侧）配对回答
	// "这条请求为什么这么慢、这么贵"：强度是原因，token 数是结果。
	// 绝大多数请求不带这个参数，因此它不参与选路，只做记录与展示。
	ReasoningEffort string    `json:"reasoning_effort,omitempty"`
	Usage           llm.Usage `json:"usage"` // 请求结束时写入的展示用量。
	Cost            float64   `json:"cost"`  // 请求结束时写入的累计费用。

	Round          int            `json:"round"`            // 最新一轮循环的递增序号, 人工中止按此匹配以免误杀下一轮。
	RoundStartedAt time.Time      `json:"round_started_at"` // 最新一轮上游请求的开始时间。
	FirstByteAt    time.Time      `json:"first_byte_at"`    // 首字节写出客户端的时间, 未提交时为零值; 富化卡片的首字耗时即 FirstByteAt-StartedAt。
	TargetChannel  string         `json:"target_channel"`   // 最新一轮选中的渠道名称。
	TargetModel    string         `json:"target_model"`     // 最新一轮实际请求上游的模型名称。
	TargetProtocol model.Protocol `json:"target_protocol"`  // 最新一轮实际请求上游的协议, 与 Protocol 不同即本轮做了跨协议转换; 0 表示尚未选出。
	// ReportedModel 是上游响应里回报的模型名（T-verify-001），空串表示上游没回报该字段。
	// 与 TargetModel 的差别是这件事的全部意义：前者是"我们请求了什么"，后者是"上游自称用了什么"。
	ReportedModel string `json:"reported_model,omitempty"`
	// ModelMismatch 标记两者不一致（仅在双方都有值时判定）。
	// 它回答"上游有没有偷换模型"——按高价模型收费却用低价模型出货，只看我方记录永远发现不了。
	ModelMismatch bool   `json:"model_mismatch,omitempty"`
	Sending       bool   `json:"sending"`            // 最新一轮是否仍在等待上游响应。
	TargetItemID  int    `json:"-"`                  // 最新一轮选中的成员行 ID（GroupItem.ID）; 仅供 least_busy 统计在途, 不进状态流与日志对外结构。
	Decision      string `json:"decision,omitempty"` // 本轮选路判定（T-decision-001）: "mode=smart;tier=decision;reason=affinity;slot=1;attempt=2", 每轮刷新。
	// AttemptChain 是**已结束轮次**的尝试明细（T-trace-001），按轮次顺序追加。
	//
	// 只收已结束的轮次，当前进行中的那轮由 Target* 字段表达 —— 两者不重叠，
	// 于是"链上全部轮次 + 当前轮"恒等于本次请求打过的全部成员，不会漏也不会重。
	//
	// 为什么在中间轮归档、而不是等终态一次性收集：每一轮结束时目标字段会被下一轮覆盖
	// （startRound 直接赋值），中间信息在那一刻就永久丢失了，事后无从重建。
	AttemptChain []model.RelayAttemptDetail `json:"attempt_chain,omitempty"`
	// AttemptsTruncated 标记尝试链是否因超长被截断（只保留后 RelayAttemptDetailMax 轮）。
	AttemptsTruncated bool   `json:"attempts_truncated,omitempty"`
	Error             string `json:"error,omitempty"` // 最新一轮的失败原因, 请求结束后即为最终错误。
	// FaultKind 是这次失败的归因分类（T-usability-007），只在失败终态时有值。
	// 取值见 model.RelayLog.FaultKind 的注释；分类必须在**产生错误的那一刻**做，
	// 因为那时才有状态码可用（落库时只剩错误文本，从文本反推会误判）。
	FaultKind string `json:"fault_kind,omitempty"`
	// StopReason 记录**为什么停下来**（T-trace-003），终态时有值。
	//
	// 与 FaultKind 的分工：FaultKind 说"这次失败算谁的账"，StopReason 说"哪条规则
	// 终止了请求、这条规则从哪来"。两者都缺一不可 —— 同样是失败终态，
	// "预算用尽"（查上游）与"全体成员判定请求非法"（改请求）的处置完全不同，
	// 只看 FaultKind 区分不出来。
	StopReason string `json:"stop_reason,omitempty"`

	body          string                                     // 客户端原始请求体, 体积大故不进状态流, 由独立接口按需拉取。
	responseBody  string                                     // 聚合后的完整最终响应体, 同样按需拉取。
	apiKeyID      int                                        // 发起请求的 API Key ID, 用于请求完成后的归属统计。
	usageRecorder func(promptTokens, completionTokens int64) // Key 级 TPM 记账回调, 鉴权层注入, 终态时调用一次。
	cancel        context.CancelFunc                         // 中止最新一轮上游请求, 仅在该轮等待响应期间非空。

	// 以下三个字段是**本轮**的结果暂存（T-trace-001），供归档进 AttemptChain 时使用。
	// 单独存放而不是复用 Error/FaultKind 的原因：那两者是"最新一轮"的对外语义，
	// 终态时还会被 markFailed 覆写成最终归因；混用会让中间轮的分类被终态覆盖掉。
	roundWaitMs    int64
	roundFaultKind string
	roundErrText   string
}

const streamBuffer = 16 // 单个状态流连接的非阻塞消息缓冲容量。
const maxFinished = 50  // 进程内最多保留的已结束请求数量。

var (
	idSeq    atomic.Uint64                          // 进程内严格递增的请求 ID。
	mu       sync.Mutex                             // 全部共享状态的互斥锁。
	requests = make(map[uint64]*RequestState)       // 按请求 ID 保存的全部请求状态。
	watchers = make(map[chan RequestState]struct{}) // 全部状态流 SSE 连接。
)

// newRequestState 分配请求 ID 并登记初始运行状态; 返回的记录是本请求后续全部状态写入的入口。
// usageRecorder 由鉴权层经 AttachUsageRecorder 注入, 终态时把实际词元量交给 Key 级 TPM 记账。
func newRequestState(ctx context.Context, modelName string, groupID int, protocol model.Protocol, body string, reasoningEffort string, apiKeyID int) *RequestState {
	mu.Lock()
	defer mu.Unlock()

	request := &RequestState{
		ID:              idSeq.Add(1),
		Status:          StatusRunning,
		StartedAt:       time.Now(),
		Model:           modelName,
		Protocol:        protocol,
		GroupID:         groupID,
		ReasoningEffort: reasoningEffort,
		body:            body,
		apiKeyID:        apiKeyID,
	}
	// 登记时保存名称快照, 查询失败时留空。
	if apiKey, err := op.APIKeyGet(apiKeyID, ctx); err == nil {
		request.APIKeyName = apiKey.Name
	}
	requests[request.ID] = request
	publishRequestLocked(request)
	return request
}

// AttachUsageRecorder 给指定请求挂上 Key 级限流的记账回调 (鉴权中间件注入)。
// 请求记录不存在或回调为空时静默跳过, 同一请求重复注入以最后一次为准。
func AttachUsageRecorder(ctx context.Context, requestID uint64, recorder func(promptTokens, completionTokens int64)) {
	if recorder == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if request := requests[requestID]; request != nil {
		request.usageRecorder = recorder
	}
}

// startRound 记录本轮选中的目标并进入上游请求, cancel 供人工中止本轮, 返回递增的轮次序号。
func (r *RequestState) startRound(cancel context.CancelFunc, itemID int, channel, modelName string, protocol model.Protocol) int {
	mu.Lock()
	defer mu.Unlock()

	// 归档上一轮（T-trace-001）: 紧接着的赋值会覆盖 Target*, 这是还能读到它的最后时刻。
	r.archiveRoundLocked()

	r.Round++
	r.RoundStartedAt = time.Now()
	r.TargetItemID = itemID
	r.TargetChannel = channel
	r.TargetModel = modelName
	r.TargetProtocol = protocol
	r.Sending = true
	r.Error = ""
	r.roundWaitMs, r.roundFaultKind, r.roundErrText = 0, "", ""
	r.cancel = cancel
	publishRequestLocked(r)
	return r.Round
}

// archiveRoundLocked 把第 r.Round 轮追加进尝试链; 调用方必须持有锁。
//
// 两个调用点都归档"当前 r.Round 那一轮", 但时机不同因而语义不同:
//   - startRound 在自增**之前**调用 → 归档的是刚结束的上一轮;
//   - finishLocked 在终态时调用 → 归档的是最后一轮。
//
// 合起来覆盖全部轮次, 且每轮恰好归档一次。
func (r *RequestState) archiveRoundLocked() {
	if r.Round == 0 {
		return // 还没打过任何一轮上游（分组不存在、成员解析失败等）
	}
	r.AttemptChain = append(r.AttemptChain, model.RelayAttemptDetail{
		Round:     r.Round,
		Channel:   r.TargetChannel,
		Model:     r.TargetModel,
		WaitMs:    r.roundWaitMs,
		FaultKind: r.roundFaultKind,
		Error:     r.roundErrText,
	})
	if len(r.AttemptChain) > model.RelayAttemptDetailMax {
		// 从头部截断而非尾部: 排查"谁在拖后腿"时, 靠近终态的轮次信息量更大。
		r.AttemptChain = r.AttemptChain[len(r.AttemptChain)-model.RelayAttemptDetailMax:]
		r.AttemptsTruncated = true
	}
}

// finishRound 记录本轮上游结果, errText 为空表示已取得可提交响应。
// retargetRound 把当前轮的目标换成首字竞速的胜出者。
// 只改目标字段不递增轮次: 竞速是一个逻辑轮次内的多路尝试, 面板上仍是一轮。
func (r *RequestState) retargetRound(itemID int, channel, modelName string, protocol model.Protocol) {
	mu.Lock()
	defer mu.Unlock()
	target, ok := requests[r.ID]
	if !ok {
		return
	}
	target.TargetItemID = itemID
	target.TargetChannel = channel
	target.TargetModel = modelName
	target.TargetProtocol = protocol
	r.TargetItemID = itemID
	r.TargetChannel = channel
	r.TargetModel = modelName
	r.TargetProtocol = protocol
}

// recordReportedModel 记录上游响应里回报的模型名并判定是否与请求的一致（T-verify-001）。
//
// requested 由调用方显式传入而不是从 TargetModel 读：调用点手上就有这一轮真实发出去的
// 模型名（channelModel.Name），两者必然同源；从状态里回读反而依赖"TargetModel 已被正确设置"
// 这条隐含前提，那个前提一旦不成立，校验就会静默失去意义。
//
// 只记不拦：上游回报不同的模型名可能是别名、路由层改名或真的偷换，中转无法替用户裁决是哪一种；
// 把它如实记下来并标出不一致，由用户在日志页判断。
func (r *RequestState) recordReportedModel(requested, reported string) {
	reported = strings.TrimSpace(reported)
	mismatch := modelMismatch(requested, reported)

	mu.Lock()
	defer mu.Unlock()

	if target, ok := requests[r.ID]; ok {
		target.ReportedModel = reported
		target.ModelMismatch = mismatch
	}
	r.ReportedModel = reported
	r.ModelMismatch = mismatch
	publishRequestLocked(r)
}

// setDecision 记录本轮的选路判定（T-decision-001）: 与其它状态一样先写进注册表再落到本对象,
// 使状态流推送出去的副本也带上它。判定文本很短（不含渠道名与凭据）, 因此不额外做脱敏。
func (r *RequestState) setDecision(decision string) {
	mu.Lock()
	defer mu.Unlock()
	if target, ok := requests[r.ID]; ok {
		target.Decision = decision
	}
	r.Decision = decision
}

// finishRound 记录本轮上游调用已经结束及其结果（T-trace-001 起接收 error 对象而非文本）。
//
// 为什么参数从 errText string 改为 err error：本轮要落进尝试链的归因分类必须用**状态码**判，
// 而状态码只存在于 error 对象里；等到只剩文本时再分类只能靠猜（上游措辞千变万化）。
// 这与 markFailed 里 FaultKind 的处理是同一个道理，两处口径同源。
//
// aborted 表示本轮是被本地取消的（人工中止），既不是上游故障也不该出现在失败原因里：
// 归档时清空错误与分类、只保留耗时——否则一次人为中断会被永久记成"这个渠道坏了"。
func (r *RequestState) finishRound(err error, aborted bool) {
	mu.Lock()
	defer mu.Unlock()

	r.Sending = false
	r.cancel = nil
	r.roundWaitMs = time.Since(r.RoundStartedAt).Milliseconds()
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	if err != nil && !aborted {
		r.roundErrText = errText
		r.roundFaultKind = faultKindOf(err)
	} else {
		r.roundErrText, r.roundFaultKind = "", ""
	}
	// Error 保持既有语义不变: 最新一轮的失败原因（成功或人工中止的那轮会清空它）。
	r.Error = errText
	if aborted {
		r.Error = ""
	}
	publishRequestLocked(r)
}

// memberBusyCount 统计"当前仍在等这个成员响应"的请求数（NM-DS-006：least_busy 的数据来源）。
// 口径：只数 Sending 为真的请求，即从发起上游请求到本轮结束（提交 / 失败 / 取消）之间；
// 因为直接从活动请求注册表派生，不存在"计数漏释放"导致成员被永久当成忙的风险。
// 人工中止那一轮会停在 Sending 为真的状态直到下一轮开始，是已知的轻微高估（人工操作且下一轮即刻修正）。
// inFlightByModel 统计某个分组（按客户端模型名=分组名）当前在途的请求数。
// 与 memberBusyCount 同一份活动请求注册表: 结构上不可能漏释放, 也不需要成对 acquire/release。
func inFlightByModel(modelName string) int {
	if modelName == "" {
		return 0
	}
	mu.Lock()
	defer mu.Unlock()
	count := 0
	for _, state := range requests {
		if state.Sending && state.Model == modelName {
			count++
		}
	}
	return count
}

func memberBusyCount(itemID int) int {
	if itemID == 0 {
		return 0
	}
	mu.Lock()
	defer mu.Unlock()

	count := 0
	for _, request := range requests {
		if request.Sending && request.TargetItemID == itemID {
			count++
		}
	}
	return count
}

// Interrupt 中止指定请求仍在等待响应且轮次匹配的上游请求; 轮次不匹配说明该轮已结束, 不影响后续轮次。
func Interrupt(id uint64, round int) {
	mu.Lock()
	request := requests[id]
	if request == nil || request.Round != round || request.cancel == nil {
		mu.Unlock()
		return
	}
	cancel := request.cancel
	request.cancel = nil
	mu.Unlock()

	cancel()
}

// wait 在重新选择目标之前退避 seconds 秒; 客户端在退避期间断开时以取消终态定稿并返回 false。
func (r *RequestState) wait(ctx context.Context, seconds int) bool {
	select {
	case <-ctx.Done():
		r.markCanceled(ctx.Err(), "", nil)
		return false
	case <-time.After(time.Duration(seconds) * time.Second):
		return true
	}
}

// markCommitted 标记响应已提交; 流式响应在此之后仍会持续转发, 故必须先于提交动作调用。
// 首字节时间只记录第一次提交: 非流式一次写完, 流式首帧写出, 后续帧不再覆盖。
func (r *RequestState) markCommitted() {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusCommitted
	if r.FirstByteAt.IsZero() {
		r.FirstByteAt = time.Now()
	}
	publishRequestLocked(r)
}

// markSucceeded 以成功终态定稿请求。
func (r *RequestState) markSucceeded(responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusSuccess
	r.Error = ""
	r.responseBody = responseBody
	// 成功路径的终止原因固定为"请求正常结束"（T-trace-003）。
	// 这里直接写而不走 recordStopReason：两者语义完全确定，不需要出口来传；
	// 而且此刻已持有锁，不能调用会再次加锁的函数。
	r.StopReason = StopReason{
		Action:   stopAction,
		Reason:   stopReasonCompleted,
		Source:   stopSourceSystem,
		Attempts: r.Round,
	}.Text()
	r.finishLocked(usage)
}

// markFailed 以失败终态定稿请求, 最终错误取自本次失败原因。
//
// reason/source 是**终止原因**（T-trace-003），由调用出口传入：同一个 markFailed
// 会被多个出口调用（预算用尽/全体拒绝/快速失败/无可用成员……），只有出口自己
// 知道是哪一个。空 reason 记为 unrecorded，让"忘记标注"变成看得见的信号。
//
// **必须在 finishLocked 之前写**：finishLocked 内部就要把快照落库，
// 之后 再写只能改内存、追不回已经写进去的那一行 —— 生产复验时失败请求的
// stop_reason 就是这样落成空值的（v0.61.0 时序 bug：落库发生在写原因之前）。
// markSucceeded / markCanceled 从一开始就是这个顺序，三者现在同构。
func (r *RequestState) markFailed(err error, responseBody string, usage *llm.Usage, reason, source string) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusFailed
	r.Error = err.Error()
	// 归因分类只在这里做：markFailed 拿得到 error 对象，也就拿得到上游状态码；
	// 等到落库时只剩这段文本，再想分类就只能靠猜（上游措辞千变万化）。
	r.FaultKind = faultKindOf(err)
	if responseBody != "" {
		r.responseBody = responseBody
	}
	r.recordStopReasonLocked(reason, source)
	r.finishLocked(usage)
}

// recordStopReasonLocked 记录本次请求**为什么停下来**（T-trace-003）；调用方必须持有锁。
//
// 与 markFailed 分开而不是合并进它：markFailed 在多个出口都被调用（含流式中断、
// 客户端取消这些"不是任何人的错"的情形），而终止原因是**出口自己才知道**的信息 ——
// 同一个 markFailed 从五个不同出口进来，只有出口知道自己是"预算用尽"还是"全体拒绝"。
//
// 只覆盖不追加：一个请求只会终止一次，先写的那个出口才是真实原因。
// （若出现重复调用，保留首次比保留最后一次更接近事实。）
//
// 空 reason 记为 unrecorded 而不是留空：留空会与"字段上线之前的历史行"混在一起，
// 而这个版本之后仍然为空只可能是某个出口忘了标 —— 那是需要被看见的实现缺陷。
func (r *RequestState) recordStopReasonLocked(reason, source string) {
	if r.StopReason != "" {
		return
	}
	if reason == "" {
		reason = stopReasonUnrecorded
	}
	if source == "" {
		source = stopSourceSystem
	}
	r.StopReason = StopReason{
		Action:   stopAction,
		Reason:   reason,
		Source:   source,
		Attempts: r.Round,
	}.Text()
}

// faultKindOf 把一次上游失败归到三类之一，供通过率统计排除「不是渠道的锅」的那种。
//
// 三类的语义见 model.RelayLog.FaultKind：
//
//	request   请求本身非法 —— 任何成员都会同样拒绝，不该算进渠道通过率
//	member    成员自身问题 —— 算渠道故障
//	transient 可恢复       —— 算渠道故障
//
// 取不到状态码（超时/网络/取消）按 transient 处理，与既有的失败处置口径一致
// （retry.go 的 classifyUpstreamFailure 也是这么判的）——**复用同一套判断**，
// 不另写一份：两处口径一旦分叉，统计与重试行为就会互相矛盾。
func faultKindOf(err error) string {
	switch classifyUpstreamFailure(err) {
	case dispositionRequestFault:
		return "request"
	case dispositionMemberFault:
		return "member"
	default:
		return "transient"
	}
}

// markCanceled 以取消终态定稿请求, 用于客户端提前断开或主动取消。
func (r *RequestState) markCanceled(err error, responseBody string, usage *llm.Usage) {
	mu.Lock()
	defer mu.Unlock()

	r.Status = StatusCanceled
	r.Error = err.Error()
	if responseBody != "" {
		r.responseBody = responseBody
	}
	// 取消路径的终止原因固定为"客户端取消"（T-trace-003），source 记 client。
	// 这条最容易被误当成渠道故障：取消不是任何一方的问题，
	// 界面据此可以明确告诉用户"这一条不用管"。
	r.StopReason = StopReason{
		Action:   stopAction,
		Reason:   stopReasonClientCancel,
		Source:   stopSourceClient,
		Attempts: r.Round,
	}.Text()
	r.finishLocked(usage)
}

// finishLocked 写入用量和费用, 发布终态, 更新请求级统计, 落库历史快照并裁剪内存历史; 调用方必须持有锁。
func (r *RequestState) finishLocked(usage *llm.Usage) {
	r.Sending = false
	r.cancel = nil
	if usage != nil {
		r.Usage = *usage
	}
	if r.usageRecorder != nil && usage != nil {
		r.usageRecorder(usage.PromptTokens, usage.CompletionTokens)
	}
	if usage != nil {
		// 成员近期负载（T-route-007 lowest_tpm_rpm 的数据源）：只在请求终态记一次，
		// 归属到最近一次服务它的成员行（分轮重试时拿不到中间轮的 usage，口径见 metrics.go）。
		recordMemberTokens(r.TargetItemID, usage.PromptTokens+usage.CompletionTokens)
	}
	metrics := usageMetrics(r.TargetModel, usage)
	r.Cost = metrics.InputCost + metrics.OutputCost
	r.Duration = time.Since(r.StartedAt)
	metrics.WaitTime = r.Duration.Milliseconds()
	if r.Status == StatusSuccess {
		metrics.RequestSuccess = 1
	} else {
		metrics.RequestFailed = 1
	}
	_ = op.StatsTotalUpdate(metrics)
	_ = op.StatsHourlyUpdate(metrics)
	_ = op.StatsDailyUpdate(context.Background(), metrics)
	// 分模型×小时用量明细 (NM-CUR-025): 渠道维度取本轮实际目标, 未选出目标即结束的请求无归属不记。
	op.LogUsageHourly(r.TargetModel, r.TargetChannel, metrics)
	if r.apiKeyID > 0 {
		_ = op.StatsAPIKeyUpdate(r.apiKeyID, metrics)
	}
	// 终态落库历史快照: 首字节毫秒未提交记 -1, 供日志页历史筛选与卡片"首字时间"回退。
	firstByteMs := int64(-1)
	if !r.FirstByteAt.IsZero() {
		firstByteMs = r.FirstByteAt.Sub(r.StartedAt).Milliseconds()
	}
	cachedTokens := int64(0)
	if r.Usage.PromptTokensDetails != nil {
		cachedTokens = r.Usage.PromptTokensDetails.CachedTokens
	}
	// 思考 token 只采信上游在 usage 里的回报（确定性值）。0 即"上游没报"，
	// 不做估算：用"输出长度减可见文本"之类的反推会把非推理模型的正常输出
	// 也算成思考，凭空造出一批看起来很像真的假数据。
	reasoningTokens := int64(0)
	if r.Usage.CompletionTokensDetails != nil {
		reasoningTokens = r.Usage.CompletionTokensDetails.ReasoningTokens
	}
	// 归档最后一轮（T-trace-001）: 中间轮在各自的 startRound 里已归档, 这里补上当前轮,
	// 使 AttemptChain 覆盖 1..Round 的全部轮次。从未打过上游时（Round==0）内部会直接跳过。
	r.archiveRoundLocked()
	op.RelayLogSave(model.RelayLog{
		RequestID:      r.ID,
		Status:         string(r.Status),
		Model:          r.Model,
		GroupID:        r.GroupID,
		APIKeyName:     r.APIKeyName,
		TargetChannel:  r.TargetChannel,
		TargetModel:    r.TargetModel,
		TargetProtocol: int(r.TargetProtocol),
		// 客户端入站协议（T-trace-004）：与 TargetProtocol 并排落库，"转换过没有"才答得上来。
		// 它是请求一进来就定下的（由入站格式推出），不随哪一轮选择而变化。
		RequestProtocol: int(r.Protocol),
		ReportedModel:   r.ReportedModel,
		ModelMismatch:   r.ModelMismatch,
		StartedAt:       r.StartedAt,
		FirstByteMs:     firstByteMs,
		DurationMs:      r.Duration.Milliseconds(),
		Attempts:        r.Round,
		Decision:        r.Decision,
		PromptTokens:    r.Usage.PromptTokens,
		CachedTokens:    cachedTokens,
		CompletionToks:  r.Usage.CompletionTokens,
		// 思考强度与思考 token（T-insight-001）：前者是请求侧参数、后者是上游回报，
		// 两者一起说明"这条请求为什么慢/贵"，因此与 token 数落在一起。
		ReasoningEffort: r.ReasoningEffort,
		ReasoningTokens: reasoningTokens,
		Cost:            r.Cost,
		Error:           r.Error,
		FaultKind:       r.FaultKind,
		StopReason:      r.StopReason,
		// 尝试链: 回答"中途换过谁、各自为何失败"。单轮成功时长度为 1（只有它自己），
		// 与 Attempts 一致; 有重试时是完整链路。
		AttemptDetail:     r.AttemptChain,
		AttemptsTruncated: r.AttemptsTruncated,
	})
	publishRequestLocked(r)

	finished := 0
	oldest := uint64(0)
	for id, request := range requests {
		if request.Status == StatusRunning || request.Status == StatusCommitted {
			continue
		}
		finished++
		if oldest == 0 || id < oldest {
			oldest = id
		}
	}
	if finished > maxFinished {
		delete(requests, oldest)
	}
}

// usageMetrics 将统一用量按模型单价转换为 Token 与费用统计; 无用量或价格时对应费用为零。
func usageMetrics(modelName string, usage *llm.Usage) model.StatsMetrics {
	if usage == nil {
		return model.StatsMetrics{}
	}
	metrics := model.StatsMetrics{InputToken: usage.PromptTokens, OutputToken: usage.CompletionTokens}
	price, err := op.LLMGet(modelName)
	if err != nil {
		return metrics
	}
	cachedTokens, writeCachedTokens := int64(0), int64(0)
	if usage.PromptTokensDetails != nil {
		cachedTokens = usage.PromptTokensDetails.CachedTokens
		writeCachedTokens = usage.PromptTokensDetails.WriteCachedTokens
	}
	inputTokens := max(int64(0), usage.PromptTokens-cachedTokens-writeCachedTokens)
	metrics.InputCost = (float64(inputTokens)*price.Input + float64(cachedTokens)*price.CacheRead + float64(writeCachedTokens)*price.CacheWrite) / 1_000_000
	metrics.OutputCost = float64(usage.CompletionTokens) * price.Output / 1_000_000
	return metrics
}

// publishRequestLocked 非阻塞发布最新请求状态, 连接拥塞时关闭它并交给客户端重连获取全量快照; 调用方必须持有锁。
func publishRequestLocked(request *RequestState) {
	for stream := range watchers {
		select {
		case stream <- *request:
		default:
			delete(watchers, stream)
			close(stream)
		}
	}
}

// OpenRequestStream 注册请求状态流连接, 返回按请求 ID 倒序的全部快照和后续增量通道。
// 日志页不提供排序开关, 而 requests 是 map, 遍历顺序随机, 故顺序须由此处定稿。
func OpenRequestStream() ([]RequestState, chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	stream := make(chan RequestState, streamBuffer)
	watchers[stream] = struct{}{}

	snapshot := make([]RequestState, 0, len(requests))
	for _, request := range requests {
		snapshot = append(snapshot, *request)
	}
	sort.Slice(snapshot, func(i, j int) bool { return snapshot[i].ID > snapshot[j].ID })
	return snapshot, stream
}

// CloseRequestStream 注销并关闭指定请求状态流连接。
func CloseRequestStream(stream chan RequestState) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := watchers[stream]; exists {
		delete(watchers, stream)
		close(stream)
	}
}

// RequestBody 返回指定请求保存的原始请求体, 记录不存在时返回空串。
func RequestBody(id uint64) string {
	mu.Lock()
	defer mu.Unlock()

	if request := requests[id]; request != nil {
		return request.body
	}
	return ""
}

// ResponseBody 返回指定请求当前保存的响应体, 记录不存在或响应未完成时返回空串。
func ResponseBody(id uint64) string {
	mu.Lock()
	defer mu.Unlock()

	if request := requests[id]; request != nil {
		return request.responseBody
	}
	return ""
}

// Clear 删除全部已结束的请求记录。
func Clear() {
	mu.Lock()
	defer mu.Unlock()

	for id, request := range requests {
		if request.Status != StatusRunning && request.Status != StatusCommitted {
			delete(requests, id)
		}
	}
}
