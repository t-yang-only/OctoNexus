package handlers

// 日期范围解析的判据（需求4）。
//
// # 这个功能要防住的事
//
//  1. **RFC3339 的精度被静默抹掉** —— 用户传 2026-09-29T14:30:00Z 想查 14:30 之后，
//     若实现把任何形状都截断到当天 00:00，他实际查到的是 00:00 之后。
//     支持精确时刻却又把它抹掉，比一开始不支持更糟：用户以为筛对了。
//  2. **YYYY-MM-DD 的半开区间** —— "查 9月24日"必须不含 9月25日 00:00 之后的数据，
//     否则按天查询会多出第二天的量，而用户无从察觉（数字看着就是"这一天的"）。
//  3. **非法值必须 400** —— 静默忽略会让用户以为筛选生效、实际看到全部，
//     那是最难发现的一类错：没有报错，只是数不对。

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// newDateRangeCtx 造一个只带 query 的 gin 上下文。
func newDateRangeCtx(t *testing.T, query string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/log/history"+query, nil)
	c.Request = req
	return c
}

func TestParseDateRangeDayIsHalfOpen(t *testing.T) {
	c := newDateRangeCtx(t, "?from=2026-09-24&to=2026-09-24")
	from, to, ok := parseDateRangeQuery(c)
	if !ok {
		t.Fatal("合法日期应解析成功")
	}
	if from == nil || to == nil {
		t.Fatal("from/to 都应被赋值")
	}
	// from = 当天 00:00（本地时区）
	wantFrom := time.Date(2026, 9, 24, 0, 0, 0, 0, time.Local)
	if !from.Equal(wantFrom) {
		t.Errorf("from 应为当天 00:00，实得 %v", from)
	}
	// to = 次日 00:00（半开区间，不含 9月25日 00:00 之后）
	wantTo := time.Date(2026, 9, 25, 0, 0, 0, 0, time.Local)
	if !to.Equal(wantTo) {
		t.Errorf("to 应为次日 00:00，实得 %v", to)
	}
}

// RFC3339 必须按精确时刻采用，不截断到当天 00:00。
func TestParseDateRangeRFC3339KeepsPrecision(t *testing.T) {
	c := newDateRangeCtx(t, "?from=2026-09-24T14:30:00Z")
	from, _, ok := parseDateRangeQuery(c)
	if !ok {
		t.Fatal("合法 RFC3339 应解析成功")
	}
	want := time.Date(2026, 9, 24, 14, 30, 0, 0, time.UTC)
	if !from.Equal(want) {
		t.Errorf("RFC3339 应原样采用，实得 %v（want %v）", from, want)
	}
	// 负向对照：若实现截断到当天 00:00，这里会是 0 点。
	if from.Hour() != 14 || from.Minute() != 30 {
		t.Errorf("时刻必须保留 14:30，实得 %02d:%02d", from.Hour(), from.Minute())
	}
}

// to 传 RFC3339 同样不能截断 —— 否则上界被推到次日，多算一天。
func TestParseDateRangeRFC3339ToKeepsPrecision(t *testing.T) {
	c := newDateRangeCtx(t, "?to=2026-09-24T14:30:00Z")
	_, to, ok := parseDateRangeQuery(c)
	if !ok {
		t.Fatal("合法 RFC3339 应解析成功")
	}
	want := time.Date(2026, 9, 24, 14, 30, 0, 0, time.UTC)
	if !to.Equal(want) {
		t.Errorf("to 的 RFC3339 应原样采用，实得 %v", to)
	}
}

// 非法值必须 400，不能静默忽略。
func TestParseDateRangeRejectsGarbage(t *testing.T) {
	cases := []string{
		"?from=not-a-date",
		"?to=2026-13-45",   // 月份/日期越界
		"?from=24/09/2026", // 顺序不符
		"?to=2026-9-4",     // 未补零（Go 的 2006-01-02 不认）
		"?from=20260924",   // 紧凑格式
	}
	for _, q := range cases {
		t.Run(q, func(t *testing.T) {
			c := newDateRangeCtx(t, q)
			_, _, ok := parseDateRangeQuery(c)
			if ok {
				t.Fatalf("%q 应被拒", q)
			}
			if c.Writer.Status() != http.StatusBadRequest {
				t.Errorf("%q 应回 400，实得 %d", q, c.Writer.Status())
			}
		})
	}
}

// 空值与缺失是两回事：都没传 = 不限（两个 nil），传了空串也按不限。
func TestParseDateRangeEmptyMeansUnbounded(t *testing.T) {
	c := newDateRangeCtx(t, "")
	from, to, ok := parseDateRangeQuery(c)
	if !ok {
		t.Fatal("没传日期应成功（不限）")
	}
	if from != nil || to != nil {
		t.Errorf("没传日期应两个都为 nil，实得 from=%v to=%v", from, to)
	}

	c = newDateRangeCtx(t, "?from=&to=")
	from, to, ok = parseDateRangeQuery(c)
	if !ok {
		t.Fatal("传空串应成功（不限）")
	}
	if from != nil || to != nil {
		t.Errorf("空串应按不限处理，实得 from=%v to=%v", from, to)
	}
}

// 只传一端时另一端必须保持不限（nil），不能默认成某个值。
func TestParseDateRangeSingleSided(t *testing.T) {
	c := newDateRangeCtx(t, "?from=2026-09-24")
	from, to, ok := parseDateRangeQuery(c)
	if !ok {
		t.Fatal("只传 from 应成功")
	}
	if from == nil {
		t.Error("from 应被赋值")
	}
	if to != nil {
		t.Errorf("只传 from 时 to 应保持 nil（不限上界），实得 %v", to)
	}

	c = newDateRangeCtx(t, "?to=2026-09-24")
	from, to, ok = parseDateRangeQuery(c)
	if !ok {
		t.Fatal("只传 to 应成功")
	}
	if to == nil {
		t.Error("to 应被赋值")
	}
	if from != nil {
		t.Errorf("只传 to 时 from 应保持 nil（不限下界），实得 %v", from)
	}
}
