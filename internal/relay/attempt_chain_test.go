package relay

import (
	"errors"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// attempt_chain_test.go 覆盖尝试链（T-trace-001）: 记录"中途换过谁、各自为何失败"。
//
// 为什么值得单独一组用例: 既有结构里 Attempts 只是计数、TargetChannel 只是"最后用了谁",
// 中间轮的成员与失败原因**没有任何位置存放**。本组用例断言的核心不变量就是
// "这些中间事实必须能被取回", 以及"不该记的东西（人工中止）不许被记成渠道故障"。

// finishWithWait 走一轮: 起轮 → 短暂等待 → 收轮, 使 WaitMs 有可检出的正值。
func finishWithWait(state *RequestState, itemID int, channel, modelName string, err error, aborted bool) {
	state.startRound(nil, itemID, channel, modelName, model.ProtocolOpenAIChatCompletion)
	time.Sleep(2 * time.Millisecond)
	state.finishRound(err, aborted)
}

// 一条请求连续试了三个成员后成功: 尝试链必须留下前两轮的成员与失败原因,
// 且最后一轮（成功那轮）也要在链上——链的语义是"本次请求打过的全部轮次"。
func TestAttemptChainRecordsEveryRound(t *testing.T) {
	state := &RequestState{ID: 9001, Round: 0}

	// 第 1 轮: 成员 A, 上游 500（可恢复）。
	finishWithWait(state, 11, "channelA", "model-a", newUpstreamStatusError(500, "upstream boom"), false)
	// 第 2 轮: 成员 B, 凭据无效（成员自身问题）。
	finishWithWait(state, 22, "channelB", "model-b", newUpstreamStatusError(401, "invalid key"), false)
	// 第 3 轮: 成员 C, 成功。
	finishWithWait(state, 33, "channelC", "model-c", nil, false)

	// 终态落库前归档最后一轮（与 finishLocked 里的调用同源）。
	state.archiveRoundLocked()

	if state.Round != 3 {
		t.Fatalf("Round = %d, want 3", state.Round)
	}
	if len(state.AttemptChain) != 3 {
		t.Fatalf("尝试链长度 = %d, want 3（每一轮都必须在链上）: %+v", len(state.AttemptChain), state.AttemptChain)
	}

	wantChannel := []string{"channelA", "channelB", "channelC"}
	wantModel := []string{"model-a", "model-b", "model-c"}
	wantKind := []string{"transient", "member", ""}
	for i, got := range state.AttemptChain {
		if got.Round != i+1 {
			t.Errorf("链[%d].Round = %d, want %d", i, got.Round, i+1)
		}
		if got.Channel != wantChannel[i] || got.Model != wantModel[i] {
			t.Errorf("链[%d] = %s/%s, want %s/%s", i, got.Channel, got.Model, wantChannel[i], wantModel[i])
		}
		if got.FaultKind != wantKind[i] {
			t.Errorf("链[%d].FaultKind = %q, want %q", i, got.FaultKind, wantKind[i])
		}
		// WaitMs 必须真的被算出来（0 说明没写进去）。
		if got.WaitMs < 1 {
			t.Errorf("链[%d].WaitMs = %d, want >= 1", i, got.WaitMs)
		}
	}

	// 失败轮必须留下原因原文, 成功轮不该留错误。
	if state.AttemptChain[0].Error != "upstream boom" {
		t.Errorf("链[0].Error = %q, want %q", state.AttemptChain[0].Error, "upstream boom")
	}
	if state.AttemptChain[1].Error != "invalid key" {
		t.Errorf("链[1].Error = %q, want %q", state.AttemptChain[1].Error, "invalid key")
	}
	if state.AttemptChain[2].Error != "" || state.AttemptChain[2].FaultKind != "" {
		t.Errorf("成功轮不该带错误/归因: %+v", state.AttemptChain[2])
	}
}

// 反向对照: 人工中止的轮次**不许**被记成渠道故障。
//
// 没有这条用例, "把 aborted 当成普通失败" 这个变异不会被任何断言发现 ——
// 表现就是用户手动中断一次, 渠道记录里永久多出一条"这个成员坏了"。
func TestAttemptChainIgnoresAbortedRound(t *testing.T) {
	state := &RequestState{ID: 9002, Round: 0}

	finishWithWait(state, 44, "channelX", "model-x", errors.New("context canceled"), true)
	state.archiveRoundLocked()

	if len(state.AttemptChain) != 1 {
		t.Fatalf("尝试链长度 = %d, want 1", len(state.AttemptChain))
	}
	got := state.AttemptChain[0]
	if got.FaultKind != "" {
		t.Errorf("人工中止的轮次 FaultKind = %q, 必须是空串（不是渠道故障）", got.FaultKind)
	}
	if got.Error != "" {
		t.Errorf("人工中止的轮次 Error = %q, 必须为空（用户中断不是上游报错）", got.Error)
	}
	if got.Channel != "channelX" {
		t.Errorf("人工中止仍应记录轮次的成员, got %q", got.Channel)
	}
}

// 归因分类取状态码而不是错误文本: 同一个 400 在不同站点措辞完全不同,
// 若按文本判, 分类会随上游文案漂移。
func TestAttemptChainClassifiesByStatusCode(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   string
	}{
		{"请求非法", 400, "request"},
		{"成员无权限", 403, "member"},
		{"上游过载", 503, "transient"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := &RequestState{ID: 9003, Round: 0}
			// 用一堆与状态码无关的文案, 证明分类确实没看文本。
			finishWithWait(state, 55, "channel", "model",
				newUpstreamStatusError(tc.status, "完全无关的中文措辞，不含任何状态码"), false)
			state.archiveRoundLocked()

			if len(state.AttemptChain) != 1 {
				t.Fatalf("尝试链长度 = %d, want 1", len(state.AttemptChain))
			}
			if got := state.AttemptChain[0].FaultKind; got != tc.want {
				t.Errorf("status %d 的归因 = %q, want %q", tc.status, got, tc.want)
			}
		})
	}
}

// 从未打过上游的请求（分组不存在等）不该产生任何尝试记录。
func TestAttemptChainEmptyWhenNoUpstreamAttempt(t *testing.T) {
	state := &RequestState{ID: 9004, Round: 0}
	state.archiveRoundLocked()
	if len(state.AttemptChain) != 0 {
		t.Fatalf("没打过上游却有 %d 条尝试记录", len(state.AttemptChain))
	}
}

// 超长请求的链必须被截断并**显式标记**, 而不是静默丢掉前几轮。
func TestAttemptChainTruncatesAndFlags(t *testing.T) {
	state := &RequestState{ID: 9005, Round: 0}
	total := model.RelayAttemptDetailMax + 5
	for i := 0; i < total; i++ {
		if i > 0 {
			// 上一轮的收尾: 除最后一轮外, 每轮都失败。
			state.finishRound(newUpstreamStatusError(500, "boom"), false)
		}
		state.startRound(nil, 100+i, "channel", "model", model.ProtocolOpenAIChatCompletion)
	}
	state.finishRound(nil, false)
	state.archiveRoundLocked()

	if len(state.AttemptChain) != model.RelayAttemptDetailMax {
		t.Fatalf("截断后长度 = %d, want %d", len(state.AttemptChain), model.RelayAttemptDetailMax)
	}
	if !state.AttemptsTruncated {
		t.Error("截断后必须置 AttemptsTruncated, 否则读的人会以为只试了这些")
	}
	// 截断从头部删, 因此链上应保留靠后的轮次（最后一条是第 total 轮）。
	last := state.AttemptChain[len(state.AttemptChain)-1]
	if last.Round != total {
		t.Errorf("链尾 Round = %d, want %d（应从头部截断）", last.Round, total)
	}
	first := state.AttemptChain[0]
	if first.Round != total-model.RelayAttemptDetailMax+1 {
		t.Errorf("链首 Round = %d, want %d", first.Round, total-model.RelayAttemptDetailMax+1)
	}
}

// 链上轮次必须严格递增且无重复 —— 归档时机错位（重复归档同一轮）会立刻暴露。
func TestAttemptChainRoundsAreStrictlyIncreasing(t *testing.T) {
	state := &RequestState{ID: 9006, Round: 0}
	for i := 0; i < 4; i++ {
		if i > 0 {
			state.finishRound(newUpstreamStatusError(429, "rate limited"), false)
		}
		state.startRound(nil, 200+i, "channel", "model", model.ProtocolOpenAIChatCompletion)
	}
	state.finishRound(nil, false)
	state.archiveRoundLocked()

	if len(state.AttemptChain) != 4 {
		t.Fatalf("尝试链长度 = %d, want 4", len(state.AttemptChain))
	}
	for i, got := range state.AttemptChain {
		if got.Round != i+1 {
			t.Errorf("链[%d].Round = %d, want %d（必须严格递增、不重不漏）", i, got.Round, i+1)
		}
	}
}
