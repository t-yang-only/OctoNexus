package relay

import (
	"fmt"
	"testing"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/glebarez/sqlite"
	"github.com/looplj/axonhub/llm"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// T-insight-001 日志画像（思考强度 / 思考 token）的落库判据。
//
// ## 为什么判据要打在"落库值"上
//
// 这两个字段的全部价值是**事后解释一条请求为什么慢、为什么贵**。
// 如果只写进内存而没进库，历史日志页看到的永远是空 —— 功能等于不存在。
// 所以判据一律查 relay_logs 的实际列，不断言内存字段。
//
// ## 必须挡住的三种错法（负向对照）
//
//	上游没报思考 token 时 panic（details 为 nil 的路径在真实站点上最常见）
//	上游没报时凭空填一个数（把"没报"显示成"有思考"）
//	思考强度写死或丢失（它来自请求侧，与 usage 无关，容易被顺手清掉）

// 思考 token 与思考强度都要落到 relay_logs 的对应列上。
func TestInsightReasoningPersistedIntoLogRow(t *testing.T) {
	openInsightTestDB(t)

	state := &RequestState{ID: 9101, Round: 1, ReasoningEffort: "xhigh"}
	usage := &llm.Usage{
		CompletionTokens:        100,
		CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 46},
	}
	state.markSucceeded("body", usage)

	row := loadInsightRow(t, 9101)
	if row.ReasoningTokens != 46 {
		t.Errorf("ReasoningTokens = %d, want 46（上游回报的思考 token 必须原样进库）", row.ReasoningTokens)
	}
	if row.ReasoningEffort != "xhigh" {
		t.Errorf("ReasoningEffort = %q, want %q（它来自请求侧，与 usage 无关）", row.ReasoningEffort, "xhigh")
	}
}

// 上游不给 completion_tokens_details 时必须安全地记 0，且不能 panic。
//
// 这条路径在真实站点上比"有值"更常见（非推理模型、以及不实现该字段的站点），
// 因此它是本功能能不能长期挂在线上的前提，而不是边角情况。
func TestInsightReasoningTokensZeroWhenUpstreamSilent(t *testing.T) {
	openInsightTestDB(t)

	state := &RequestState{ID: 9102, Round: 1}
	// 只有基础用量，没有 details —— 绝大多数响应长这样。
	state.markSucceeded("body", &llm.Usage{CompletionTokens: 12})

	row := loadInsightRow(t, 9102)
	if row.ReasoningTokens != 0 {
		t.Errorf("ReasoningTokens = %d, want 0（上游没报就是 0，不能凭空造值）", row.ReasoningTokens)
	}
	if row.ReasoningEffort != "" {
		t.Errorf("ReasoningEffort = %q, want 空串（客户端没指定时不填默认值）", row.ReasoningEffort)
	}
}

// 失败请求同样要带上这两个字段 —— 排查"这条为什么又失败又慢"时，
// 失败行和成功行看到的信息量不该有差别。
func TestInsightReasoningPersistedOnFailurePath(t *testing.T) {
	openInsightTestDB(t)

	state := &RequestState{ID: 9103, Round: 2, ReasoningEffort: "low"}
	state.markFailed(errTestCanceled{}, "", &llm.Usage{
		CompletionTokens:        5,
		CompletionTokensDetails: &llm.CompletionTokensDetails{ReasoningTokens: 7},
	}, stopReasonBudget, stopSourceConfig)

	row := loadInsightRow(t, 9103)
	if row.ReasoningTokens != 7 {
		t.Errorf("失败行 ReasoningTokens = %d, want 7", row.ReasoningTokens)
	}
	if row.ReasoningEffort != "low" {
		t.Errorf("失败行 ReasoningEffort = %q, want %q", row.ReasoningEffort, "low")
	}
}

func loadInsightRow(t *testing.T, requestID uint64) model.RelayLog {
	t.Helper()
	var row model.RelayLog
	if err := db.GetDB().Where("request_id = ?", requestID).First(&row).Error; err != nil {
		t.Fatalf("落库的历史快照查不到（request_id=%d）：%v", requestID, err)
	}
	return row
}

// openInsightTestDB 提供本组用例所需的最小 DB（终态落库会写统计表与 relay_logs）。
func openInsightTestDB(t *testing.T) {
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
