import io

p = "internal/server/handlers/usage_report.go"
s = io.open(p, encoding="utf-8").read()

old_imp = """import (
\t"net/http"
\t"time\""""

new_imp = """import (
\t"fmt"
\t"net/http"
\t"strconv"
\t"strings"
\t"time\""""
assert old_imp in s
s = s.replace(old_imp, new_imp, 1)

old_body = """// reportPeriod 解析请求里的周期，缺省按设置项当前值。
func reportPeriod(raw string) model.UsageReportPeriod {
\tif model.IsValidUsageReportPeriod(raw) {
\t\treturn model.UsageReportPeriod(raw)
\t}
\tif value, err := op.SettingGetString(model.SettingKeyUsageReportPeriod); err == nil && model.IsValidUsageReportPeriod(value) {
\t\treturn model.UsageReportPeriod(value)
\t}
\treturn model.UsageReportDaily
}

// previewUsageReport 只组装不发送：让用户在真正开启报告之前先看清会收到什么。
func previewUsageReport(c *gin.Context) {
\tvar body struct {
\t\tPeriod string `json:"period"`
\t}
\t_ = c.ShouldBindJSON(&body)
\treport, err := op.UsageReportPrepare(c.Request.Context(), reportPeriod(body.Period), timeNow())
\tif err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}
\tresp.Success(c, report)
}

// sendUsageReport 立即发送一份报告（面板上的"立即发送"）。
//
// 刻意不写周期记账（force）：否则用户点一下"试一下"就把当天的正式报告名额吃掉了，
// 第二天才发现日报没来。
func sendUsageReport(c *gin.Context) {
\tvar body struct {
\t\tPeriod string `json:"period"`
\t}
\t_ = c.ShouldBindJSON(&body)
\treport, results, err := task.UsageReportSendNow(c.Request.Context(), reportPeriod(body.Period), true)
\tif err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{"report": report, "results": results})
}

func usageReportHistory(c *gin.Context) {
\tresp.Success(c, op.UsageReportHistory(c.Request.Context(), 20))
}"""

new_body = """// parseReportPeriod 解析请求里的周期：传了非法值直接报错（防静默降级），未传时回退到设置项当前值。
func parseReportPeriod(raw string) (model.UsageReportPeriod, error) {
\ttrimmed := strings.TrimSpace(raw)
\tif trimmed != "" {
\t\tif !model.IsValidUsageReportPeriod(trimmed) {
\t\t\treturn "", fmt.Errorf("invalid period %q (want daily, weekly, or monthly)", trimmed)
\t\t}
\t\treturn model.UsageReportPeriod(trimmed), nil
\t}
\tif value, err := op.SettingGetString(model.SettingKeyUsageReportPeriod); err == nil && model.IsValidUsageReportPeriod(value) {
\t\treturn model.UsageReportPeriod(value), nil
\t}
\treturn model.UsageReportDaily, nil
}

// previewUsageReport 只组装不发送：让用户在真正开启报告之前先看清会收到什么。
func previewUsageReport(c *gin.Context) {
\tvar body struct {
\t\tPeriod string `json:"period"`
\t}
\tif c.Request.ContentLength > 0 {
\t\tif err := c.ShouldBindJSON(&body); err != nil {
\t\t\tresp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
\t\t\treturn
\t\t}
\t}
\tperiod, err := parseReportPeriod(body.Period)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}
\treport, err := op.UsageReportPrepare(c.Request.Context(), period, timeNow())
\tif err != nil {
\t\twriteOpError(c, err)
\t\treturn
\t}
\tresp.Success(c, report)
}

// sendUsageReport 立即发送一份报告（面板上的"立即发送"）。
//
// 刻意不写周期记账（force）：否则用户点一下"试一下"就把当天的正式报告名额吃掉了，
// 第二天才发现日报没来。
func sendUsageReport(c *gin.Context) {
\tvar body struct {
\t\tPeriod string `json:"period"`
\t}
\tif c.Request.ContentLength > 0 {
\t\tif err := c.ShouldBindJSON(&body); err != nil {
\t\t\tresp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
\t\t\treturn
\t}
\t}
\tperiod, err := parseReportPeriod(body.Period)
\tif err != nil {
\t\tresp.Error(c, http.StatusBadRequest, err.Error())
\t\treturn
\t}
\treport, results, err := task.UsageReportSendNow(c.Request.Context(), period, true)
\tif err != nil {
\t\tresp.Error(c, http.StatusInternalServerError, err.Error())
\t\treturn
\t}
\tresp.Success(c, gin.H{"report": report, "results": results})
}

func usageReportHistory(c *gin.Context) {
\tlimit := 20
\tif raw := strings.TrimSpace(c.Query("limit")); raw != "" {
\t\tparsed, err := strconv.Atoi(raw)
\t\tif err != nil || parsed <= 0 || parsed > 100 {
\t\t\tresp.Error(c, http.StatusBadRequest, "invalid limit (must be 1-100)")
\t\t\treturn
\t\t}
\t\tlimit = parsed
\t}
\tresp.Success(c, op.UsageReportHistory(c.Request.Context(), limit))
}"""

assert old_body in s
s = s.replace(old_body, new_body, 1)

io.open(p, "w", encoding="utf-8", newline="").write(s)
print("R40_APPLIED")
