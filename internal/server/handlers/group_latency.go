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

// T-perf-003 分组维度的延迟画像接口。
//
// 用户调用的不是渠道而是分组（客户端填的是 Max-flash 这类名字），
// 而分组内部还要选路 —— 所以"这个分组多快"只能按分组统计。
func groupLatency(c *gin.Context) {
	window := 500
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
			return
		}
		window = parsed
	}
	stats, err := op.GroupLatencyStats(c.Request.Context(), window)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, stats)
}

func init() {
	router.NewGroupRouter("/api/v1/group").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/latency", http.MethodGet).
				Handle(groupLatency),
		)
}
