package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/t-yang-only/OctoNexus/internal/op"
)

// T-reject-001「已鉴权的请求级拒绝必须留痕」的判据。
//
// # 这个功能的全部价值
//
// 用户被拒绝之后**能不能自己查明白为什么**。判据因此必须打在「库里的行」上：
// 只断言响应的用例会放过最典型的漏法 —— 响应完全正确，而库里什么都没有
// （这正是本功能要修的那个线上实测现象：测试 Key 调白名单外的模型，
// 客户端拿到 400，日志页一条记录都没有）。
//
// # 必须挡住的错法（每条都有对应用例）
//
//	拒绝没落库                 → 功能等于没做
//	归因记成 transient         → 把"请求写错了"记成渠道故障，污染分组通过率
//	终止原因与来源记错          → 界面上给出的处置建议指错方向
//	顺手改了客户端可见行为      → 客户端拿到的状态码或文案变了（本次改动不该动它）
//	测试标记丢失               → 拒绝行混进画像样本

// 白名单外的模型：客户端拿到 400，且库里必须有一条归因为 request 的失败行。
func TestRejectTrackedPersistsKeyScopeRejection(t *testing.T) {
	openInsightTestDB(t)
	gin.SetMode(gin.TestMode)

	c, rec := newRelayTestContext(t, `{"model":"not-allowed","messages":[{"role":"user","content":"hi"}]}`, "")
	c.Set("supported_models", []string{"allowed-a", "allowed-b"})
	c.Set("api_key_id", 0)

	Forward(llm.APIFormatOpenAIChatCompletion)(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400（白名单拒绝是请求侧问题）", rec.Code)
	}
	// 错误文案必须**以原话开头**并带上允许清单。
	//
	// 早先这里断言"逐字不变"，但那正是缺陷本身：只说"不支持"而不说"支持哪些"，
	// 客户端拿到 400 也不知道该把 model 改成什么（线上实测有 3 条这样的失败，
	// 用户唯一的办法是去面板翻这把 Key 的设置）。
	// 现在保留原话作前缀（既有客户端若按它匹配不至于全崩），追加 allowed models 清单。
	if msg := errorMessageOf(t, rec); !strings.HasPrefix(msg, "model not supported by this api key") {
		t.Errorf("错误文案 = %q, want 以 model not supported by this api key 开头", msg)
	} else if !strings.Contains(msg, "allowed-a") || !strings.Contains(msg, "allowed-b") {
		t.Errorf("错误文案 = %q, 应带上这把 Key 允许的模型清单（否则客户端不知道该改成什么）", msg)
	}

	row := loadRejectRow(t, "not-allowed")
	if row.Status != "failed" {
		t.Errorf("Status = %q, want failed", row.Status)
	}
	if row.FaultKind != "request" {
		t.Errorf("FaultKind = %q, want request —— "+
			"归因错成 transient 会把一次「Key 的模型范围不含它」记成渠道瞬时故障，污染分组通过率", row.FaultKind)
	}
	if want := "action=stop;reason=api_key_scope_rejected;source=config"; row.StopReason != want {
		t.Errorf("StopReason = %q, want %q", row.StopReason, want)
	}
	if row.Attempts != 0 {
		t.Errorf("Attempts = %d, want 0（拒绝发生在选路之前，一次上游尝试都没发生）", row.Attempts)
	}
	if row.FirstByteMs != -1 {
		t.Errorf("FirstByteMs = %d, want -1（没走到上游，没有首字节）", row.FirstByteMs)
	}
}

// 分组不存在：同样是请求侧问题，归因必须走 request、来源必须是 client。
func TestRejectTrackedPersistsModelNotFound(t *testing.T) {
	openInsightTestDB(t)
	gin.SetMode(gin.TestMode)

	// 不设 supported_models -> 不做白名单检查，直接走到查分组那一步。
	c, rec := newRelayTestContext(t, `{"model":"no-such-group-xyz","messages":[]}`, "")
	c.Set("api_key_id", 0)

	Forward(llm.APIFormatOpenAIChatCompletion)(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400", rec.Code)
	}
	if msg := errorMessageOf(t, rec); !strings.Contains(msg, "model not found") {
		t.Errorf("错误文案应保留 model not found 原话，实得 %q", msg)
	}

	row := loadRejectRow(t, "no-such-group-xyz")
	if row.FaultKind != "request" {
		t.Errorf("FaultKind = %q, want request", row.FaultKind)
	}
	if want := "action=stop;reason=model_not_found;source=client"; row.StopReason != want {
		t.Errorf("StopReason = %q, want %q", row.StopReason, want)
	}
}

// 客户端可见行为必须与改动前**逐字一致** —— 本次改动只增加落库，不动客户端看到的东西。
//
// 这条单独成立：把 rejectRequest 换成 rejectRequestTracked 时，
// 顺手改动状态码或错误类型（例如统一成 502）会让客户端把请求侧问题当服务端故障重试。
func TestRejectTrackedKeepsClientVisibleBehaviour(t *testing.T) {
	openInsightTestDB(t)
	gin.SetMode(gin.TestMode)

	c, rec := newRelayTestContext(t, `{"model":"not-allowed","messages":[]}`, "")
	c.Set("supported_models", []string{"allowed-a"})
	c.Set("api_key_id", 0)

	Forward(llm.APIFormatOpenAIChatCompletion)(c)

	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是预期的错误体: %v（原文：%s）", err, rec.Body.String())
	}
	if envelope.Error.Type != "invalid_request_error" {
		t.Errorf("error.type = %q, want invalid_request_error（与旧出口同款，不能变成 upstream_error）",
			envelope.Error.Type)
	}
}

// 声明为验证请求的调用被拒时同样要带标记，否则"哪几条是我自己发的"又答不上来。
func TestRejectTrackedCarriesTestFlag(t *testing.T) {
	openInsightTestDB(t)
	gin.SetMode(gin.TestMode)

	c, _ := newRelayTestContext(t, `{"model":"not-allowed","messages":[]}`, "true")
	c.Set("supported_models", []string{"allowed-a"})
	c.Set("api_key_id", 0)

	Forward(llm.APIFormatOpenAIChatCompletion)(c)

	if row := loadRejectRow(t, "not-allowed"); !row.IsTest {
		t.Errorf("IsTest = false, want true —— 拒绝行也必须能被排除出画像样本")
	}
}

// 负向对照：裸 errors.New 确实会被归到 transient。
//
// 没有这条，上面两条 FaultKind == "request" 的断言无法证明自己不是恒真的：
// 若 newUpstreamStatusError 哪天被换回裸 fmt.Errorf/errors.New，
// 归因会静默退回 transient，而单看"有落库"是看不出来的。
func TestBareErrorWouldBeTransient(t *testing.T) {
	bare := faultKindOf(plainError("model not supported by this api key"))
	if bare != "transient" {
		t.Fatalf("faultKindOf(裸 error) = %q, want transient —— "+
			"这条对照失效说明归类逻辑变了，请复核上面两条用例的 FaultKind 断言", bare)
	}
	if got := faultKindOf(newUpstreamStatusError(http.StatusBadRequest, "x")); got != "request" {
		t.Fatalf("faultKindOf(带 400 的 error) = %q, want request", got)
	}
}

// systemone 路径的三处选路前拒绝同样必须留痕 —— 它不走 RequestState，用自己的记账函数。
func TestSystemOneRejectionsPersist(t *testing.T) {
	openInsightTestDB(t)
	gin.SetMode(gin.TestMode)

	t.Run("白名单拒绝", func(t *testing.T) {
		c, rec := newRelayTestContext(t, `{"model":"not-allowed","state":{}}`, "")
		c.Set("supported_models", []string{"allowed-a"})

		ForwardSystemOne()(c)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d, want 400", rec.Code)
		}
		row := loadRejectRow(t, "not-allowed")
		if row.FaultKind != "request" {
			t.Errorf("FaultKind = %q, want request", row.FaultKind)
		}
		if want := "action=stop;reason=api_key_scope_rejected;source=config"; row.StopReason != want {
			t.Errorf("StopReason = %q, want %q", row.StopReason, want)
		}
		if row.Status != "failed" {
			t.Errorf("Status = %q, want failed", row.Status)
		}
	})

	t.Run("分组不存在", func(t *testing.T) {
		c, rec := newRelayTestContext(t, `{"model":"no-such-group-xyz","state":{}}`, "")

		ForwardSystemOne()(c)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("状态码 = %d, want 400", rec.Code)
		}
		row := loadRejectRow(t, "no-such-group-xyz")
		if want := "action=stop;reason=model_not_found;source=client"; row.StopReason != want {
			t.Errorf("StopReason = %q, want %q", row.StopReason, want)
		}
	})

	// model 缺失也是选路前的拒绝，但它属于"扫描流量"那一类，本轮**有意不落库**
	// （避免任何人往 /v1/systemone 乱发就灌爆日志）。这条用例把该决定钉住：
	// 若哪天有人给它也加上记账，这里会红，提醒他先想清楚样本污染问题。
	t.Run("model 缺失仍不落库", func(t *testing.T) {
		c, _ := newRelayTestContext(t, `{"state":{}}`, "")

		ForwardSystemOne()(c)

		var count int64
		if err := db.GetDB().Model(&model.RelayLog{}).
			Where("model = ''").Count(&count).Error; err != nil {
			t.Fatalf("count: %v", err)
		}
		if count != 0 {
			t.Errorf("model 缺失的请求落库了 %d 行；这类拒绝有意不留痕（防扫描流量灌日志）", count)
		}
	})

	// 测试标记在 systemone 路径同样要透传。
	// 这条是变异检查补出来的：把 systemone 的 isTestRequest(c) 改成 false，
	// 标准协议那条用例（TestRejectTrackedCarriesTestFlag）照样绿 —— 它测的是另一条路径。
	t.Run("测试标记透传", func(t *testing.T) {
		// 用独立的模型名：同名行会在同一张表里共存，按名取第一行会拿到**别的子用例**写的那条
		// （实测踩到：这条断言先拿到的是上一个子用例写的 IsTest=false 那行）。
		c, _ := newRelayTestContext(t, `{"model":"not-allowed-test-flag","state":{}}`, "1")
		c.Set("supported_models", []string{"allowed-a"})

		ForwardSystemOne()(c)

		if row := loadRejectRow(t, "not-allowed-test-flag"); !row.IsTest {
			t.Errorf("IsTest = false, want true（systemone 路径的拒绝行也要能被排除出画像样本）")
		}
	})
}

// 模型名重写命中、但重写后的目标分组不存在：走的是**第二个**拒绝点。
//
// 源码里"原名查不到"与"重写后仍查不到"是两段独立代码，只测第一个点
// 无法证明第二个点也接了线（变异检查 M10 的锚点命中两次，暴露了这一点）。
//
// 另一条同样是要点：记录里存的必须是**客户端请求的名字**，不是重写后的目标名 ——
// 用户按自己填的模型名才查得到这次拒绝。
func TestRejectTrackedPersistsMappingMissTarget(t *testing.T) {
	conn := openRouteCooldownTestDB(t) // 顺带带来全套表与 op.InitCache
	// 该装置的建表清单里没有 relay_logs（它当时验收的是冷却持久化），
	// 而本用例要断言的就是"拒绝写进了 relay_logs"，缺表会把判据变成"查不到表"。
	if err := conn.AutoMigrate(&model.ModelMapping{}, &model.RelayLog{}); err != nil {
		t.Fatalf("migrate model_mappings/relay_logs: %v", err)
	}
	if err := conn.Create(&model.ModelMapping{
		Name: "重写链路", Pattern: "legacy-ghost", MatchType: model.ModelMatchExact,
		TargetModel: "ghost-group-not-exist", Enabled: true,
	}).Error; err != nil {
		t.Fatalf("造重写规则: %v", err)
	}
	if err := op.InitCache(); err != nil {
		t.Fatalf("重建缓存（让重写规则进内存）: %v", err)
	}

	gin.SetMode(gin.TestMode)
	c, rec := newRelayTestContext(t, `{"model":"legacy-ghost","state":{}}`, "")

	ForwardSystemOne()(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, want 400（重写命中但目标不存在仍是请求侧问题）", rec.Code)
	}
	row := loadRejectRow(t, "legacy-ghost")
	if want := "action=stop;reason=model_not_found;source=client"; row.StopReason != want {
		t.Errorf("StopReason = %q, want %q", row.StopReason, want)
	}
}

// ---- 装置 ----

// newRelayTestContext 造一个最小可用的转发入口上下文。
func newRelayTestContext(t *testing.T, body string, testHeader string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if testHeader != "" {
		req.Header.Set(RelayTestHeader, testHeader)
	}
	c.Request = req
	return c, rec
}

// loadRejectRow 按请求的模型名取出那条拒绝记录。
// 用模型名而不是 request_id 定位：这里的重点是"用户按模型名能查到这次拒绝"。
func loadRejectRow(t *testing.T, modelName string) model.RelayLog {
	t.Helper()
	var row model.RelayLog
	if err := db.GetDB().Where("model = ?", modelName).First(&row).Error; err != nil {
		t.Fatalf("被拒绝的请求必须留下记录（model=%q 查不到）：%v\n"+
			"这正是 T-reject-001 要修的现象：客户端拿到 400，日志页什么都没有", modelName, err)
	}
	return row
}

// errorMessageOf 取出错误响应里的 message（三家协议的响应形状在这里是同一个）。
func errorMessageOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("响应不是预期的错误体: %v（原文：%s）", err, rec.Body.String())
	}
	return envelope.Error.Message
}

// plainError 是一个不带状态码的普通错误，用于负向对照。
type plainError string

func (e plainError) Error() string { return string(e) }
