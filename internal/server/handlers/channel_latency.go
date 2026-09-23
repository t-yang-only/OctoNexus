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

// T-perf-001 渠道延迟画像接口。
//
// 实测发现 17 个渠道的上游 TLS 握手从 1ms 到 1177ms，差三个数量级。
// 而慢的上游会拖慢每一次转发 —— 用户在客户端只感觉"这个模型怎么这么慢"，
// 看不出是渠道的问题，更不知道该换哪个用。
//
// relay_logs 里早就有 FirstByteMs / DurationMs，这个接口把它变成可比较的画像。
func channelLatency(c *gin.Context) {
	window := 500
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
			return
		}
		window = parsed
	}
	stats, err := op.ChannelLatencyStats(c.Request.Context(), window)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, stats)
}

func init() {
	router.NewGroupRouter("/api/v1/channel").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/latency", http.MethodGet).
				Handle(channelLatency),
		)
}
