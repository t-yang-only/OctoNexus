package handlers

// 统计接口 window 参数的入口夹取判据。
//
// # 为什么入口层也要有判据
//
// op 层的 relayLogWindow 已改成「0 = 显式不要样本」，但线上实测 window=0
// 仍返回 1 条样本 —— 因为入口的 analyticsWindow 写着 `if window < 1 { window = 1 }`，
// 在到达 op 层之前就把 0 夹成了 1。**两层夹取互相抵消**，只修一层不生效。
//
// 这就是本项目反复见的模式：同一个参数在入口与内核各夹一次，
// 改内核时忘了入口，行为没变而测试全绿（因为测的是内核那一层）。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newAnalyticsWindowCtx(t *testing.T, query string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/analytics/overview"+query, nil)
	return c
}

func TestAnalyticsWindowZeroPassesThrough(t *testing.T) {
	// window=0 必须**原样透传**给 op 层（它据此返回空窗口）。
	// 入口夹成 1 会让"关闭统计"变成"取 1 条样本"，调用方完全看不出。
	window, ok := analyticsWindow(newAnalyticsWindowCtx(t, "?window=0"))
	if !ok {
		t.Fatal("window=0 是合法输入，应解析成功")
	}
	if window != 0 {
		t.Errorf("window=0 应原样透传，实得 %d（入口把它夹掉了）", window)
	}
}

func TestAnalyticsWindowNegativeFallsBackToDefault(t *testing.T) {
	// 负数视为"没给"：回落默认 500。
	window, ok := analyticsWindow(newAnalyticsWindowCtx(t, "?window=-1"))
	if !ok {
		t.Fatal("window=-1 应解析成功")
	}
	if window != 500 {
		t.Errorf("window<0 应回落默认 500，实得 %d", window)
	}
}

func TestAnalyticsWindowClampsUpperBound(t *testing.T) {
	window, ok := analyticsWindow(newAnalyticsWindowCtx(t, "?window=999999"))
	if !ok {
		t.Fatal("超大窗口应被夹住而非报错")
	}
	if window != 20000 {
		t.Errorf("超上限应夹到 20000，实得 %d", window)
	}
}

func TestAnalyticsWindowDefaultsWhenAbsent(t *testing.T) {
	window, ok := analyticsWindow(newAnalyticsWindowCtx(t, ""))
	if !ok {
		t.Fatal("没传 window 应成功")
	}
	if window != 500 {
		t.Errorf("没传 window 应默认 500，实得 %d", window)
	}
}

func TestAnalyticsWindowRejectsGarbage(t *testing.T) {
	for _, q := range []string{"?window=abc", "?window=1.5", "?window="} {
		if q == "?window=" {
			// 空串按"没传"处理（TrimSpace 后为空）
			if w, ok := analyticsWindow(newAnalyticsWindowCtx(t, q)); !ok || w != 500 {
				t.Errorf("%q 应按没传处理（默认 500），实得 ok=%v w=%d", q, ok, w)
			}
			continue
		}
		c := newAnalyticsWindowCtx(t, q)
		if _, ok := analyticsWindow(c); ok {
			t.Errorf("%q 应被拒", q)
		}
		if c.Writer.Status() != http.StatusBadRequest {
			t.Errorf("%q 应回 400，实得 %d", q, c.Writer.Status())
		}
	}
}

// 常规窗口不受边界处理影响（回归守卫）。
func TestAnalyticsWindowNormalValuesPassThrough(t *testing.T) {
	for _, want := range []int{1, 2, 10, 100, 500, 1000} {
		window, ok := analyticsWindow(newAnalyticsWindowCtx(t, "?window="+itoa(want)))
		if !ok {
			t.Fatalf("window=%d 应解析成功", want)
		}
		if window != want {
			t.Errorf("window=%d 应原样通过，实得 %d", want, window)
		}
	}
}

// 所有带 window 的统计接口必须共用同一份夹取口径。
//
// 这条判据存在的理由：analyticsWindow 的注释里写着"抄出来的副本会悄悄分叉
// （本项目已经见过）"，而现场真的出现了两次 —— relay_stats.go 与 attempt_stats.go
// 各抄了一份含 `if window < 1 { window = 1 }` 的解析，于是 op 层与 analyticsWindow
// 都改成「0 = 显式不要样本」后，这两个接口仍把 0 夹成 1。
//
// 逐个 handler 打真实请求，而不是只测 analyticsWindow 本身 ——
// 层与层之间的抵消，按层写的单测看不见（本项目已为此多修了一个版本）。
func TestAllWindowedStatsHandlersShareClamp(t *testing.T) {
	gin.SetMode(gin.TestMode)

	handlers := map[string]gin.HandlerFunc{
		"/api/v1/analytics/overview":     analyticsOverview,
		"/api/v1/analytics/latency":      latencyDistribution,
		"/api/v1/analytics/routing":      routingProfile,
		"/api/v1/analytics/group-health": groupHealth,
		"/api/v1/log/fault-stats":        relayFaultStats,
		"/api/v1/log/attempt-stats":      attemptChainStats,
	}

	// 先造几条日志：否则库里空时 window=0 与 window=1 都返回 0 条样本，
	// 样本说明都是 0 —— "入口把 0 夹成 1"这个缺陷根本显不出来。
	// （实测：没有这段种子数据时，把 handler 改回私有副本，判据照样绿。）
	seedWindowStatsLog(t)

	for path, handler := range handlers {
		t.Run(path, func(t *testing.T) {
			c, rec := newWindowedHandlerRequest(t, path+"?window=0")
			handler(c)

			if rec.Code != http.StatusOK {
				t.Fatalf("window=0 应正常返回，实得 HTTP %d：%s", rec.Code, rec.Body.String())
			}
			// 样本说明里的 window 必须是 0（这个接口自己没把 0 夹成别的值）。
			got := sampleWindowOf(t, rec.Body.Bytes())
			if got != 0 {
				t.Errorf("window=0 应返回空窗口，实得 %d（这个接口自己夹掉了 0）", got)
			}
		})
	}
}

// seedWindowStatsLog 造一条成功日志，让"窗口夹成 1"能取到数据。
func seedWindowStatsLog(t *testing.T) {
	t.Helper()
	conn, err := gorm.Open(sqlite.Open("file:windowedstats?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if err := conn.AutoMigrate(&model.RelayLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := conn.Create(&model.RelayLog{
		RequestID: 90001, Status: "success", Model: "m",
		StartedAt: time.Now(), DurationMs: 1000,
		PromptTokens: 10, CompletionToks: 5,
	}).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
	t.Cleanup(db.SetDBForTest(conn))
}

// newWindowedHandlerRequest 造一个只带 query 的 gin 上下文（统计 handler 只读 query）。
func newWindowedHandlerRequest(t *testing.T, url string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, url, nil)
	return c, rec
}

// sampleWindowOf 从响应里取样本窗口条数，兼容两种外层形状
// （{data:{sample:{window}}} 与 {data:{window}}）。
func sampleWindowOf(t *testing.T, body []byte) int64 {
	t.Helper()
	var payload struct {
		Data struct {
			Window int64 `json:"window"`
			Sample struct {
				Window    int64 `json:"window"`
				Samples   int64 `json:"samples"`
				Truncated bool  `json:"truncated"`
			} `json:"sample"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if payload.Data.Sample.Window != 0 || payload.Data.Sample.Samples != 0 {
		return payload.Data.Sample.Window
	}
	return payload.Data.Window
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf []byte
	for v > 0 {
		buf = append([]byte{byte('0' + v%10)}, buf...)
		v /= 10
	}
	return string(buf)
}
