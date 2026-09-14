package relay

import (
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// TestRankCandidatesRoundRobinSequence 锁定平滑加权轮询的当选序列与周期取模口径。
// 权重 3/2/1（priority 1/2/3）total=6，经典 SWRR 重放：
// round0 [3,2,1]→成员1；round1 [0,4,2]→成员2；round2 [3,0,3]→成员1（首个最大值胜出）；
// round3 [0,2,4]→成员3；round4 [3,4,-1]→成员2；round5 [6,0,0]→成员1。
// 故 0..5 当选序列为 [1,2,1,3,2,1]，round6 取模回到 round0。
func TestRankCandidatesRoundRobinSequence(t *testing.T) {
	items := []model.GroupItem{
		{ID: 1, Priority: 1, Available: true},
		{ID: 2, Priority: 2, Available: true},
		{ID: 3, Priority: 3, Available: true},
	}
	want := []int{1, 2, 1, 3, 2, 1}
	for round := uint64(0); round < 6; round++ {
		ranked, err := rankCandidates(items, nil, time.Now().UnixMilli(), nil, nil, round)
		if err != nil {
			t.Fatalf("round %d err: %v", round, err)
		}
		if ranked[0].ID != want[round] {
			t.Fatalf("round %d head = %d, want %d", round, ranked[0].ID, want[round])
		}
	}
	// 周期取模：round6 与 round0 同一当选者（长期运行 round 上亿不重放挂死）。
	sixth, err := rankCandidates(items, nil, time.Now().UnixMilli(), nil, nil, 6)
	if err != nil {
		t.Fatalf("round 6 err: %v", err)
	}
	if sixth[0].ID != want[0] {
		t.Fatalf("round 6 head = %d, want %d (modulo cycle)", sixth[0].ID, want[0])
	}
}

// TestRankCandidatesCooldownReturns 冷却到期成员回到 eligible 区而非队尾。
// 权重 2/1（priority 1/2）：round1 时 eligible 双成员当选者为成员 2；
// 若冷却仍生效，eligible 仅成员 1，当选者恒为成员 1。两者可测区分。
func TestRankCandidatesCooldownReturns(t *testing.T) {
	items := []model.GroupItem{
		{ID: 1, Priority: 1, Available: true},
		{ID: 2, Priority: 2, Available: true},
	}
	now := time.Now().UnixMilli()
	expired := map[int]int64{2: now - 1000}
	ranked, err := rankCandidates(items, expired, now, nil, nil, 1)
	if err != nil {
		t.Fatalf("expired cooldown err: %v", err)
	}
	if ranked[0].ID != 2 {
		t.Fatalf("expired cooldown head = %d, want 2 (member returned to eligible)", ranked[0].ID)
	}
	active := map[int]int64{2: now + 60_000}
	cooled, err := rankCandidates(items, active, now, nil, nil, 1)
	if err != nil {
		t.Fatalf("active cooldown err: %v", err)
	}
	if cooled[0].ID != 1 || len(cooled) != 2 || cooled[1].ID != 2 {
		t.Fatalf("active cooldown ranked = %+v, want [1 2] (cooling pressed to tail)", cooled)
	}
}

// TestRankCandidatesLatencyKeepsWeightOrderAcrossTiers 延迟排序不跨 priority 档：
// 同档内按延迟升序，跨档仍权重序优先。成员 3 延迟最低但 priority 最低，不得越过前档。
func TestRankCandidatesLatencyKeepsWeightOrderAcrossTiers(t *testing.T) {
	items := []model.GroupItem{
		{ID: 1, Priority: 1, Available: true},
		{ID: 2, Priority: 1, Available: true},
		{ID: 3, Priority: 2, Available: true},
	}
	latency := func(id int) (int64, bool) {
		switch id {
		case 1:
			return 300, true
		case 2:
			return 50, true
		case 3:
			return 10, true
		default:
			return 0, false
		}
	}
	ranked, err := rankCandidates(items, nil, time.Now().UnixMilli(), nil, latency, 0)
	if err != nil {
		t.Fatalf("rank err: %v", err)
	}
	got := []int{ranked[0].ID, ranked[1].ID, ranked[2].ID}
	want := []int{2, 1, 3}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (latency sorts within tier only)", got, want)
		}
	}
}
