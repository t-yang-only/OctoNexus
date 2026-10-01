package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/op"
	"github.com/t-yang-only/OctoNexus/internal/server/middleware"
	"github.com/t-yang-only/OctoNexus/internal/server/resp"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
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
//
// window 解析复用 analyticsWindow，**不在这里抄一份**：
// 早先这里确实抄了一份，于是 op 层与 analyticsWindow 都改成「0 = 显式不要样本」之后，
// 这个接口仍在把 0 夹成 1 —— 线上实测 window=0 返回 sample.window=1、截断=true。
// analytics.go 的注释里写着"抄出来的副本会悄悄分叉（本项目已经见过）"，这就是现场。
func relayFaultStats(c *gin.Context) {
	window, ok := analyticsWindow(c)
	if !ok {
		return
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
