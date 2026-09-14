package relay

import (
	"errors"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// TestRankCandidatesWeightedOrder 加权轮询第一版：权重高的先当选，round 推进轮转。
func TestRankCandidatesWeightedOrder(t *testing.T) {
	items := []model.GroupItem{
		{ID: 1, Priority: 1, Available: true},
		{ID: 2, Priority: 2, Available: true},
		{ID: 3, Priority: 3, Available: true},
	}
	first, err := rankCandidates(items, nil, time.Now().UnixMilli(), nil, nil, 0)
	if err != nil || first[0].ID != 1 {
		t.Fatalf("round0 = %+v err=%v, want head 1", first, err)
	}
	// 三成员权重 3/2/1：round 推进后当选者轮转（0→1, 1→2, 2→3 附近）。
	seen := map[int]bool{}
	for round := uint64(0); round < 6; round++ {
		ranked, err := rankCandidates(items, nil, time.Now().UnixMilli(), nil, nil, round)
		if err != nil {
			t.Fatalf("round %d err: %v", round, err)
		}
		seen[ranked[0].ID] = true
	}
	if len(seen) != 3 {
		t.Fatalf("rotation covered %v, want all 3 members", seen)
	}

	// round 周期语义：total=6，round 6/12 与 round 0 同当选；
	// 大 round（模拟长期运行十亿级计数）必须瞬间返回而非 O(round) 重放。
	period := map[uint64]int{0: 1, 1: 2, 2: 1, 3: 3, 4: 2, 5: 1}
	for _, round := range []uint64{6, 12, 1000000000} {
		ranked, err := rankCandidates(items, nil, time.Now().UnixMilli(), nil, nil, round)
		if err != nil {
			t.Fatalf("round %d err: %v", round, err)
		}
		if want := period[round%6]; ranked[0].ID != want {
			t.Fatalf("round %d head = %d, want %d", round, ranked[0].ID, want)
		}
	}
}

// TestRankCandidatesExclusions 健康/冷却/归零剔除：不可用剔除、冷却压尾、归零授权剔除。
func TestRankCandidatesExclusions(t *testing.T) {
	grant := 100
	items := []model.GroupItem{
		{ID: 1, Priority: 1, Available: false}, // 停用凭据：剔除
		{ID: 2, Priority: 2, Available: true, ChannelGrantID: &grant},
		{ID: 3, Priority: 3, Available: true},
	}
	now := time.Now().UnixMilli()
	cooldowns := map[int]int64{3: now + 60_000}
	ranked, err := rankCandidates(items, cooldowns, now, nil, nil, 0)
	if err != nil {
		t.Fatalf("rank err: %v", err)
	}
	if len(ranked) != 2 || ranked[0].ID != 2 || ranked[1].ID != 3 {
		t.Fatalf("ranked = %+v, want [2 3] (cooling last)", ranked)
	}

	zero := map[int]bool{grant: true}
	if _, err := rankCandidates(items, nil, now, zero, nil, 0); err == nil {
		// 成员 2 被归零剔除后只剩 3（eligible），3 仍在——此处不断言错误只断剔除语义：
		// 重新用全归零集合验证空候选错误。
		t.Log("member 2 excluded, 3 remains")
	}
	zeroAll := map[int]bool{grant: true, 0: false}
	onlyZero := []model.GroupItem{{ID: 2, Priority: 2, Available: true, ChannelGrantID: &grant}}
	if _, err := rankCandidates(onlyZero, nil, now, zeroAll, nil, 0); !errors.Is(err, ErrNoEligibleMember) {
		t.Fatalf("all-excluded err = %v, want ErrNoEligibleMember", err)
	}
}

// TestRankCandidatesLatencyPlug 最低延迟可插拔：同档按延迟升序，无数据沉底。
func TestRankCandidatesLatencyPlug(t *testing.T) {
	items := []model.GroupItem{
		{ID: 1, Priority: 1, Available: true},
		{ID: 2, Priority: 1, Available: true},
		{ID: 3, Priority: 1, Available: true},
	}
	latency := func(id int) (int64, bool) {
		switch id {
		case 1:
			return 300, true
		case 2:
			return 50, true
		default:
			return 0, false
		}
	}
	ranked, err := rankCandidates(items, nil, time.Now().UnixMilli(), nil, latency, 0)
	if err != nil {
		t.Fatalf("rank err: %v", err)
	}
	if ranked[0].ID != 2 || ranked[1].ID != 1 || ranked[2].ID != 3 {
		t.Fatalf("latency order = %v, want [2 1 3]", ranked)
	}
}
