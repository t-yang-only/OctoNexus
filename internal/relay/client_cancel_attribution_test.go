package relay

// 客户端取消不得被记成成员故障（本轮实测的缺陷）。
//
// ## 现场
//
// 实时尝试链面板上出现：
//
//	apikey  共尝试 336 次  失败 6  可恢复 6
//	Post "https://api.apikey.fan/v1/chat/completions": context canceled
//
// 「可恢复」是 transient 故障档 —— 但那 6 次是**用户自己中断的请求**，
// 不是渠道的毛病。用户会据此去查一个没坏的渠道。
//
// ## 根因
//
// `finishRound(err, aborted)` 的 `aborted` 参数原本写成
// `ctx.Err() == nil && context.Cause(roundCtx) == context.Canceled`。
// 那个 `ctx.Err() == nil` 本意是"排除客户端取消"（它下面有单独分支处理），
// 但 finishRound 是在那个分支**之前**调用的 —— 客户端取消时算出 aborted=false，
// 于是 `roundFaultKind = faultKindOf("context canceled")`。
// 该错误没有上游状态码，`faultKindOf` 按 transient 兜底 —— 归因就这样错了。
//
// ## 修法与判据取向
//
// 归档口径改为「这一轮是不是上游失败」，并抽成 `roundNotUpstreamFailure`，
// 让这个决定可以被直接钉住 —— 内联表达式没法单测，这正是它当初没被守住的原因。

import (
	"context"
	"errors"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// 客户端取消（父 ctx 结束）→ 判定为"不是上游失败"。
//
// 这是本组用例的核心：修之前这条会算出 false。
func TestRoundNotUpstreamOnClientCancel(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	// cancel 必须被调用，否则父 ctx 结束前这个子 ctx 一直挂着（go vet 抓的正是这条：
	// "the cancel function returned by context.WithCancelCause should be called, not discarded"）。
	// 本用例的断言在 defer 之前执行，所以这里补的 cancel 不影响它要验的语义
	// —— 它验的是「父 ctx 结束会向上传播」，而 defer 发生在断言之后。
	roundCtx, cancelRoundCause := context.WithCancelCause(parent)
	defer cancelRoundCause(nil)

	// 模拟客户端断开：父 ctx 结束，roundCtx 被向上传播地取消。
	cancelParent()

	if !roundNotUpstreamFailure(roundCtx) {
		t.Error("客户端取消应判为「不是上游失败」—— 否则会被记成渠道的可恢复故障")
	}
}

// 人工中止（本轮被本地取消）→ 同样判为"不是上游失败"。
func TestRoundNotUpstreamOnManualAbort(t *testing.T) {
	roundCtx, cancelRoundCause := context.WithCancelCause(context.Background())
	cancelRoundCause(context.Canceled)

	if !roundNotUpstreamFailure(roundCtx) {
		t.Error("人工中止应判为「不是上游失败」")
	}
}

// 服务端自己的超时 → **是**上游失败，必须仍被计入。
//
// 这条防的是"为了排除客户端取消而把超时也一起放过"—— 那会把真实的
// upstream timeout 从面板上抹掉，比原缺陷更糟。
func TestRoundIsUpstreamFailureOnServerTimeout(t *testing.T) {
	timeoutErr := errors.New("upstream non-stream response timeout")
	roundCtx, cancelRoundCause := context.WithCancelCause(context.Background())
	cancelRoundCause(timeoutErr)

	if roundNotUpstreamFailure(roundCtx) {
		t.Error("服务端超时必须仍判为上游失败 —— 它是真实的成员故障")
	}
}

// 流式空闲超时同样是真实失败。
func TestRoundIsUpstreamFailureOnStreamIdleTimeout(t *testing.T) {
	roundCtx, cancelRoundCause := context.WithCancelCause(context.Background())
	cancelRoundCause(errStreamIdleTimeout)

	if roundNotUpstreamFailure(roundCtx) {
		t.Error("流式空闲超时必须仍判为上游失败")
	}
}

// 未取消的一轮不算"非上游失败"。
func TestRoundNotUpstreamFalseWhenLive(t *testing.T) {
	roundCtx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)

	if roundNotUpstreamFailure(roundCtx) {
		t.Error("未取消的一轮不该被判为「不是上游失败」")
	}
}

// finishRound 的归档语义：aborted=true 时清空错误与归因，false 时记录。
//
// 这条钉住的是下游那一半 —— 判据只覆盖判定函数的话，
// finishRound 传错参数仍然会漏。
func TestFinishRoundClearsAttributionWhenNotUpstream(t *testing.T) {
	cancelErr := context.Canceled

	// aborted=true（客户端取消/人工中止）：不得留下失败归因。
	abortedState := &RequestState{ID: 9101}
	abortedState.startRound(nil, 11, "channelA", "model-a", model.ProtocolOpenAIChatCompletion)
	abortedState.finishRound(cancelErr, true)
	if abortedState.roundFaultKind != "" {
		t.Errorf("非上游失败的一轮不该留下归因，实得 %q", abortedState.roundFaultKind)
	}
	if abortedState.roundErrText != "" {
		t.Errorf("非上游失败的一轮不该留下错误原文，实得 %q", abortedState.roundErrText)
	}

	// aborted=false（真实失败）：必须留下归因。
	failedState := &RequestState{ID: 9102}
	failedState.startRound(nil, 11, "channelA", "model-a", model.ProtocolOpenAIChatCompletion)
	failedState.finishRound(errors.New("upstream boom"), false)
	if failedState.roundFaultKind == "" {
		t.Error("真实失败必须留下归因")
	}
}
