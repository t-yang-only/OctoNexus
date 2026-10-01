package relay

import (
	"testing"
	"time"
)

// T-usability-004 「分组连续无可用成员」的等待上限。
//
// ## 为什么需要这条判据
//
// 修之前的行为是**无限等待**：选出成员失败就 sleep 一段再重试，直到客户端自己断开。
// 对"成员正在冷却、等一会儿就好"是合理的，但对"分组永久没有成员"
// （成员被删、或渠道授权撤销后残留的自动分组）就是灾难 ——
// 请求一直挂着，用户看到的是"卡住不返回"，既不知道是分组的问题，
// 也不知道该去改什么。
//
// 实测踩到：撤销 senseaudio 授权后留下 23 个成员数为 0 的分组，
// 调用它们全部挂到超时（不是快速失败）。
//
// 这段逻辑的失败模式（上限判断永远返回 false）在端到端测试里
// 表现为"请求挂住"，非常难定位，所以单独给它一条判据。

// 未到上限时不该提前判死 —— 否则成员正常冷却恢复的场景会被误杀。
func TestMemberUnavailableWaitNotEnoughBeforeCap(t *testing.T) {
	old := memberUnavailableWaitCapSeconds
	memberUnavailableWaitCapSeconds = 60
	defer func() { memberUnavailableWaitCapSeconds = old }()

	// 刚开始等：不该判够。
	if memberUnavailableWaitedEnough(time.Now()) {
		t.Fatalf("刚开始等待就判定超限，会让正在冷却的成员被误杀")
	}
	// 等了一半：仍不该判够。
	if memberUnavailableWaitedEnough(time.Now().Add(-30 * time.Second)) {
		t.Fatalf("等待 30 秒（上限 60）就判定超限，过早")
	}
}

// 到上限时必须判够 —— 这是修复的核心：让永久无成员的分组尽快失败。
func TestMemberUnavailableWaitEnoughAtCap(t *testing.T) {
	old := memberUnavailableWaitCapSeconds
	memberUnavailableWaitCapSeconds = 60
	defer func() { memberUnavailableWaitCapSeconds = old }()

	// 恰好到上限。
	if !memberUnavailableWaitedEnough(time.Now().Add(-60 * time.Second)) {
		t.Fatalf("等待已达上限 60 秒仍未判定超限，请求会继续挂住")
	}
	// 明显超过上限。
	if !memberUnavailableWaitedEnough(time.Now().Add(-120 * time.Second)) {
		t.Fatalf("等待远超上限仍未判定超限")
	}
}

// 上限可调：单测把它压到 1 秒，判据要跟着变 —— 否则测试自己会等 60 秒。
func TestMemberUnavailableWaitCapIsRespected(t *testing.T) {
	old := memberUnavailableWaitCapSeconds
	defer func() { memberUnavailableWaitCapSeconds = old }()

	memberUnavailableWaitCapSeconds = 1
	if memberUnavailableWaitedEnough(time.Now()) {
		t.Fatalf("上限=1 秒时，刚开始不该判够")
	}
	if !memberUnavailableWaitedEnough(time.Now().Add(-2 * time.Second)) {
		t.Fatalf("上限=1 秒时，等 2 秒应判够")
	}

	// 上限变大后，同一个时刻应从「够」变成「不够」——证明读的是变量而不是写死的值。
	memberUnavailableWaitCapSeconds = 600
	if memberUnavailableWaitedEnough(time.Now().Add(-2 * time.Second)) {
		t.Fatalf("上限改成 600 秒后，等 2 秒不该判够（说明上限没生效）")
	}
}

// 默认上限必须是有限的正数：0 或负数会让每个请求立刻失败（连冷却恢复都等不到），
// 无限大则等于没修。
func TestMemberUnavailableWaitCapDefaultIsSane(t *testing.T) {
	if memberUnavailableWaitCapSeconds <= 0 {
		t.Fatalf("默认上限 %d 不合法：非正数会让正常冷却恢复的请求被立刻拒绝",
			memberUnavailableWaitCapSeconds)
	}
	if memberUnavailableWaitCapSeconds > 300 {
		t.Fatalf("默认上限 %d 秒太长：永久无成员的分组会让客户端挂太久，体感就是卡死",
			memberUnavailableWaitCapSeconds)
	}
}
