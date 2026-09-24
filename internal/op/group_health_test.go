package op

import (
	"context"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"gorm.io/gorm"
)

// T-insight-008「分组健康」的判据。
//
// ## 每一条判据对着一个具体的错误写法
//
//  1. 状态判定必须**分得开**：一个分组只满足一个条件时不能被别的条件吃掉
//     （最常见的是"没有成员"被后面几档盖住，于是空分组显示成 idle 甚至 healthy）；
//  2. **配置问题优先于流量**：成员全不可用的分组，即使历史上成功过，也不能判 healthy；
//  3. **空闲不是健康**：没人用的分组不能显示成一切正常；
//  4. 成功率的**分母不是请求数**：取消与"请求本身非法"必须被排除，
//     否则客户端发错参数会把分组健康度拉低，而分组完全无辜；
//  5. 无上游落点的失败（`无可用成员`）**必须留行**——这类失败没有 target_channel，
//     恰好是最该被看见的一类，丢了它就等于这个面板白做；
//  6. 排序输入来自 map 遍历，端到端用例会让"少写一个末位键"的实现偶发排对，
//     因此排序必须由**纯函数 + 确定性输入 + 多轮轮换**来钉。

// groupHealthLogSeed 是播种一行日志的最少字段。
type groupHealthLogSeed struct {
	groupID   int
	status    string
	faultKind string
	channel   string
	model     string
	startedAt time.Time
}

func seedGroupHealthLog(t *testing.T, conn *gorm.DB, s groupHealthLogSeed) {
	t.Helper()
	row := model.RelayLog{
		GroupID:       s.groupID,
		Status:        s.status,
		FaultKind:     s.faultKind,
		TargetChannel: s.channel,
		TargetModel:   s.model,
		StartedAt:     s.startedAt,
	}
	if err := conn.Create(&row).Error; err != nil {
		t.Fatalf("seed group health log: %v", err)
	}
}

// withGroupCache 把一个分组放进缓存快照，测试结束移除。
//
// 必须这样做而不能只往数据库塞行：GroupHealthStats 的配置侧事实读的是
// **缓存快照**（与界面、与选路同一份底座），数据库里的分组行不会被 GroupList 读到 ——
// 只塞库会得到"配置侧全空"的假象，让"成员全不可用"这类判据永远测不出来。
//
// 播进去的 GroupItem 不设 GrantRef，于是 groupSnapshot 里
// `channelGrantCache.Get(0)` 落空直接 continue，Available 保持播种值 ——
// 判据因此能精确控制"可用成员数"这个输入。
func withGroupCache(t *testing.T, group model.Group) {
	t.Helper()
	groupCache.Set(group.ID, group)
	t.Cleanup(func() { groupCache.Del(group.ID) })
}

// groupItemsWithAvailability 造 n 个成员，其中 available 个可用。
func groupItemsWithAvailability(groupID, n, available int) []model.GroupItem {
	items := make([]model.GroupItem, 0, n)
	for index := 0; index < n; index++ {
		items = append(items, model.GroupItem{
			ID:        groupID*100 + index + 1,
			GroupID:   groupID,
			Available: index < available,
		})
	}
	return items
}

// groupHealthRowOf 按名字取一行，取不到直接失败 —— 不用裸索引，
// 一个 panic 会终止整个测试进程、让后面的判据根本不执行（本项目踩过）。
func groupHealthRowOf(t *testing.T, out GroupHealthSummary, name string) GroupHealthRow {
	t.Helper()
	for _, row := range out.Groups {
		if row.Name == name {
			return row
		}
	}
	t.Fatalf("分组 %q 不在结果里（共 %d 行）", name, len(out.Groups))
	return GroupHealthRow{}
}

// 判据 1：六个状态在各自的隔离输入下都要能被判出来。
//
// 隔离设计：每个分组只满足自己那一条判据的触发条件 ——
// 空分组不播种请求，全不可用分组播种**成功**请求（这样"成功率低"这条不可能命中它），
// 空闲分组播种成员但不播种请求，依此类推。
func TestGroupHealthStatesAreIsolated(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()

	withGroupCache(t, model.Group{ID: 9001, Name: "gh-empty", Mode: model.GroupModeFailover})
	withGroupCache(t, model.Group{
		ID: 9002, Name: "gh-no-member", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9002, 3, 0),
	})
	withGroupCache(t, model.Group{
		ID: 9003, Name: "gh-idle", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9003, 3, 3),
	})
	withGroupCache(t, model.Group{
		ID: 9004, Name: "gh-healthy", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9004, 3, 3),
	})
	withGroupCache(t, model.Group{
		ID: 9005, Name: "gh-degraded", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9005, 3, 3),
	})
	withGroupCache(t, model.Group{
		ID: 9006, Name: "gh-failing", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9006, 3, 3),
	})
	// 高成功率边界：19 成功 1 故障 = 95%，高于降级线，必须判 healthy。
	// 没有这个用例，把降级线写成 100 也会全绿（100% 成功的组在两个阈值下都是 healthy）。
	withGroupCache(t, model.Group{
		ID: 9007, Name: "gh-near-perfect", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9007, 3, 3),
	})

	// no-member 组：历史上成功过 —— 若实现先看流量，它会显示成 healthy。
	seedGroupHealthLog(t, conn, groupHealthLogSeed{groupID: 9002, status: "success", startedAt: base})
	// healthy：10 条全成功。
	for index := 0; index < 10; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9004, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
	}
	// degraded：8 成功 2 成员故障 = 80%，落在 [50, 90) 区间内。
	for index := 0; index < 8; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9005, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
	}
	for index := 0; index < 2; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9005, status: "failed", faultKind: "member", channel: "chA",
			startedAt: base.Add(time.Duration(20+index) * time.Second),
		})
	}
	// failing：2 成功 2 失败 = 50% —— 恰好不低于阈值，所以再补一条失败压到 40%。
	for index := 0; index < 2; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9006, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
	}
	for index := 0; index < 3; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9006, status: "failed", faultKind: "transient", channel: "chB",
			startedAt: base.Add(time.Duration(10+index) * time.Second),
		})
	}
	// near-perfect：19 成功 + 1 成员故障 = 95%。
	for index := 0; index < 19; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9007, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
	}
	seedGroupHealthLog(t, conn, groupHealthLogSeed{
		groupID: 9007, status: "failed", faultKind: "member", channel: "chC",
		startedAt: base.Add(30 * time.Second),
	})

	out, err := GroupHealthStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("GroupHealthStats: %v", err)
	}

	cases := []struct {
		name  string
		state string
	}{
		{"gh-empty", GroupStateEmpty},
		{"gh-no-member", GroupStateNoMember},
		{"gh-idle", GroupStateIdle},
		{"gh-healthy", GroupStateHealthy},
		{"gh-degraded", GroupStateDegraded},
		{"gh-failing", GroupStateFailing},
		{"gh-near-perfect", GroupStateHealthy},
	}
	for _, testCase := range cases {
		row := groupHealthRowOf(t, out, testCase.name)
		if row.State != testCase.state {
			t.Errorf("%s: 状态=%q，期望 %q（成功率 %.2f，分母 %d，请求 %d，成员 %d/%d）",
				testCase.name, row.State, testCase.state, row.SuccessRate,
				row.SuccessSamples, row.Requests, row.AvailableCount, row.MemberCount)
		}
	}
}

// 判据 2：配置层的问题不能被流量盖过去。
//
// 两个方向都要钉：
//   - 空分组即使收到过请求也仍是 empty（成员为 0 与流量无关）；
//   - 成员全部不可用的分组即使成功率 100% 也仍是 no_member
//     —— 它现在发不出任何请求，历史成功说明不了当下的可用性。
func TestGroupHealthConfigBeatsTraffic(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()

	withGroupCache(t, model.Group{ID: 9101, Name: "gh-empty-but-used", Mode: model.GroupModeFailover})
	withGroupCache(t, model.Group{
		ID: 9102, Name: "gh-disabled-but-was-healthy", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9102, 4, 0),
	})
	for index := 0; index < 6; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9101, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9102, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
	}

	out, err := GroupHealthStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("GroupHealthStats: %v", err)
	}
	if row := groupHealthRowOf(t, out, "gh-empty-but-used"); row.State != GroupStateEmpty {
		t.Errorf("有请求的空分组应为 empty，实际 %q", row.State)
	}
	if row := groupHealthRowOf(t, out, "gh-disabled-but-was-healthy"); row.State != GroupStateNoMember {
		t.Errorf("成员全不可用的分组应为 no_member（成功率 %.2f 不该盖过配置），实际 %q",
			row.SuccessRate, row.State)
	}
	// 守恒式而不是等值断言：全局分组缓存可能被同包其它用例留了数据，
	// 但"各状态计数之和 == 行数、且每行状态都被归入某一档"这条恒成立。
	total := out.FailingCount + out.NoMemberCount + out.DegradedCount +
		out.EmptyCount + out.IdleCount + out.HealthyCount
	if total != len(out.Groups) {
		t.Errorf("状态计数之和=%d，与实际行数 %d 不等（有行的状态没被归类）", total, len(out.Groups))
	}
}

// 判据 3：成功率的分母排除「取消」与「请求本身非法」。
//
// 这是本功能最容易写错的一处：用 requests 当分母会得到
//   - 取消：客户端自己断的，谁也没失败，却把成功率拉低；
//   - 请求非法：客户端发错参数，换任何成员都一样失败，与分组无关。
//
// 两者都会让"分组到底行不行"这个结论失真。
func TestGroupHealthRateExcludesCanceledAndRequestFault(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()

	withGroupCache(t, model.Group{
		ID: 9201, Name: "gh-cancel-heavy", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9201, 2, 2),
	})
	withGroupCache(t, model.Group{
		ID: 9202, Name: "gh-bad-request", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9202, 2, 2),
	})
	withGroupCache(t, model.Group{
		ID: 9203, Name: "gh-real-failure", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9203, 2, 2),
	})

	for index := 0; index < 10; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9201, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9202, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9203, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
	}
	for index := 0; index < 5; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9201, status: "canceled", startedAt: base.Add(time.Duration(20+index) * time.Second),
		})
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9202, status: "failed", faultKind: "request", channel: "chX",
			startedAt: base.Add(time.Duration(20+index) * time.Second),
		})
	}
	// 反向对照：真正属于分组能力的失败必须计入分母（否则分母永远满分，功能失去意义）。
	for index := 0; index < 5; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9203, status: "failed", faultKind: "member", channel: "chY",
			startedAt: base.Add(time.Duration(20+index) * time.Second),
		})
	}

	out, err := GroupHealthStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("GroupHealthStats: %v", err)
	}

	canceled := groupHealthRowOf(t, out, "gh-cancel-heavy")
	if canceled.Requests != 15 {
		t.Errorf("取消组请求数=%d，期望 15（取消也要计入请求总数，只是不进分母）", canceled.Requests)
	}
	if canceled.SuccessSamples != 10 || canceled.SuccessRate != 100 {
		t.Errorf("取消不该进分母：分母=%d（期望 10）成功率=%.2f（期望 100）",
			canceled.SuccessSamples, canceled.SuccessRate)
	}
	if canceled.State != GroupStateHealthy {
		t.Errorf("全是成功与取消的分组应为 healthy，实际 %q", canceled.State)
	}

	badRequest := groupHealthRowOf(t, out, "gh-bad-request")
	if badRequest.RequestFault != 5 {
		t.Errorf("请求非法档=%d，期望 5（计数照常要给出）", badRequest.RequestFault)
	}
	if badRequest.SuccessSamples != 10 || badRequest.SuccessRate != 100 {
		t.Errorf("请求非法不该进分母：分母=%d（期望 10）成功率=%.2f（期望 100）",
			badRequest.SuccessSamples, badRequest.SuccessRate)
	}

	real := groupHealthRowOf(t, out, "gh-real-failure")
	if real.SuccessSamples != 15 {
		t.Errorf("成员故障必须进分母：分母=%d，期望 15", real.SuccessSamples)
	}
	if real.SuccessRate < 66 || real.SuccessRate > 67 {
		t.Errorf("成员故障组成功率=%.2f，期望约 66.67", real.SuccessRate)
	}
	// 取消不仅不进分母，**也不是失败**：它不该出现在失败落点里、也不该留下失败时刻。
	// 少了这两条，"取消算成失败"的实现只在失败下钻上出错、成功率却看不出异常。
	if len(canceled.FailingChannels) != 0 {
		t.Errorf("取消不该出现在失败渠道下钻里，实际 %+v", canceled.FailingChannels)
	}
	if canceled.LastFailureAt != "" {
		t.Errorf("全是成功与取消的分组不该有失败时刻，实际 %q", canceled.LastFailureAt)
	}
	// 反向对照：真的失败过的分组必须有落点，否则上面两条可能只是"永远为空"。
	if len(real.FailingChannels) == 0 {
		t.Errorf("有真实失败的分组必须给出失败落点")
	}
}

// 判据 4：没有失败的分组，失败时刻必须是空串而不是零值时刻。
// 零值会被 JSON 序列化成 0001-01-01T00:00:00Z，界面上读起来像"很久以前失败过"。
func TestGroupHealthZeroFailureTimeStaysEmpty(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	withGroupCache(t, model.Group{
		ID: 9301, Name: "gh-never-failed", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9301, 1, 1),
	})
	seedGroupHealthLog(t, conn, groupHealthLogSeed{groupID: 9301, status: "success", startedAt: base})

	out, err := GroupHealthStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("GroupHealthStats: %v", err)
	}
	row := groupHealthRowOf(t, out, "gh-never-failed")
	if row.LastFailureAt != "" {
		t.Errorf("从未失败的分组，LastFailureAt 应为空串，实际 %q", row.LastFailureAt)
	}
	if len(row.FailingChannels) != 0 {
		t.Errorf("从未失败的分组不该有失败渠道，实际 %d 条", len(row.FailingChannels))
	}
	if formatHealthTime(time.Time{}) != "" {
		t.Errorf("零值时刻必须格式化成空串")
	}
}

// 判据 5：失败落点的下钻——只收失败、按失败数降序、上限 5 条，
// 并且**没有上游落点的失败必须留行**（"无可用成员"这类失败没有 target_channel，
// 恰恰是最该被看见的一类）。
func TestGroupHealthFailingChannelsRankLimitAndChannelLess(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	withGroupCache(t, model.Group{
		ID: 9401, Name: "gh-many-failures", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9401, 2, 2),
	})

	// 6 个上游各失败若干次（超过上限 5，用来验证截断），加一批无渠道失败。
	channels := []string{"ch1", "ch2", "ch3", "ch4", "ch5", "ch6"}
	for index, channel := range channels {
		for count := 0; count <= index; count++ {
			seedGroupHealthLog(t, conn, groupHealthLogSeed{
				groupID: 9401, status: "failed", faultKind: "transient",
				channel: channel, model: "m-" + channel,
				startedAt: base.Add(time.Duration(index*10+count) * time.Second),
			})
		}
	}
	// 「无可用成员」形态：失败但没有上游落点。
	for index := 0; index < 7; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9401, status: "failed", faultKind: "member",
			startedAt: base.Add(time.Duration(100+index) * time.Second),
		})
	}
	// 成功行不该出现在失败下钻里。
	seedGroupHealthLog(t, conn, groupHealthLogSeed{
		groupID: 9401, status: "success", channel: "ch1", model: "m-ch1",
		startedAt: base.Add(200 * time.Second),
	})

	out, err := GroupHealthStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("GroupHealthStats: %v", err)
	}
	row := groupHealthRowOf(t, out, "gh-many-failures")
	if len(row.FailingChannels) != groupHealthChannelLimit {
		t.Fatalf("失败渠道应截断到 %d 条，实际 %d 条", groupHealthChannelLimit, len(row.FailingChannels))
	}
	if row.FailingChannels[0].Channel != "" || row.FailingChannels[0].Failures != 7 {
		t.Errorf("失败最多的是无上游落点的那一类（7 次），实际首条=%+v", row.FailingChannels[0])
	}
	if row.SuccessSamples != 29 {
		t.Errorf("分母=%d，期望 29（1 成功 + 28 失败，请求非法为 0）", row.SuccessSamples)
	}
	for _, channel := range row.FailingChannels {
		if channel.Channel == "ch1" && channel.Failures != 1 {
			t.Errorf("ch1 失败数=%d，期望 1（成功行不能被算成失败）", channel.Failures)
		}
	}
	// 降序：相邻两项不许上升。
	for index := 1; index < len(row.FailingChannels); index++ {
		if row.FailingChannels[index-1].Failures < row.FailingChannels[index].Failures {
			t.Errorf("失败渠道未按失败数降序：%+v", row.FailingChannels)
			break
		}
	}
}

// 判据 6：排序由纯函数钉死，且对输入顺序不敏感。
//
// 端到端用例不够：排序输入来自 map 遍历（Go 顺序随机），
// "少写一个末位比较键"的实现会偶发排对，端到端跑十次可能全绿。
func TestSortGroupHealthRowsIsDeterministic(t *testing.T) {
	rows := []GroupHealthRow{
		{GroupID: 1, Name: "idle-a", State: GroupStateIdle},
		{GroupID: 2, Name: "healthy-a", State: GroupStateHealthy},
		{GroupID: 3, Name: "failing-b", State: GroupStateFailing,
			RelayLogFaultCounts: RelayLogFaultCounts{MemberFault: 3}},
		{GroupID: 4, Name: "failing-a", State: GroupStateFailing,
			RelayLogFaultCounts: RelayLogFaultCounts{MemberFault: 3}},
		{GroupID: 5, Name: "degraded-a", State: GroupStateDegraded,
			RelayLogFaultCounts: RelayLogFaultCounts{TransientFault: 1}},
		{GroupID: 6, Name: "no-member-a", State: GroupStateNoMember},
		{GroupID: 7, Name: "empty-a", State: GroupStateEmpty},
	}
	// 期望：failing 内部同失败数时按名字升序（failing-a 在前），
	// 状态顺序 failing < no_member < degraded < empty < idle < healthy。
	want := []string{"failing-a", "failing-b", "no-member-a", "degraded-a", "empty-a", "idle-a", "healthy-a"}

	for round := 0; round < 3; round++ {
		rotated := make([]GroupHealthRow, 0, len(rows))
		for index := range rows {
			rotated = append(rotated, rows[(index+round)%len(rows)])
		}
		sortGroupHealthRows(rotated)
		for index, row := range rotated {
			if row.Name != want[index] {
				t.Fatalf("第 %d 轮第 %d 位=%q，期望 %q（完整顺序 %v）",
					round, index, row.Name, want[index], rowNames(rotated))
			}
		}
	}
}

func rowNames(rows []GroupHealthRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Name)
	}
	return out
}

// 判据 7：日志里出现、配置里已经没有的分组要能被认出来。
// 不标出来的话，界面上会出现一行空名字的分组，用户不知道那是什么。
func TestGroupHealthMarksDeletedGroups(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	// 不放进缓存：模拟"分组已删、日志还在保留期"。
	seedGroupHealthLog(t, conn, groupHealthLogSeed{
		groupID: 9501, status: "failed", faultKind: "member", startedAt: base,
	})

	out, err := GroupHealthStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("GroupHealthStats: %v", err)
	}
	var found bool
	for _, row := range out.Groups {
		if row.GroupID != 9501 {
			continue
		}
		found = true
		if !row.Deleted {
			t.Errorf("已删除的分组必须标记 Deleted")
		}
		if row.Name != groupHealthDeletedName {
			t.Errorf("已删除的分组名=%q，期望 %q", row.Name, groupHealthDeletedName)
		}
		if row.MemberCount != 0 || row.AvailableCount != 0 {
			t.Errorf("已删除的分组没有成员事实，实际 %d/%d", row.AvailableCount, row.MemberCount)
		}
	}
	if !found {
		t.Fatalf("日志里的分组 9501 没有出现在结果中")
	}
}

// 判据 8：空窗口不报错、形状稳定（空切片而不是 nil），窗口账如实。
func TestGroupHealthEmptyWindowKeepsShape(t *testing.T) {
	withAnalyticsDB(t)

	out, err := GroupHealthStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("GroupHealthStats: %v", err)
	}
	if out.Groups == nil {
		t.Errorf("Groups 不能是 nil（序列化成 null 会让前端遍历崩）")
	}
	if out.Sample.Samples != 0 || out.Window != 0 {
		t.Errorf("空窗口应如实报告 0 样本，实际 window=%d samples=%d", out.Window, out.Sample.Samples)
	}
	if out.FailingThreshold != groupHealthFailingRate || out.DegradedThreshold != groupHealthDegradedRate {
		t.Errorf("判定线必须随结果返回：failing=%.1f degraded=%.1f",
			out.FailingThreshold, out.DegradedThreshold)
	}
}

// 判据 9：窗口账与其它画像同源——Window 等于参与统计的样本数，
// 且跳过测试请求的账要一起给出（"排除了几条"必须可见）。
func TestGroupHealthWindowMatchesSample(t *testing.T) {
	conn := withAnalyticsDB(t)
	base := analyticsBase()
	withGroupCache(t, model.Group{
		ID: 9601, Name: "gh-window", Mode: model.GroupModeFailover,
		Items: groupItemsWithAvailability(9601, 1, 1),
	})
	for index := 0; index < 5; index++ {
		seedGroupHealthLog(t, conn, groupHealthLogSeed{
			groupID: 9601, status: "success", startedAt: base.Add(time.Duration(index) * time.Second),
		})
	}
	if err := conn.Create(&model.RelayLog{
		GroupID: 9601, Status: "success", IsTest: true,
		StartedAt: base.Add(30 * time.Second),
	}).Error; err != nil {
		t.Fatalf("seed test log: %v", err)
	}

	out, err := GroupHealthStats(context.Background(), 500)
	if err != nil {
		t.Fatalf("GroupHealthStats: %v", err)
	}
	if out.Sample.Window != 6 || out.Sample.TestSkipped != 1 || out.Sample.Samples != 5 {
		t.Errorf("窗口账不对：window=%d skipped=%d samples=%d（期望 6/1/5）",
			out.Sample.Window, out.Sample.TestSkipped, out.Sample.Samples)
	}
	if out.Window != out.Sample.Samples {
		t.Errorf("Window（%d）必须等于参与统计的样本数（%d）", out.Window, out.Sample.Samples)
	}
	row := groupHealthRowOf(t, out, "gh-window")
	if row.Requests != 5 {
		t.Errorf("测试请求不该进分组账：请求数=%d，期望 5", row.Requests)
	}
}
