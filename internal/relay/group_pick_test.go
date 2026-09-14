package relay

import (
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// pickTestGroup 用唯一分组 ID 隔离路由状态, 冷却/亲和配置按需覆写。
func pickTestGroup(id int, mode model.GroupMode, active int, cfg model.GroupRelayConfig) model.Group {
	return model.Group{ID: id, Name: "g", Mode: mode, ActiveItemID: active, RelayConfig: cfg}
}

func grantLeaf(itemID, grantID int) model.GroupItem {
	ref := grantID
	return model.GroupItem{ID: itemID, ChannelGrantID: &ref}
}

// TestPickGroupItemSkipsCoolingLeaf 冷却按展平后的叶子成员行 ID 生效: 被打入冷却的
// 叶子在下一轮被跳过, 选到下一个可用叶子 (语义冻结: 状态挂顶层 group.ID, 键为叶子 ID)。
func TestPickGroupItemSkipsCoolingLeaf(t *testing.T) {
	defer ResetRouteState(9001)
	group := pickTestGroup(9001, model.GroupModeFailover, 0, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      60,
		MemberAffinitySeconds:      0,
	})
	flat := []model.GroupItem{grantLeaf(11, 101), grantLeaf(12, 102)}

	if got := PickGroupItem(group, flat); got.ID != 11 {
		t.Fatalf("first pick = %d, want 11", got.ID)
	}
	// 成员 11 一轮失败即冷却 (max attempts = 1)。
	if !recordRouteFailure(group, 11, 1) {
		t.Fatal("failure 11 did not cool down")
	}
	if got := PickGroupItem(group, flat); got.ID != 12 {
		t.Fatalf("after cooling 11 pick = %d, want 12", got.ID)
	}
}

// TestPickGroupItemAffinityKeepsFailoverTarget 亲和期内即便高优先级叶子已恢复, 仍沿用
// 当前承载叶子: 亲和/冷却键是展平后成员行 ID, 但选路状态按顶层分组持有。
func TestPickGroupItemAffinityKeepsFailoverTarget(t *testing.T) {
	defer ResetRouteState(9002)
	group := pickTestGroup(9002, model.GroupModeFailover, 0, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      60,
		MemberAffinitySeconds:      300,
	})
	flat := []model.GroupItem{grantLeaf(21, 201), grantLeaf(22, 202)}

	if got := PickGroupItem(group, flat); got.ID != 21 {
		t.Fatalf("first pick = %d, want 21", got.ID)
	}
	recordRouteFailure(group, 21, 1)
	if got := PickGroupItem(group, flat); got.ID != 22 {
		t.Fatalf("failover pick = %d, want 22", got.ID)
	}
	// 备用叶子成功一次即按配置建立亲和窗口。
	recordRouteSuccess(group, 22)
	// 即便 21 的冷却被人工解除, 亲和期内仍沿用 22。
	if got := PickGroupItem(group, flat); got.ID != 22 {
		t.Fatalf("affinity pick = %d, want 22 (still on failover target)", got.ID)
	}
}

// TestPickGroupItemManualSelectsActiveLeaf 手动模式恒取人工指定成员: 展平后命中该
// 叶子即返回, 不受冷却/亲和影响; 指定为 0 或未命中返回零值, 调用方按无目标等待。
func TestPickGroupItemManualSelectsActiveLeaf(t *testing.T) {
	group := pickTestGroup(9003, model.GroupModeManual, 32, model.GroupRelayConfig{})
	flat := []model.GroupItem{grantLeaf(31, 301), grantLeaf(32, 302)}
	if got := PickGroupItem(group, flat); got.ID != 32 {
		t.Fatalf("manual pick = %d, want 32", got.ID)
	}
	group.ActiveItemID = 0
	if got := PickGroupItem(group, flat); got.ID != 0 {
		t.Fatalf("manual no-select pick = %d, want 0", got.ID)
	}
}

// TestPickGroupItemEmptyFlatIsZero 空平面表恒返回零值 (空树/悬空引用展平即空表)。
func TestPickGroupItemEmptyFlatIsZero(t *testing.T) {
	defer ResetRouteState(9004)
	group := pickTestGroup(9004, model.GroupModeFailover, 0, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      60,
	})
	if got := PickGroupItem(group, nil); got.ID != 0 {
		t.Fatalf("empty flat pick = %d, want 0", got.ID)
	}
}

// TestPickGroupItemDedupLeavesFlat 菱形共享子组展平后同一叶子行只出现一次
// (去重语义在 op.Flatten 层保证, 选路层假定输入已去重并稳定命中首叶)。
func TestPickGroupItemDedupLeavesFlat(t *testing.T) {
	defer ResetRouteState(9005)
	group := pickTestGroup(9005, model.GroupModeFailover, 0, model.GroupRelayConfig{
		MemberMaxAttempts:          1,
		MemberRetryIntervalSeconds: 1,
		MemberCooldownSeconds:      60,
	})
	flat := []model.GroupItem{grantLeaf(41, 401)}
	if got := PickGroupItem(group, flat); got.ID != 41 {
		t.Fatalf("dedup pick = %d, want 41", got.ID)
	}
}

// TestPickGroupItemManualChildRefZero 手动模式 ActiveItemID 指向子分组引用行时回零等待。
// 引用行本身不在展平平面表内（splice 进来的是子组成员，原行不进入结果）；
// 整块选中语义待后续任务定义，本轮冻结为确定行为：回零由 handler 按无目标等待，
// 不误选块内首叶造成人工指定漂移。
func TestPickGroupItemManualChildRefZero(t *testing.T) {
	group := pickTestGroup(9005+1, model.GroupModeManual, 12, model.GroupRelayConfig{})
	flat := []model.GroupItem{grantLeaf(11, 101), grantLeaf(21, 102)}
	if got := PickGroupItem(group, flat); got.ID != 0 {
		t.Fatalf("manual child-ref pick = %d, want 0", got.ID)
	}
}
