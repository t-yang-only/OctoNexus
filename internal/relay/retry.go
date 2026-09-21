package relay

import (
	"errors"
	"strings"
	"sync/atomic"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"
)

// upstreamStatusError 承载上游以非 2xx 结束时的状态码。
// 错误文本与改造前完全一致（"upstream responded 400 Bad Request: …"），
// 只额外携带状态码，供重试决策区分「确定性错误」与「可恢复错误」。
//
// retryHint（T-allocate-002）是上游在响应头里给出的"多久之后再来"（Retry-After 一类）。
// 流式路径自己读原始响应，故构造错误时顺手把提示带上；非流式路径的提示由库的 httpclient.Error
// 提供（见 throttle.go 的 parseRetryHint）。0 表示上游没说，按分组配置的冷却处理。
type upstreamStatusError struct {
	status    int
	message   string
	retryHint time.Duration
}

func (e *upstreamStatusError) Error() string { return e.message }

// newUpstreamStatusError 构造带状态码的上游错误。
func newUpstreamStatusError(status int, message string) error {
	return &upstreamStatusError{status: status, message: message}
}

// upstreamStatusOf 从错误链里取出上游 HTTP 状态码。
// 非流式路径由 httpclient.Error 带出状态码（已用 %w 包裹），流式路径由 upstreamStatusError 带出。
// 取不到状态码（网络错误、超时、上下文取消）时返回 false。
func upstreamStatusOf(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	var statusErr *upstreamStatusError
	if errors.As(err, &statusErr) && statusErr.status > 0 {
		return statusErr.status, true
	}
	var apiErr *httpclient.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode > 0 {
		return apiErr.StatusCode, true
	}
	return 0, false
}

// failureDisposition 是一次上游失败该如何处置的分类。
type failureDisposition int

const (
	// dispositionRetry 可恢复失败: 超时、网络错误、408/409/429/5xx。
	// 保持既有语义 —— 消耗该成员的尝试次数, 达上限后进入冷却并故障转移到下一个成员。
	dispositionRetry failureDisposition = iota
	// dispositionMemberFault 成员自身问题: 401/403/404/402/407 等。
	// 换渠道才有可能成功, 因此不再消耗剩余尝试次数, 直接按「尝试次数用尽」处理（记失败并立即冷却）。
	dispositionMemberFault
	// dispositionRequestFault 请求本身非法: 400/405/406/413/414/415/422/501。
	// 任何成员都会同样拒绝, 既不该记成员失败（那不是成员的锅），也不该反复重试。
	dispositionRequestFault
)

// requestFaultStatuses 是「换成员也不会成功」的确定性错误:
// 请求体不合法、方法/媒体类型/长度不受支持、语义校验失败。把它们重试一万次也是同样的结果。
var requestFaultStatuses = map[int]bool{
	400: true, // 请求参数错误（模型不支持当前参数、messages 结构不合法等）
	405: true, // 方法不允许
	406: true, // 不接受的内容类型
	413: true, // 请求体过大
	414: true, // URI 过长
	415: true, // 媒体类型不支持
	422: true, // 语义校验失败
	501: true, // 上游未实现该能力
}

// memberFaultStatuses 是「成员自身的问题」: 凭据无效/无权限/模型不存在/欠费。
// 重试当前成员没有意义, 但换一个成员可能成功, 因此记失败后立即换人。
var memberFaultStatuses = map[int]bool{
	401: true, // 凭据无效
	402: true, // 需要付费（余额不足）
	403: true, // 无权限 / 被风控
	404: true, // 模型或路径不存在
	407: true, // 需要代理认证
}

// classifyUpstreamFailure 判定一次上游失败的处置方式。
// 取不到状态码（超时/网络/取消）按可恢复处理, 与既有行为一致。
func classifyUpstreamFailure(err error) failureDisposition {
	status, ok := upstreamStatusOf(err)
	if !ok {
		return dispositionRetry
	}
	switch {
	case requestFaultStatuses[status]:
		return dispositionRequestFault
	case memberFaultStatuses[status]:
		return dispositionMemberFault
	default:
		// 408/409/429/5xx 以及未列举的状态码按可恢复处理（保守: 保持既有重试语义）。
		return dispositionRetry
	}
}

// attemptCap 是一个请求内允许发起的上游尝试总次数上限。
//
// 为什么需要上限（上游 issue #388「手动模式下的无限重试」/ #338「客户端超时后后台持续空转」）:
// 手动模式永远不会给成员打冷却（recordRouteFailure 直接返回 false），
// 故障转移模式在成员全部处于冷却时也只能按重试间隔反复选同一个成员 ——
// 两者都会在成员持续不可用时**无限重试**，每秒一次地打上游（线上被中转站判定为攻击并封 IP），
// 而客户端早已超时离开。有了上限, 请求会以一个明确的失败响应结束。
//
// 取值: 成员数 × 单成员尝试次数 × 2（至少两轮完整扫描）, 下限 6、上限 64 ——
// 既不影响正常的故障转移（多成员场景拿到的上限远大于一轮扫描）, 又能兜住无限重试。
func attemptCap(memberCount, memberMaxAttempts int) int {
	if memberCount < 1 {
		memberCount = 1
	}
	if memberMaxAttempts < 1 {
		memberMaxAttempts = 1
	}
	cap := memberCount * memberMaxAttempts * 2
	if cap < minAttemptCap {
		cap = minAttemptCap
	}
	if cap > maxAttemptCap {
		cap = maxAttemptCap
	}
	return cap
}

const (
	// minAttemptCap 单成员分组（含手动模式）也能拿到的最小尝试次数。
	minAttemptCap = 6
	// maxAttemptCap 单请求尝试次数的硬上限, 防止成员极多时长时间占用客户端。
	maxAttemptCap = 64
	// maxRetryBackoffSeconds 退避上限: 再久也不超过这个间隔, 避免恢复后迟迟不重试。
	maxRetryBackoffSeconds = 30
)

// retryBackoff 返回同一个成员第 failures 次连续失败后的等待秒数。
// 退避从配置的重试间隔开始逐次翻倍（1×2^(n-1)），封顶 maxRetryBackoffSeconds。
// 配置为 0 或负数时按 1 秒处理: 重试总得有个间隔, 否则就是刷上游。
func retryBackoff(retryIntervalSeconds, failures int) int {
	interval := retryIntervalSeconds
	if interval < 1 {
		interval = 1
	}
	if failures < 1 {
		failures = 1
	}
	if failures > 6 { // 1,2,4,8,16,32 已超过上限, 更大的失败次数不再移位
		failures = 6
	}
	delay := interval << (failures - 1)
	if delay > maxRetryBackoffSeconds {
		delay = maxRetryBackoffSeconds
	}
	return delay
}

// upstreamFailureHint 给日志与客户端错误附加一句人话, 说明这次失败为什么被这样处置。
func upstreamFailureHint(disposition failureDisposition) string {
	switch disposition {
	case dispositionMemberFault:
		return "channel credential or model rejected by upstream, switching member"
	case dispositionRequestFault:
		return "upstream rejected the request itself, failing fast instead of retrying"
	default:
		return ""
	}
}

// requestFaultMessage 组装「请求本身被上游拒绝」时回给客户端的错误文本。
// 保留上游原文（客户端最需要看到的就是上游为什么拒绝), 并标明这是确定性错误、不会再重试。
func requestFaultMessage(err error) string {
	message := ""
	if err != nil {
		message = strings.TrimSpace(err.Error())
	}
	if message == "" {
		message = "upstream rejected the request"
	}
	return "upstream rejected the request: " + message
}

// 「上游判定请求本身非法」（400 一类）时的两种取向（T-retry-003，用户要求两种都要）。
// 两种都保留上游原文，区别只在「要不要先用别的成员试一次」：
//   - RequestFaultActionFailover（默认，与既往行为一致）: 记下这个成员拒绝过，换下一个成员再试；
//     全部成员都拒绝同一份请求时才以错误结束。好处是「某个成员不支持该参数、另一个支持」时
//     请求仍然成功 —— 多中转混用时很常见（同一模型在不同站点的参数支持度并不一致）。
//   - RequestFaultActionFailFast: 第一个成员拒绝就立刻把上游的状态码与原文回给客户端，
//     不再换成员。好处是错误反馈最快、最贴近上游原话，代价是放弃「换一家也许能成」。
const (
	RequestFaultActionFailover = "failover"
	RequestFaultActionFailFast = "failfast"
)

// requestFaultAction 是运行时的取向选择（包内原子量）。
// 由装配层（server 启动 / 设置变更）经 SetRequestFaultAction 注入，relay 自身不耦合配置源。
// 未注入时取默认 failover: 与改造前的行为完全一致。
var requestFaultAction atomic.Value

// SetRequestFaultAction 设置取向; 非法取值一律回落默认 failover（不吞成"未知状态"）。
func SetRequestFaultAction(action string) {
	if action != RequestFaultActionFailFast {
		action = RequestFaultActionFailover
	}
	requestFaultAction.Store(action)
}

// RequestFaultAction 返回当前取向，未设置时为默认 failover。
func RequestFaultAction() string {
	if value, ok := requestFaultAction.Load().(string); ok && value != "" {
		return value
	}
	return RequestFaultActionFailover
}
