package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/op"
	"github.com/t-yang-only/OctoNexus/internal/server/middleware"
	"github.com/t-yang-only/OctoNexus/internal/server/resp"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
)

// T-trace-002 尝试链聚合接口。
//
// 与 /log/fault-stats 的分工（**两者刻意不同，都要有**）：
//
//	fault-stats  按**最终结果**归因：成功率 / 渠道健康度
//	attempt-stats 按**每一次尝试**归因：谁在被反复试错
//
// 关键差异：一次**成功**的请求里 A 失败、B 接手成功 ——
// fault-stats 看不到 A 的失败（那条日志 status=success），本接口能看到。
// 所以 affected_requests 通常**大于** fault-stats 的失败数，
// 两者之差就是「试错但最终成功」的请求量。
func attemptChainStats(c *gin.Context) {
	// window 解析复用 analyticsWindow，**不在这里抄一份**：
	// 早先这里确实抄了一份（含 `if window < 1 { window = 1 }`），
	// 于是 op 层与 analyticsWindow 都改成「0 = 显式不要样本」后，
	// 这个接口仍会把 0 夹成 1 —— 与 relay_stats.go 踩的是同一个坑。
	window, ok := analyticsWindow(c)
	if !ok {
		return
	}

	// 分类维度从 query 取，非法值回 400 而不是静默按渠道聚合 ——
	// 静默兜底会让用户以为界面上选的维度生效了，实际拿到的是另一个维度的数。
	groupBy, ok := op.AttemptGroupByValid(c.Query("group_by"))
	if !ok {
		resp.Error(c, http.StatusBadRequest, "invalid group_by (want channel/model/apikey)")
		return
	}

	stats, err := op.AttemptChainStats(c.Request.Context(), window, groupBy)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, stats)
}

func init() {
	router.NewGroupRouter("/api/v1/log").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/attempt-stats", http.MethodGet).
				Handle(attemptChainStats),
		)
}
