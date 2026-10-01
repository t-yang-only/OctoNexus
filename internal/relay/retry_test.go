package relay

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm/httpclient"
)

func TestUpstreamStatusOfReadsBothErrorShapes(t *testing.T) {
	// 流式路径: 自己构造的带状态码错误（错误文本与改造前一致）。
	streamErr := newUpstreamStatusError(http.StatusUnauthorized, "upstream responded 401 Unauthorized: bad key")
	if status, ok := upstreamStatusOf(streamErr); !ok || status != http.StatusUnauthorized {
		t.Fatalf("流式错误取状态码失败: status=%d ok=%v", status, ok)
	}
	if streamErr.Error() != "upstream responded 401 Unauthorized: bad key" {
		t.Fatalf("错误文本被改变, 会影响日志与既有断言: %q", streamErr.Error())
	}

	// 非流式路径: 库返回的 httpclient.Error, 且被 %w 包裹（既有实现就是包裹后再拼正文）。
	apiErr := &httpclient.Error{Method: "POST", URL: "http://upstream/v1/chat/completions",
		StatusCode: http.StatusTooManyRequests, Status: "429 Too Many Requests", Body: []byte("slow down")}
	wrapped := fmt.Errorf("%w: %s", apiErr, apiErr.Body)
	if status, ok := upstreamStatusOf(wrapped); !ok || status != http.StatusTooManyRequests {
		t.Fatalf("包裹后的 httpclient.Error 取状态码失败: status=%d ok=%v", status, ok)
	}

	// 超时/网络/取消这类没有状态码的错误: 返回 false, 由调用方按可恢复处理。
	if status, ok := upstreamStatusOf(errors.New("context deadline exceeded")); ok || status != 0 {
		t.Fatalf("无状态码错误不该报出状态码: status=%d ok=%v", status, ok)
	}
	if _, ok := upstreamStatusOf(nil); ok {
		t.Fatal("nil 错误不该报出状态码")
	}
}

func TestClassifyUpstreamFailure(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   failureDisposition
	}{
		// 请求本身非法: 换成员也会被拒绝, 既不该重试也不该记成员失败。
		{"400 参数错误", http.StatusBadRequest, dispositionRequestFault},
		{"405 方法不允许", http.StatusMethodNotAllowed, dispositionRequestFault},
		{"413 请求体过大", http.StatusRequestEntityTooLarge, dispositionRequestFault},
		{"415 媒体类型", http.StatusUnsupportedMediaType, dispositionRequestFault},
		{"422 语义校验", http.StatusUnprocessableEntity, dispositionRequestFault},
		{"501 未实现", http.StatusNotImplemented, dispositionRequestFault},
		// 成员自身问题: 重试当前成员没有意义, 但换成员可能成功。
		{"401 凭据无效", http.StatusUnauthorized, dispositionMemberFault},
		{"402 需要付费", http.StatusPaymentRequired, dispositionMemberFault},
		{"403 无权限", http.StatusForbidden, dispositionMemberFault},
		{"404 模型不存在", http.StatusNotFound, dispositionMemberFault},
		// 可恢复: 保持既有重试语义。
		{"429 限流", http.StatusTooManyRequests, dispositionRetry},
		{"500 上游故障", http.StatusInternalServerError, dispositionRetry},
		{"502 网关错误", http.StatusBadGateway, dispositionRetry},
		{"503 不可用", http.StatusServiceUnavailable, dispositionRetry},
		{"504 网关超时", http.StatusGatewayTimeout, dispositionRetry},
	}
	for _, tc := range cases {
		got := classifyUpstreamFailure(newUpstreamStatusError(tc.status, "x"))
		if got != tc.want {
			t.Errorf("%s: 处置=%v, 期望=%v", tc.name, got, tc.want)
		}
	}
	// 没有状态码的错误（超时/网络/取消）保持既有行为: 可恢复。
	if got := classifyUpstreamFailure(errors.New("dial tcp: connection refused")); got != dispositionRetry {
		t.Errorf("无状态码错误应可恢复, 得到 %v", got)
	}
}

func TestAttemptCapIsBoundedAndGenerousEnough(t *testing.T) {
	// 单成员分组（含手动模式）也要有下限: 不能被 memberMaxAttempts 直接压到 1、2 次。
	if got := attemptCap(1, 1); got != minAttemptCap {
		t.Fatalf("单成员单次尝试应取下限 %d, 得到 %d", minAttemptCap, got)
	}
	if got := attemptCap(0, 0); got != minAttemptCap {
		t.Fatalf("空分组应取下限 %d, 得到 %d", minAttemptCap, got)
	}
	// 多成员: 至少两轮完整扫描（成员数 × 单成员尝试次数 × 2）。
	if got := attemptCap(5, 2); got != 20 {
		t.Fatalf("5 成员 × 2 次 × 2 轮应得 20, 得到 %d", got)
	}
	// 成员极多时封顶, 避免请求长时间占着客户端。
	if got := attemptCap(100, 5); got != maxAttemptCap {
		t.Fatalf("应封顶到 %d, 得到 %d", maxAttemptCap, got)
	}
}

func TestRetryBackoffDoublesAndCaps(t *testing.T) {
	cases := []struct {
		interval int
		failures int
		want     int
	}{
		{1, 1, 1}, // 第一次失败: 按配置间隔
		{1, 2, 2}, // 同一成员连续失败: 翻倍
		{1, 3, 4},
		{1, 4, 8},
		{1, 5, 16},
		{1, 6, maxRetryBackoffSeconds}, // 封顶
		{1, 9, maxRetryBackoffSeconds}, // 超过封顶不再增长
		{3, 1, 3},
		{3, 3, 12},
		{3, 5, maxRetryBackoffSeconds},
		{0, 1, 1},  // 配置为 0 也要有间隔, 否则就是刷上游
		{-5, 2, 2}, // 负数按 1 秒处理
	}
	for _, tc := range cases {
		if got := retryBackoff(tc.interval, tc.failures); got != tc.want {
			t.Errorf("retryBackoff(%d, %d)=%d, 期望 %d", tc.interval, tc.failures, got, tc.want)
		}
	}
}

func TestRequestFaultMessageKeepsUpstreamReason(t *testing.T) {
	message := requestFaultMessage(errors.New("upstream responded 400 Bad Request: {\"error\":\"bad tools\"}"))
	if message == "" {
		t.Fatal("错误文本不该为空")
	}
	for _, want := range []string{"upstream rejected the request", "upstream responded 400", "bad tools"} {
		if !strings.Contains(message, want) {
			t.Errorf("错误文本缺少 %q: %s", want, message)
		}
	}
	if got := requestFaultMessage(nil); !strings.Contains(got, "upstream rejected the request") {
		t.Errorf("nil 错误也要给出可读文本, 得到 %q", got)
	}
}
