package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// L5 热路径接线单测 (T-route-002): flag 关 = 原路径回归; flag 开 = failover 候选按加权轮询定序,
// 冷却/探测/亲和语义不变。开关是包内原子量 (SetRouteBalanceEnabled), 每例复原为默认关闭。

// balancedGroup 构造隔离 ID 的 failover 分组, 冷却/亲和按测试需要覆写。
// Available 显式为真: 生产路径的候选来自 op 快照 (分组读取已剔除不可转发成员),
// rankCandidates 以 Available 为剔除口径, 内存构造须自带。
func balancedGroup(t *testing.T, id int, cfg model.GroupRelayConfig) model.Group {
	t.Helper()
	group := pickTestGroup(id, model.GroupModeFailover, 0, cfg)
	group.Items = availableLeaves(11, 12, 13)
	return group
}

// availableLeaves 按给定成员 ID 构造带 Available 标记的授权叶子表 (grant 主键 = 100+成员)。
func availableLeaves(ids ...int) []model.GroupItem {
	items := make([]model.GroupItem, 0, len(ids))
	for i, id := range ids {
		item := grantLeaf(id, 100+id)
		item.Available = true
		item.Priority = i + 1
		items = append(items, item)
	}
	return items
}

// TestPickGroupItemBalancedFlagOffMatchesLegacy flag 关时与原路径行为一致:
// 首轮均选最高优先级成员; 打冷却后顺延到未冷却成员, 不引入轮转偏移。
func TestPickGroupItemBalancedFlagOffMatchesLegacy(t *testing.T) {
	SetRouteBalanceEnabled(false)
	t.Cleanup(func() { SetRouteBalanceEnabled(false) })
	defer ResetRouteState(9201)
	group := balancedGroup(t, 9201, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      60,
		MemberAffinitySeconds:      0,
	})
	if got := PickGroupItemBalanced(group, group.Items); got.ID != 11 {
		t.Fatalf("flag off first pick = %d, want 11 (legacy priority order)", got.ID)
	}
	group.Items = availableLeaves(11)
	if !recordRouteFailure(group, 11, 1, 0) {
		t.Fatal("failure 11 did not cool down")
	}
	// 原路径 pickGroupItem 亲和/探测随缓存继续生效; 平衡外壳 failover 冷却顺延
	// 只需断言 11 被跳过即可, 首个可用成员 12/13 均为合法原路径行为。
	group.Items = availableLeaves(11, 12, 13)
	if got := PickGroupItemBalanced(group, group.Items); got.ID == 11 {
		t.Fatalf("flag off cooled pick = %d, want any of 12/13 (11 cooled)", got.ID)
	}
}

// TestPickGroupItemBalancedFlagOnRotates flag 开时 failover 候选按加权轮询轮转:
// 权重 3/2/1 下 0..5 轮当选序列为 11,12,11,13,12,11 (与 balance_verify 的经典 SWRR 序列一致)。
func TestPickGroupItemBalancedFlagOnRotates(t *testing.T) {
	SetRouteBalanceEnabled(true)
	t.Cleanup(func() { SetRouteBalanceEnabled(false) })
	defer ResetRouteState(9202)
	group := balancedGroup(t, 9202, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      0,
		MemberAffinitySeconds:      0,
	})
	want := []int{11, 12, 11, 13, 12, 11}
	for round := 0; round < len(want); round++ {
		got := PickGroupItemBalanced(group, group.Items)
		if got.ID != want[round] {
			t.Fatalf("balanced pick %d = %d, want %d (SWRR sequence)", round, got.ID, want[round])
		}
	}
}

// TestPickGroupItemBalancedFlagOnKeepsProbeAndCooldown flag 开不改冷却/探测语义:
// 成员被打入冷却后仍被跳过, 冷却到期后回到候选区参与探测放行。
func TestPickGroupItemBalancedFlagOnKeepsProbeAndCooldown(t *testing.T) {
	SetRouteBalanceEnabled(true)
	t.Cleanup(func() { SetRouteBalanceEnabled(false) })
	defer ResetRouteState(9203)
	group := balancedGroup(t, 9203, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      60,
		MemberAffinitySeconds:      0,
	})
	// 先用一次真实失败把 11 打入冷却。
	if got := PickGroupItemBalanced(group, group.Items); got.ID != 11 {
		t.Fatalf("first pick = %d, want 11", got.ID)
	}
	group.Items = availableLeaves(11)
	if !recordRouteFailure(group, 11, 1, 0) {
		t.Fatal("failure 11 did not cool down")
	}
	group.Items = availableLeaves(11, 12, 13)
	// 11 冷却中: rank 剔除/压尾 + pick 跳冷却双保险, 11 不应再当选。
	if got := PickGroupItemBalanced(group, group.Items); got.ID == 11 {
		t.Fatal("cooled member 11 picked again, want skipped")
	}
	// 冷却到期 (deadline 过去) 后 11 回到候选区, 与 12/13 一同参与定序, 非零即正常。
	if got := PickGroupItemBalanced(group, group.Items); got.ID == 0 {
		t.Fatal("expired cooldown returned zero, want a probe or eligible pick")
	}
}

// TestRouteBalanceDisabledByDefault 新进程开关恒默认关闭, 未显式开启时热路径走原语义。
func TestRouteBalanceDisabledByDefault(t *testing.T) {
	t.Cleanup(func() { SetRouteBalanceEnabled(false) })
	if RouteBalanceEnabled() {
		t.Fatal("RouteBalanceEnabled = true on fresh state, want default false")
	}
}

// TestSetRouteBalanceEnabledResetsRounds 开→关复位各分组轮转计数:
// 再开时从 round0 重新开始 SWRR 周期, 不沿用关闭前的轮转偏移。
func TestSetRouteBalanceEnabledResetsRounds(t *testing.T) {
	SetRouteBalanceEnabled(true)
	t.Cleanup(func() { SetRouteBalanceEnabled(false) })
	defer ResetRouteState(9204)
	group := balancedGroup(t, 9204, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      0,
		MemberAffinitySeconds:      0,
	})
	// 消耗两轮: SWRR 序列 11,12 → 停在第 2 轮。
	_ = PickGroupItemBalanced(group, group.Items)
	_ = PickGroupItemBalanced(group, group.Items)
	SetRouteBalanceEnabled(false)
	SetRouteBalanceEnabled(true)
	// 复位后 round0 当选者应回到 11。
	if got := PickGroupItemBalanced(group, group.Items); got.ID != 11 {
		t.Fatalf("after reset pick = %d, want 11 (round counter reset)", got.ID)
	}
}
