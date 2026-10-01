package op

import (
	"sync"
	"time"
)

// Key 级限流 (litellm G5 对标, 单进程内存滑窗):
// RPM 在请求进入前判额 (拒绝 429), TPM 在请求结束后记账 (超出后窗口内新请求被拒),
// 两者共用每 key 每维度一个时间戳/用量环形窗口, 过期切片惰性淘汰, 无后台协程。

type rateWindow struct {
	stamps []int64 // 事件时间戳 (UnixMilli), RPM 记请求, TPM 记词元量。
	used   []int64 // 与 stamps 对齐的本次事件量 (RPM 恒为 1)。
}

var (
	rateLimitMu      sync.Mutex
	rateLimitRPM     = make(map[int]*rateWindow) // keyID → 请求滑窗。
	rateLimitTPM     = make(map[int]*rateWindow) // keyID → 词元滑窗。
	rateLimitStopped = make(map[int]time.Time)   // keyID → TPM 超限后的禁行截止时刻。
)

const rateWindowSpan = time.Minute

// allowRate 检查 key 在 limit 下能否再发起一次请求; limit<=0 恒放行。
func allowRate(store map[int]*rateWindow, keyID, limit int) bool {
	if limit <= 0 {
		return true
	}
	nowMilli := time.Now().UnixMilli()
	spanMilli := rateWindowSpan.Milliseconds()
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()
	w, ok := store[keyID]
	if !ok {
		w = &rateWindow{}
		store[keyID] = w
	}
	// 惰性淘汰窗口外切片。
	total, n := int64(0), 0
	for i := range w.stamps {
		if nowMilli-w.stamps[i] < spanMilli {
			w.stamps[n] = w.stamps[i]
			w.used[n] = w.used[i]
			total += w.used[n]
			n++
		}
	}
	if total+1 <= int64(limit) {
		w.stamps = append(w.stamps[:n], nowMilli)
		w.used = append(w.used[:n], 1)
		return true
	}
	w.stamps = w.stamps[:n]
	w.used = w.used[:n]
	return false
}

// AllowKeyRPM 判定 key 本分钟还能否发起新请求 (RPM 预检查维度)。
func AllowKeyRPM(keyID, limit int) bool {
	return allowRate(rateLimitRPM, keyID, limit)
}

// allowTPMAt 记录一次词元用量并返回当前窗口总量, 供下一次请求判额。
func allowTPMAt(keyID, tokens int) int64 {
	if tokens <= 0 {
		tokens = 1 // 零 token 请求也占一个计数位, 防零成本穿透。
	}
	nowMilli := time.Now().UnixMilli()
	spanMilli := rateWindowSpan.Milliseconds()
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()
	w, ok := rateLimitTPM[keyID]
	if !ok {
		w = &rateWindow{}
		rateLimitTPM[keyID] = w
	}
	total, n := int64(0), 0
	for i := range w.stamps {
		if nowMilli-w.stamps[i] < spanMilli {
			w.stamps[n] = w.stamps[i]
			w.used[n] = w.used[i]
			total += w.used[n]
			n++
		}
	}
	w.stamps = append(w.stamps[:n], nowMilli)
	w.used = append(w.used[:n], int64(tokens))
	return total + int64(tokens)
}

// RecordKeyUsage 在请求结束后记账词元用量 (TPM 后记账维度)。
func RecordKeyUsage(keyID int, tokens int64) {
	if keyID <= 0 {
		return
	}
	allowTPMAt(keyID, int(tokens))
}

// AllowKeyTPM 判定 key 在 TPM 限额下能否发起新请求: 近一分钟记账总量须低于限额。
func AllowKeyTPM(keyID, limit int) bool {
	if limit <= 0 {
		return true
	}
	nowMilli := time.Now().UnixMilli()
	spanMilli := rateWindowSpan.Milliseconds()
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()
	w, ok := rateLimitTPM[keyID]
	if !ok {
		return true
	}
	total, n := int64(0), 0
	for i := range w.stamps {
		if nowMilli-w.stamps[i] < spanMilli {
			w.stamps[n] = w.stamps[i]
			w.used[n] = w.used[i]
			total += w.used[n]
			n++
		}
	}
	w.stamps = w.stamps[:n]
	w.used = w.used[:n]
	return total < int64(limit)
}

// ResetRateLimitsForTest 清空全部限流窗口, 仅测试接线用。
func ResetRateLimitsForTest() {
	rateLimitMu.Lock()
	defer rateLimitMu.Unlock()
	rateLimitRPM = make(map[int]*rateWindow)
	rateLimitTPM = make(map[int]*rateWindow)
}
