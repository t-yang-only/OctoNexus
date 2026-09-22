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

// T-usability-008 真实通过率接口。
//
// 回答两个不同的问题（刻意分成两个数字，只给一个必然误导一半场景）：
//
//	success_rate  用户发起的请求有多少成功了 —— 体验视角
//	channel_rate  这个渠道本身健康吗         —— 诊断视角（排除了「请求本身非法」）
//
// 实测动机：senseaudio 的 success_rate 只有 31.6%，但它 channel_rate 接近 100% ——
// 那 25 次失败全是「用 chat 接口调 TTS/图像模型」造成的请求非法，
// 跟渠道没关系。只给一个数，用户就会去修一个没坏的东西。
//
// window 用**条数**而不是天数：部署后流量差异极大，
// 按天取会让「最近一天只有 3 条」的渠道得出毫无意义的比例。
func relayFaultStats(c *gin.Context) {
	window := 500
	if raw := strings.TrimSpace(c.Query("window")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
			return
		}
		window = parsed
	}
	// 夹住上限：统计要扫日志表，无界查询在日志量大时会拖慢管理面。
	if window < 1 {
		window = 1
	}
	if window > 20000 {
		window = 20000
	}

	stats, err := op.RelayLogFaultsStats(c.Request.Context(), window)
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
			router.NewRoute("/fault-stats", http.MethodGet).
				Handle(relayFaultStats),
		)
}
