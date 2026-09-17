package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 总余额查询（T-balance-001，管理面）: 面板首页与设置页读这一份快照。
// 转发面的标准协议余额端点在同包的 balance.go（挂 /v1, 走 APIKeyAuth）。
//
// 必须挂在 middleware.Auth() 之内: 快照里有各渠道名称与余额, 与 /api/v1/stats 同级,
// 匿名可读等于把上游账号的资产状况公开（活体用例 B1 就是这条守卫）。
func init() {
	router.NewGroupRouter("/api/v1/balance").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/summary", http.MethodGet).
				Handle(balanceSummary),
		)
}

// balanceSummary 回完整快照: 总余额 + 逐渠道明细 + 逐 Key 额度。
func balanceSummary(c *gin.Context) {
	resp.Success(c, op.BalanceSummaryGet())
}
