package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 分压与限流监控（T-monitor-001，管理面）。
//
// 用户口径："给前端页面升级更加详细的监控页面"。这一份快照就是面板那一页的数据源:
// 每个成员此刻的剩余请求数（估算）、分压权重与健康系数、冷却/限流状态、最近一分钟的
// 请求与 token 消耗。全部由选路层自己算出来（与真正选路同一函数）, 面板只展示、不重复折算。
//
// 必须挂在 middleware.Auth() 之内: 明细含渠道名/模型名/凭据名, 与 /api/v1/stats、
// /api/v1/balance/summary 同级, 匿名可读等于把上游账号的资产与限流状况公开。
func init() {
	router.NewGroupRouter("/api/v1/monitor").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/allocation", http.MethodGet).
				Handle(allocationMonitor),
		)
}

// allocationMonitor 回一次分压与限流快照（只读, 不改变任何选路状态）。
func allocationMonitor(c *gin.Context) {
	resp.Success(c, relay.AllocationMonitor(relay.AllocationNowMs()))
}
