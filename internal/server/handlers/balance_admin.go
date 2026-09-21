package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/task"
	"github.com/gin-gonic/gin"
)

// 总余额查询与人工录入（T-balance-001 / R-balance-002，管理面）:
// 面板首页与设置页读这一份快照。
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
		).
		AddRoute(
			// 人工录入某渠道的剩余额度：接口读不到的站点（实测 16 个里 14 个）唯一的兜底。
			router.NewRoute("/channel/:id", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(balanceChannelSet),
		).
		AddRoute(
			// 立刻跑一轮余额扫描：默认 5 分钟一轮，用户改完配置不想等。
			router.NewRoute("/scan", http.MethodPost).
				Handle(balanceScanNow),
		)
}

// balanceSummary 回完整快照: 总余额 + 逐渠道明细 + 逐 Key 额度。
// 每条未读到的渠道带上原因码与一句人话（ReasonCode/ReasonText），否则面板只能报一个数字。
func balanceSummary(c *gin.Context) {
	resp.Success(c, op.BalanceSummaryGet())
}

// balanceChannelSet 人工录入余额。points<=0 表示清除（回到自动读数/未知）。
type balanceChannelSetRequest struct {
	Points float64 `json:"points"`
	Note   string  `json:"note"`
}

func balanceChannelSet(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		resp.Error(c, http.StatusBadRequest, "invalid channel id")
		return
	}
	var body balanceChannelSetRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid body")
		return
	}
	if err := op.ChannelManualBalanceSet(id, body.Points, body.Note); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, op.BalanceSummaryGet())
}

// balanceScanNow 立刻跑一轮余额扫描（同步返回）。扫完的快照由调用方再取一次。
func balanceScanNow(c *gin.Context) {
	task.QuotaScanNow()
	resp.Success(c, op.BalanceSummaryGet())
}
