package relay

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-trace-003 终止原因记录的判据（吸收 new-api 的 PolicyDecision 设计）。
//
// ## 为什么需要这组用例
//
// 这个功能的**全部价值**在于"同一个失败终态能被区分为不同原因"。
// 如果实现只是写个常量、或者五个出口都写同一个值，功能就是零 ——
// 所以判据的核心是：**不同出口必须产出不同的记录**。
//
// 反面情形（必须有负向对照挡住）：
//   - 所有出口写同一个 reason（等于没记录）
//   - 后到的出口覆盖先到的（真实原因丢失）
//   - 空 reason 也写进去（产生"原因不明的终止"假记录）

// 核心判据：终止原因的结构化文本包含全部三个维度。
//
// 少任何一个都会让记录失去意义：没有 Reason 不知道哪条规则，
// 没有 Source 分不清该改设置还是改请求，没有 Action 与既有决策记录不同形。
func TestStopReasonTextCarriesAllDimensions(t *testing.T) {
	sr := StopReason{Action: stopAction, Reason: stopReasonBudget, Source: stopSourceConfig}
	got := sr.Text()

	for _, want := range []string{"action=stop", "reason=attempt_budget_exhausted", "source=config"} {
		if !strings.Contains(got, want) {
			t.Errorf("Text() = %q, 缺少 %q", got, want)
		}
	}
}

// 空原因不产生文本 —— 否则落库会出现"原因不明的终止"这种假记录。
func TestStopReasonTextEmptyWhenNoReason(t *testing.T) {
	if got := (StopReason{}).Text(); got != "" {
		t.Errorf("零值 Text() = %q, want 空串", got)
	}
}

// **本功能的核心**：四个失败出口必须互不相同。
//
// 这是"记录终止原因"这件事成立的前提 —— 如果两两相同，用户还是区分不出来，
// 那这个字段只是多占一列存储。
func TestStopReasonsArePairwiseDistinct(t *testing.T) {
	cases := map[string]struct{ reason, source string }{
		"预算用尽":  {stopReasonBudget, stopSourceConfig},
		"全体拒绝":  {stopReasonAllRejected, stopSourceUpstream},
		"快速失败":  {stopReasonFailFast, stopSourceConfig},
		"无可用成员": {stopReasonNoMember, stopSourceConfig},
	}

	seen := map[string]string{}
	for name, c := range cases {
		text := stopReasonText(c.reason, c.source)
		if prev, dup := seen[text]; dup {
			t.Errorf("「%s」与「%s」产生了相同的记录 %q —— 两者无法区分", name, prev, text)
		}
		seen[text] = name
	}
}

// 「全体拒绝」与「快速失败」是**同一个错误、不同来源**的典型：
// 两者 FaultKind 都是 request、HTTP 都是 502，只有 Source 能分开
// （前者改请求，后者改设置）。这条用例锁住这个区分。
func TestAllRejectedAndFailFastShareFaultKindButDifferInSource(t *testing.T) {
	rejected := stopReasonText(stopReasonAllRejected, stopSourceUpstream)
	failFast := stopReasonText(stopReasonFailFast, stopSourceConfig)

	if rejected == failFast {
		t.Fatalf("两者记录相同 %q，无法区分该改请求还是改设置", rejected)
	}
	if !strings.Contains(rejected, "source=upstream") {
		t.Errorf("全体拒绝的 source 应为 upstream，got %q", rejected)
	}
	if !strings.Contains(failFast, "source=config") {
		t.Errorf("快速失败的 source 应为 config，got %q", failFast)
	}
}

// 只覆盖不追加：一个请求只终止一次，先写的出口才是真实原因。
//
// 负向对照：若实现改成"后者覆盖前者"，重复调用两次后拿到的是伪造原因。
func TestMarkFailedKeepsFirstStopReason(t *testing.T) {
	openStopReasonTestDB(t)

	state := &RequestState{ID: 8901}
	state.markFailed(errTestCanceled{}, "", nil, stopReasonBudget, stopSourceConfig)
	first := state.StopReason

	// 模拟一个不该发生的二次定稿（如 defer 里的兜底出口）。
	state.markFailed(errTestCanceled{}, "", nil, stopReasonNoMember, stopSourceConfig)

	if state.StopReason != first {
		t.Fatalf("二次定稿覆盖了首次原因：%q -> %q", first, state.StopReason)
	}
	if !strings.Contains(state.StopReason, stopReasonBudget) {
		t.Errorf("保留的应是首次原因 budget，got %q", state.StopReason)
	}
}

// 出口没传 reason 时记 unrecorded，而不是留空。
//
// 留空会与"字段上线之前的历史行"混成一堆，谁也不知道新的空值意味着漏标；
// unrecorded 只有一个解释：某个调 markFailed 的出口忘了标。
func TestMarkFailedRecordsUnrecordedWhenReasonMissing(t *testing.T) {
	openStopReasonTestDB(t)

	state := &RequestState{ID: 8902}
	state.markFailed(errTestCanceled{}, "", nil, "", "")

	if !strings.Contains(state.StopReason, stopReasonUnrecorded) {
		t.Errorf("缺省原因应记为 %q，got %q", stopReasonUnrecorded, state.StopReason)
	}
	// source 也不能留空，否则文本里会出现 "source=" 这种残缺键值。
	if !strings.Contains(state.StopReason, "source="+stopSourceSystem) {
		t.Errorf("缺省 source 应回落到 system，got %q", state.StopReason)
	}
	if state.StopReason == "" {
		t.Fatal("StopReason 不能为空串 —— 那与历史空值无法区分")
	}
}

// 成功与取消路径各自的原因固定且互不相同 —— 这两条是最高频的终态，
// 写错会让绝大多数日志的终止原因失真。
//
// 这条用例**必须打真正的路径函数**，不能只比较两个常量：
// 常量之间天然不同，那样的用例在"路径函数写错 source"时照样绿
// （变异检查 M5 当时就没命中 —— recordStopReason 之外的路径函数没被覆盖）。
func TestSuccessAndCancelPathsWriteOwnReason(t *testing.T) {
	// markSucceeded/markCanceled 会走 finishLocked → 统计落库，需要真 DB。
	openStopReasonTestDB(t)

	success := &RequestState{}
	success.markSucceeded("body", nil)
	if !strings.Contains(success.StopReason, stopReasonCompleted) {
		t.Errorf("成功路径的 StopReason = %q, 应含 %q", success.StopReason, stopReasonCompleted)
	}
	if !strings.Contains(success.StopReason, "source=system") {
		t.Errorf("成功路径的 source 应为 system，got %q", success.StopReason)
	}

	canceled := &RequestState{}
	canceled.markCanceled(errTestCanceled{}, "body", nil)
	if !strings.Contains(canceled.StopReason, stopReasonClientCancel) {
		t.Errorf("取消路径的 StopReason = %q, 应含 %q", canceled.StopReason, stopReasonClientCancel)
	}
	// 这一条是关键：取消不属于任何一方的故障，source 必须是 client。
	// 若写成 system，界面就无法据此告诉用户"这条不用管"。
	if !strings.Contains(canceled.StopReason, "source=client") {
		t.Errorf("取消路径的 source 应为 client，got %q", canceled.StopReason)
	}

	if success.StopReason == canceled.StopReason {
		t.Errorf("成功与取消的终止原因相同 %q —— 两条最高频的终态无法区分", success.StopReason)
	}
}

// openStopReasonTestDB 提供本组用例所需的最小 DB。
//
// 成功/取消路径会触发统计落库，失败路径还要额外落 relay_logs 历史快照 ——
// 后者正是本组最关键的判据所在（终止原因必须"写进库里"而不仅是留在内存），
// 所以 RelayLog 必须一并迁移，否则查表会报表不存在。
// 与 route_cooldown_test.go 同一套装置口径：内存库 + 用完即换。
func openStopReasonTestDB(t *testing.T) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	conn, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(
		&model.StatsTotal{}, &model.StatsDaily{}, &model.StatsHourly{}, &model.StatsAPIKey{},
		&model.RelayLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))
}

// errTestCanceled 是取消路径用的最小 error 实现（markCanceled 会调 err.Error()）。
type errTestCanceled struct{}

func (errTestCanceled) Error() string { return "context canceled" }

// Attempts 要如实反映"试了几次才放弃" —— 它是判断"预算用尽"严重程度的唯一依据。
func TestStopReasonCarriesAttempts(t *testing.T) {
	openStopReasonTestDB(t)

	state := &RequestState{ID: 8903, Round: 7}
	state.markFailed(errTestCanceled{}, "", nil, stopReasonBudget, stopSourceConfig)

	if !strings.Contains(state.StopReason, stopReasonBudget) {
		t.Errorf("StopReason = %q, 应含 %q", state.StopReason, stopReasonBudget)
	}
	// 文本里不含 attempts，保持与 Decision.Text() 同形的三键格式
	// （尝试次数另有 relay_logs.attempts 列承载）。
	if strings.Contains(state.StopReason, "attempts=") {
		t.Errorf("Text() 不该包含 attempts（与 Decision.Text() 格式保持一致），got %q", state.StopReason)
	}
}

// **本轮 bug 的回归判据**：终止原因必须写进 relay_logs，而不只是留在内存。
//
// v0.61.0 的失败请求在生产上落库为空的根因是时序：finishLocked 内部就落库了，
// 而原因是在 markFailed 返回**之后**才由调用方补写 —— 写进内存的那一份
// 追不回已经落好的行。所以判据必须是**查表**，而不是断言 state.StopReason：
// 后者在两种实现下都非空（改前也一样），根本抓不住这个 bug。
//
// 负向对照：把原因写入移到 finishLocked 之后，本用例必须变红。
func TestMarkFailedPersistsStopReasonIntoLogRow(t *testing.T) {
	openStopReasonTestDB(t)

	state := &RequestState{ID: 8904, Round: 3}
	state.markFailed(errTestCanceled{}, "", nil, stopReasonAllRejected, stopSourceUpstream)

	var row model.RelayLog
	if err := db.GetDB().Where("request_id = ?", uint64(8904)).First(&row).Error; err != nil {
		t.Fatalf("落库的历史快照查不到：%v", err)
	}
	if row.StopReason == "" {
		t.Fatal("relay_logs.stop_reason 落库为空 —— 终止原因写在了落库之后（v0.61.0 时序 bug 复现）")
	}
	if !strings.Contains(row.StopReason, stopReasonAllRejected) {
		t.Errorf("落库的 stop_reason = %q, 应含 %q", row.StopReason, stopReasonAllRejected)
	}
	// source 必须一起落库：只落 reason 分不清该改请求还是改设置。
	if !strings.Contains(row.StopReason, "source="+stopSourceUpstream) {
		t.Errorf("落库的 stop_reason = %q, 应含 source=upstream", row.StopReason)
	}
}
