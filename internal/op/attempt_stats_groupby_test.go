package op

// 尝试链聚合的分类维度判据。
//
// 这个功能的核心风险不是"算错"，而是**三个维度之间对不上**：
// 用户按渠道看到"共尝试 83 次"，切到模型维度却看到总数变了 —— 那说明
// 有一类尝试在某个维度里被丢掉了。所以判据必须逐维度断言守恒。

import (
	"context"
	"testing"
	"time"

	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
)

// seedGroupByLogs 造两条请求，覆盖三个维度的差异：
//
//	请求1  model=grp-A  apikey=key-1  两次尝试：channelA 失败(member) → channelB 成功
//	请求2  model=grp-B  apikey=key-2  一次尝试：channelA 成功
func seedGroupByLogs(t *testing.T) {
	t.Helper()
	conn := openAttemptStatsTestDB(t)
	// openAttemptStatsTestDB 只建库不接线；AttemptChainStats 走全局 db。
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	now := time.Now()
	logs := []model.RelayLog{
		{
			RequestID: 1, Status: "success", Model: "grp-A", APIKeyName: "key-1",
			TargetChannel: "channelB", TargetModel: "upstream-b", StartedAt: now,
			Attempts: 2,
			AttemptDetail: []model.RelayAttemptDetail{
				{Channel: "channelA", Error: "boom", FaultKind: "member"},
				{Channel: "channelB"},
			},
		},
		{
			RequestID: 2, Status: "success", Model: "grp-B", APIKeyName: "key-2",
			TargetChannel: "channelA", TargetModel: "upstream-a", StartedAt: now,
			Attempts: 1,
			AttemptDetail: []model.RelayAttemptDetail{
				{Channel: "channelA"},
			},
		},
	}
	for i := range logs {
		if err := conn.Create(&logs[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

// 三个维度下 attempts 总数必须一致 —— 这是本功能最重要的守恒式。
//
// 不一致就说明某个维度把尝试丢了（例如请求级维度下只计了"轮数"而不是"尝试次数"）。
func TestAttemptChainGroupByConservesTotalAttempts(t *testing.T) {
	seedGroupByLogs(t)

	var totals []int64
	for _, gb := range []AttemptGroupBy{
		AttemptGroupByChannel, AttemptGroupByModel, AttemptGroupByAPIKey,
	} {
		got, err := AttemptChainStats(context.Background(), 100, gb)
		if err != nil {
			t.Fatalf("group_by=%s: %v", gb, err)
		}
		var sum int64
		for _, g := range got.Groups {
			sum += g.Attempts
		}
		totals = append(totals, sum)
		if got.GroupBy != gb {
			t.Errorf("回带的 GroupBy = %q, want %q", got.GroupBy, gb)
		}
	}

	if totals[0] != totals[1] || totals[1] != totals[2] {
		t.Errorf("三个维度的 attempts 总数应一致，实得 channel=%d model=%d apikey=%d",
			totals[0], totals[1], totals[2])
	}
	if totals[0] != 3 {
		t.Errorf("本装置共 3 次尝试，实得 %d", totals[0])
	}
}

// 按渠道：channelA 2 次（1 失败 1 成功），channelB 1 次。
func TestAttemptChainGroupByChannel(t *testing.T) {
	seedGroupByLogs(t)
	got, err := AttemptChainStats(context.Background(), 100, AttemptGroupByChannel)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	byName := map[string]AttemptGroupStat{}
	for _, g := range got.Groups {
		byName[g.Name] = g
	}
	if a, ok := byName["channelA"]; !ok || a.Attempts != 2 || a.Failures != 1 || a.MemberFault != 1 {
		t.Errorf("channelA = %+v, want attempts=2 failures=1 member=1", a)
	}
	if b, ok := byName["channelB"]; !ok || b.Attempts != 1 || b.Failures != 0 {
		t.Errorf("channelB = %+v, want attempts=1 failures=0", b)
	}
}

// 按模型：一次请求的**每次**尝试都归到同一个模型名下。
//
// 这条正是"与渠道维度的语义差异"所在 —— 若写成"每个分组只计一次尝试"，
// grp-A 会变成 attempts=1，与渠道维度的总数就对不上了。
func TestAttemptChainGroupByModelCountsEveryAttempt(t *testing.T) {
	seedGroupByLogs(t)
	got, err := AttemptChainStats(context.Background(), 100, AttemptGroupByModel)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	byName := map[string]AttemptGroupStat{}
	for _, g := range got.Groups {
		byName[g.Name] = g
	}
	a, ok := byName["grp-A"]
	if !ok {
		t.Fatalf("grp-A 应出现在模型维度，实得 %+v", got.Groups)
	}
	if a.Attempts != 2 {
		t.Errorf("grp-A attempts = %d, want 2（一次请求的每次尝试都要计入）", a.Attempts)
	}
	if a.Failures != 1 || a.MemberFault != 1 {
		t.Errorf("grp-A 失败归因 = %+v, want failures=1 member=1", a)
	}
	if b, ok := byName["grp-B"]; !ok || b.Attempts != 1 {
		t.Errorf("grp-B = %+v, want attempts=1", b)
	}
}

// 按 API Key：与模型同理，逐次尝试计入。
func TestAttemptChainGroupByAPIKey(t *testing.T) {
	seedGroupByLogs(t)
	got, err := AttemptChainStats(context.Background(), 100, AttemptGroupByAPIKey)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	byName := map[string]AttemptGroupStat{}
	for _, g := range got.Groups {
		byName[g.Name] = g
	}
	if k, ok := byName["key-1"]; !ok || k.Attempts != 2 || k.Failures != 1 {
		t.Errorf("key-1 = %+v, want attempts=2 failures=1", k)
	}
	if k, ok := byName["key-2"]; !ok || k.Attempts != 1 || k.Failures != 0 {
		t.Errorf("key-2 = %+v, want attempts=1 failures=0", k)
	}
}

// 请求级维度下字段为空 → 归到 "(未记录)"，而不是被丢掉。
//
// 丢掉会让各维度的总数对不上，那种不一致比缺一个名字更难查。
func TestAttemptChainGroupByEmptyNameFallsBackToUnnamed(t *testing.T) {
	conn := openAttemptStatsTestDB(t)
	restore := db.SetDBForTest(conn)
	t.Cleanup(restore)
	now := time.Now()
	row := model.RelayLog{
		RequestID: 9, Status: "success", Model: "", APIKeyName: "",
		TargetChannel: "ch", StartedAt: now, Attempts: 1,
		AttemptDetail: []model.RelayAttemptDetail{{Channel: "ch"}},
	}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	got, err := AttemptChainStats(context.Background(), 100, AttemptGroupByModel)
	if err != nil {
		t.Fatalf("AttemptChainStats: %v", err)
	}
	if len(got.Groups) != 1 || got.Groups[0].Name != attemptGroupUnnamed {
		t.Errorf("空模型名应归到 %q，实得 %+v", attemptGroupUnnamed, got.Groups)
	}
	if got.Groups[0].Attempts != 1 {
		t.Errorf("归到未记录后仍要计入 attempts，实得 %d", got.Groups[0].Attempts)
	}
}

// 非法维度必须被拒（上层据此回 400）。
func TestAttemptGroupByValidRejectsUnknown(t *testing.T) {
	for _, bad := range []string{"target_model", "channel;drop", "CHANNEL", "fault"} {
		if _, ok := AttemptGroupByValid(bad); ok {
			t.Errorf("%q 不该被接受", bad)
		}
	}
	// 空值按 channel —— 既有调用方不传该参数时行为逐字不变。
	if gb, ok := AttemptGroupByValid(""); !ok || gb != AttemptGroupByChannel {
		t.Errorf("空值应归一为 channel，实得 %q ok=%v", gb, ok)
	}
	for _, good := range []string{"channel", "model", "apikey"} {
		if _, ok := AttemptGroupByValid(good); !ok {
			t.Errorf("%q 应被接受", good)
		}
	}
}
