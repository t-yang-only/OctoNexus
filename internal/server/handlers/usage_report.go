package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/t-yang-only/OctoNexus/internal/op"
	"github.com/t-yang-only/OctoNexus/internal/server/middleware"
	"github.com/t-yang-only/OctoNexus/internal/server/resp"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
	"github.com/t-yang-only/OctoNexus/internal/task"
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

// parseReportPeriod 解析请求里的周期：传了非法值直接报错（防静默降级），未传时回退到设置项当前值。
func parseReportPeriod(raw string) (model.UsageReportPeriod, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed != "" {
		if !model.IsValidUsageReportPeriod(trimmed) {
			return "", fmt.Errorf("invalid period %q (want daily, weekly, or monthly)", trimmed)
		}
		return model.UsageReportPeriod(trimmed), nil
	}
	if value, err := op.SettingGetString(model.SettingKeyUsageReportPeriod); err == nil && model.IsValidUsageReportPeriod(value) {
		return model.UsageReportPeriod(value), nil
	}
	return model.UsageReportDaily, nil
}

// previewUsageReport 只组装不发送：让用户在真正开启报告之前先看清会收到什么。
func previewUsageReport(c *gin.Context) {
	var body struct {
		Period string `json:"period"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
			return
		}
	}
	period, err := parseReportPeriod(body.Period)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	report, err := op.UsageReportPrepare(c.Request.Context(), period, timeNow())
	if err != nil {
		writeOpError(c, err)
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
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
			return
		}
	}
	period, err := parseReportPeriod(body.Period)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	report, results, err := task.UsageReportSendNow(c.Request.Context(), period, true)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"report": report, "results": results})
}

func usageReportHistory(c *gin.Context) {
	limit := 20
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 || parsed > 100 {
			resp.Error(c, http.StatusBadRequest, "invalid limit (must be 1-100)")
			return
		}
		limit = parsed
	}
	resp.Success(c, op.UsageReportHistory(c.Request.Context(), limit))
}
