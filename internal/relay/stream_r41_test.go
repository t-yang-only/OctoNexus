package relay

import (
	"os"
	"strings"
	"testing"
)

// R41-F1: 验证 stopReasonStreamIdle 的文本构造格式
func TestStopReasonStreamIdleText(t *testing.T) {
	text := stopReasonText(stopReasonStreamIdle, stopSourceConfig)
	for _, want := range []string{"action=stop", "reason=stream_idle_timeout", "source=config"} {
		if !strings.Contains(text, want) {
			t.Errorf("stopReasonText(stream_idle) = %q, want containing %q", text, want)
		}
	}
}

// R41-F2: 验证 markFailed 接收确切的 streamErrReason 与 streamErrSource 时不再落成 unrecorded
func TestStreamFailureRecordsPreciseStopReason(t *testing.T) {
	openStopReasonTestDB(t)

	// 1. 空闲看门狗超时
	stateIdle := &RequestState{ID: 9501}
	stateIdle.markFailed(errStreamIdleTimeout, "", nil, stopReasonStreamIdle, stopSourceConfig)
	if !strings.Contains(stateIdle.StopReason, stopReasonStreamIdle) {
		t.Fatalf("stateIdle StopReason 应包含 %q, 实际: %q", stopReasonStreamIdle, stateIdle.StopReason)
	}
	if strings.Contains(stateIdle.StopReason, stopReasonUnrecorded) {
		t.Fatalf("stateIdle StopReason 不应包含 %q", stopReasonUnrecorded)
	}

	// 2. 流式中途截断/提交守卫
	stateCommit := &RequestState{ID: 9502}
	stateCommit.markFailed(errTestCanceled{}, "", nil, stopReasonCommitGuard, stopSourceUpstream)
	if !strings.Contains(stateCommit.StopReason, stopReasonCommitGuard) {
		t.Fatalf("stateCommit StopReason 应包含 %q, 实际: %q", stopReasonCommitGuard, stateCommit.StopReason)
	}
	if strings.Contains(stateCommit.StopReason, stopReasonUnrecorded) {
		t.Fatalf("stateCommit StopReason 不应包含 %q", stopReasonUnrecorded)
	}
}

// R41-F3: 源码守卫测试：断言 handler.go 中流式收尾的 markFailed 不再传入空 reason/source
func TestStreamHandlerPassesNonEmptyStopReason(t *testing.T) {
	bytes, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	content := string(bytes)
	if strings.Contains(content, `request.markFailed(err, string(responseBody), result.usage, "", "")`) {
		t.Fatalf("handler.go 仍存在传空 reason 的 markFailed 调用，导致落入 unrecorded 盲区")
	}
	if !strings.Contains(content, `X-Accel-Buffering`) || !strings.Contains(content, `Cache-Control`) {
		t.Fatalf("handler.go 缺少流式防缓冲关键响应头 X-Accel-Buffering 或 Cache-Control")
	}
}
