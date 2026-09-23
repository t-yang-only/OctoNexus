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
	window := 500
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
			return
		}
		window = parsed
	}
	if window < 1 {
		window = 1
	}
	if window > 20000 {
		window = 20000
	}

	stats, err := op.AttemptChainStats(c.Request.Context(), window)
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
