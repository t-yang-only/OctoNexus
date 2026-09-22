package handlers

import (
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/task"
	"github.com/gin-gonic/gin"
)

func timeNow() time.Time { return time.Now() }

// 用量报告（吸收上游 lingyuins/octopus 的 Usage Reports，管理面）。
//
// 整组挂在 middleware.Auth() 之内：报告里含费用、模型清单与渠道名，
// 等于把"这台机器在用什么、花了多少"公开，匿名可读不合适。
func init() {
	router.NewGroupRouter("/api/v1/usage-report").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/preview", http.MethodPost).
				Handle(previewUsageReport),
		).
		AddRoute(
			router.NewRoute("/send", http.MethodPost).
				Handle(sendUsageReport),
		).
		AddRoute(
			router.NewRoute("/history", http.MethodGet).
				Handle(usageReportHistory),
		)
}

// reportPeriod 解析请求里的周期，缺省按设置项当前值。
func reportPeriod(raw string) model.UsageReportPeriod {
	if model.IsValidUsageReportPeriod(raw) {
		return model.UsageReportPeriod(raw)
	}
	if value, err := op.SettingGetString(model.SettingKeyUsageReportPeriod); err == nil && model.IsValidUsageReportPeriod(value) {
		return model.UsageReportPeriod(value)
	}
	return model.UsageReportDaily
}

// previewUsageReport 只组装不发送：让用户在真正开启报告之前先看清会收到什么。
func previewUsageReport(c *gin.Context) {
	var body struct {
		Period string `json:"period"`
	}
	_ = c.ShouldBindJSON(&body)
	report, err := op.UsageReportPrepare(c.Request.Context(), reportPeriod(body.Period), timeNow())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, report)
}

// sendUsageReport 立即发送一份报告（面板上的"立即发送"）。
//
// 刻意不写周期记账（force）：否则用户点一下"试一下"就把当天的正式报告名额吃掉了，
// 第二天才发现日报没来。
func sendUsageReport(c *gin.Context) {
	var body struct {
		Period string `json:"period"`
	}
	_ = c.ShouldBindJSON(&body)
	report, results, err := task.UsageReportSendNow(c.Request.Context(), reportPeriod(body.Period), true)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"report": report, "results": results})
}

func usageReportHistory(c *gin.Context) {
	resp.Success(c, op.UsageReportHistory(c.Request.Context(), 20))
}
