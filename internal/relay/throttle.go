package relay

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/looplj/axonhub/llm/httpclient"
)

// 成员限流记忆（T-allocate-002）。
//
// 上游的 429 不是"这个成员坏了"，而是"这把钥匙在这个窗口里超限了，等一会再来"。
// 既有链路把 429 当可恢复失败：消耗尝试次数 → 达到上限后按分组配置冷却。缺的是两件事——
//  1. 上游在响应头里明确给了等待时长（Retry-After）时，我们应该照它等，而不是一律等配置的冷却秒数；
//  2. 这个"正在限流"的事实应该被记住，好让分压选路（mode=allocate）先把流量挪到别的钥匙上，
//     而不是等冷却到期后又一头撞上去。
//
// 因此这里只做两件事：记一份进程内的限流账（谁、到什么时候、被限过几次），以及把上游的
// Retry-After / X-RateLimit-Reset 解析成等待时长。冷却与重试语义仍归既有链路（route.go / retry.go），
// 本文件不改变任何既有判定，只在其上补"等待时长"与"给分配用的信号"。

// throttleRecord 是一个成员最近一次限流的事实。
type throttleRecord struct {
	untilMs int64 // 限流截止时刻（Unix 毫秒）；过期即视为已恢复。
	hits    int   // 进程内累计命中次数（重启清零），供监控展示"这把钥匙被限流过几次"。
	lastMs  int64 // 最近一次命中时刻（Unix 毫秒）。
}

var memberThrottles = struct {
	mu      sync.Mutex
	records map[int]throttleRecord
}{records: make(map[int]throttleRecord)}

// recordMemberThrottle 记一次限流命中：更新截止时刻（取较晚者）与命中次数。
// itemID 为展平后的成员行 ID（与冷却/亲和同一把键）；返回记完之后的记录。
func recordMemberThrottle(itemID int, nowMs, untilMs int64) throttleRecord {
	if itemID == 0 {
		return throttleRecord{}
	}
	if untilMs < nowMs {
		untilMs = nowMs
	}
	memberThrottles.mu.Lock()
	defer memberThrottles.mu.Unlock()

	record := memberThrottles.records[itemID]
	record.hits++
	record.lastMs = nowMs
	if untilMs > record.untilMs {
		record.untilMs = untilMs
	}
	memberThrottles.records[itemID] = record
	return record
}

// memberThrottleState 返回该成员当前是否处于限流等待中（未过期才算）。
func memberThrottleState(itemID int, nowMs int64) (throttleRecord, bool) {
	if itemID == 0 {
		return throttleRecord{}, false
	}
	memberThrottles.mu.Lock()
	defer memberThrottles.mu.Unlock()

	record, seen := memberThrottles.records[itemID]
	if !seen || record.untilMs <= nowMs {
		return throttleRecord{}, false
	}
	return record, true
}

// MemberThrottleRecord 是对外（监控/测试）读取一次限流账的形状：历史命中次数与截止时刻都在里面,
// 过期也照常返回（页面要展示"最近被限流过"), 是否仍在等待由调用方用 until_ms 判断。
type MemberThrottleRecord struct {
	ItemID  int   `json:"item_id"`
	Hits    int   `json:"hits"`
	UntilMs int64 `json:"until_ms"`
	LastMs  int64 `json:"last_ms"`
}

// MemberThrottleOf 返回该成员的限流账；从未被限流过时 ok=false。
func MemberThrottleOf(itemID int) (MemberThrottleRecord, bool) {
	memberThrottles.mu.Lock()
	defer memberThrottles.mu.Unlock()

	record, seen := memberThrottles.records[itemID]
	if !seen {
		return MemberThrottleRecord{}, false
	}
	return MemberThrottleRecord{ItemID: itemID, Hits: record.hits, UntilMs: record.untilMs, LastMs: record.lastMs}, true
}

// resetMemberThrottleForTest 清空限流账；仅测试用。
func resetMemberThrottleForTest() {
	memberThrottles.mu.Lock()
	memberThrottles.records = make(map[int]throttleRecord)
	memberThrottles.mu.Unlock()
}

// maxRetryAfterHint 是一次上游等待提示的上限：再久也不超过它，避免一个坏响应把成员冻住一小时。
const maxRetryAfterHint = 30 * time.Minute

// parseRetryAfterHeader 解析上游的"多久之后再来"响应头。
// 支持 RFC 7231 的两种写法：delta-seconds（"30"）与 HTTP-date（"Wed, 21 Oct 2015 07:28:00 GMT"）。
// 无法解析、非正数、或者时间点已过去时返回 ok=false —— 不猜一个时长出来。
func parseRetryAfterHeader(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0, false
		}
		hint := time.Duration(seconds) * time.Second
		if hint > maxRetryAfterHint {
			hint = maxRetryAfterHint
		}
		return hint, true
	}
	if at, err := http.ParseTime(value); err == nil {
		hint := at.Sub(now)
		if hint <= 0 {
			return 0, false
		}
		if hint > maxRetryAfterHint {
			hint = maxRetryAfterHint
		}
		return hint, true
	}
	return 0, false
}

// retryAfterFromHeader 从一组响应头里找等待提示，依次看 Retry-After、x-ratelimit-reset-after、
// x-ratelimit-reset（后者在不少中转站上是"距重置还有多少秒"，也有站点放的是时间戳，故两种都试）。
// 都取不到时返回 0（调用方按配置的冷却处理）。
func retryAfterFromHeader(header http.Header, now time.Time) time.Duration {
	if header == nil {
		return 0
	}
	if hint, ok := parseRetryAfterHeader(header.Get("Retry-After"), now); ok {
		return hint
	}
	// x-ratelimit-reset-after 的取值是"还有多少秒"，与 Retry-After 的 delta-seconds 同口径。
	if hint, ok := parseRetryAfterHeader(header.Get("X-RateLimit-Reset-After"), now); ok {
		return hint
	}
	// x-ratelimit-reset 有两种流派：Unix 秒（时间点）与"还有多少秒"。
	if raw := strings.TrimSpace(header.Get("X-RateLimit-Reset")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil {
			if seconds > 0 {
				nowSeconds := now.Unix()
				// 明显大于当前时间戳的按"Unix 秒时间点"解释，否则按"还剩多少秒"。
				if int64(seconds) > nowSeconds {
					return clampHint(time.Duration(int64(seconds)-nowSeconds) * time.Second)
				}
				return clampHint(time.Duration(seconds) * time.Second)
			}
		}
	}
	return 0
}

// clampHint 把等待时长夹到 (0, maxRetryAfterHint]。
func clampHint(hint time.Duration) time.Duration {
	if hint <= 0 {
		return 0
	}
	if hint > maxRetryAfterHint {
		return maxRetryAfterHint
	}
	return hint
}

// parseRetryHint 从一次上游失败里取出"上游希望我们等多久"。
//
// 两条来源，顺序固定：
//  1. 我们自己构造的 upstreamStatusError（流式路径：响应头在手上，构造错误时就把提示带上了）；
//  2. 库自带的 httpclient.Error（非流式路径：错误对象里带着响应头）。
//
// 拿不到提示时返回 0 —— 由调用方按分组配置的冷却处理，不在这里编一个时长。
func parseRetryHint(err error, now time.Time) time.Duration {
	if err == nil {
		return 0
	}
	var statusErr *upstreamStatusError
	if errors.As(err, &statusErr) {
		return clampHint(statusErr.retryHint)
	}
	// 非流式路径：httpclient.Error 里带 Headers，交给它自带的解析（它只认 429）。
	var apiErr *httpclient.Error
	if errors.As(err, &apiErr) {
		if httpclient.HasRetryAfterHeader(apiErr) {
			if hint, ok := httpclient.ParseRetryAfter(apiErr); ok {
				return clampHint(hint)
			}
		}
		return retryAfterFromHeader(apiErr.Headers, now)
	}
	return 0
}

// rateLimitHint 报告这次失败是否是上游的限流（429）以及希望等待的时长；非限流返回 0。
// 429 之外的 503/502 也可能带 Retry-After，但那些属于"上游故障"，沿用既有冷却语义即可，
// 不在这里扩大解释范围。
func rateLimitHint(err error, now time.Time) time.Duration {
	status, ok := upstreamStatusOf(err)
	if !ok || status != http.StatusTooManyRequests {
		return 0
	}
	return parseRetryHint(err, now)
}
