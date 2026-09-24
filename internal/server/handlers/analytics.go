package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// analyticsWindow 解析并夹取统计接口的 window 参数。
//
// window 用**条数**而不是天数：部署后流量差异极大，按天取会让
// "最近一天只有 3 条"的实例得出毫无意义的比例（口径同 fault-stats）。
//
// 上限 20000 是硬约束：统计要扫日志表，无界查询在日志量大时会拖慢管理面。
//
// 五个统计接口共用这一份，而不是各自抄一遍 —— 抄出来的副本会悄悄分叉
// （本项目已经见过"某处忘了夹上限"这类漂移）。解析失败时直接回 400 并返回 ok=false。
func analyticsWindow(c *gin.Context) (int, bool) {
	window := 500
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
			return 0, false
		}
		window = parsed
	}
	if window < 1 {
		window = 1
	}
	if window > 20000 {
		window = 20000
	}
	return window, true
}

// T-insight-002 模型调用分析总览接口。
//
// 回答"现在整体怎么样"——项目既有统计都是单一维度的（故障率按渠道、
// 耗时按分组、尝试链按轮次），没有一面全局的镜子：窗口内发了多少、花了多少、
// 健康线在哪、时间线上谁在吃 token。
func analyticsOverview(c *gin.Context) {
	window, ok := analyticsWindow(c)
	if !ok {
		return
	}

	stats, err := op.AnalyticsOverviewStats(c.Request.Context(), window)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, stats)
}

// latencyDistribution T-insight-004 全局延迟分布接口。
//
// 与 /overview 的分工：overview 回答"多少量、多少钱、谁在吃 token"，
// 这里回答"整体有多慢，慢是普遍的还是被少数拖累的"。
// 两者都挂在同一个 group 上、共用同一套 window 夹取口径。
func latencyDistribution(c *gin.Context) {
	window, ok := analyticsWindow(c)
	if !ok {
		return
	}

	stats, err := op.LatencyDistributionStats(c.Request.Context(), window)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, stats)
}

// routingProfile T-insight-007 选路判定与终止原因画像接口。
//
// 与 /overview、/latency 的分工：那两个回答"多少量、多少钱、有多慢"，
// 这里回答"**谁在做主**"与"**怎么停的**"——decision 与 stop_reason 两个字段
// 从落库那天起就没有任何统计读过它们，日志页只能一行一行看。
//
// 三个比例的分母各不相同（reasons/modes 用记录到的行数、tiers 用智能路由行数、
// stops 用终止原因已记录的行数），所以界面上任何一处都不能拿窗口总数当分母。
func routingProfile(c *gin.Context) {
	window, ok := analyticsWindow(c)
	if !ok {
		return
	}

	stats, err := op.RoutingProfileStats(c.Request.Context(), window)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, stats)
}

// groupHealth T-insight-008 分组健康画像接口。
//
// 与既有统计的分工：那四个都在回答「多快 / 多少 / 走了谁」，
// 这里回答「**这个分组还能不能用**」—— 成员全不可用、成功率崩掉、
// 失败集中在哪个上游。生产动机是验收时反复撞到的
// `53HK/Auto-Model 无可用成员`：这类失败没有 target_channel，
// 渠道故障统计看不见它，只有在分组维度才现形。
func groupHealth(c *gin.Context) {
	window, ok := analyticsWindow(c)
	if !ok {
		return
	}

	stats, err := op.GroupHealthStats(c.Request.Context(), window)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, stats)
}

func init() {
	router.NewGroupRouter("/api/v1/analytics").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/overview", http.MethodGet).
				Handle(analyticsOverview),
		).
		AddRoute(
			router.NewRoute("/latency", http.MethodGet).
				Handle(latencyDistribution),
		).
		AddRoute(
			router.NewRoute("/routing", http.MethodGet).
				Handle(routingProfile),
		).
		AddRoute(
			router.NewRoute("/group-health", http.MethodGet).
				Handle(groupHealth),
		)
}
