package handlers

import (
	"net/http"
	"strconv"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 手动订阅（R-acct-004 / T-acct-005，管理面）: 上游没有余额接口时，人录一条套餐余额与有效期，
// 它按与自动读数同一口径折算后并入总余额（见 op.BalanceSummaryGet）。
//
// 与余额查询同一口径：整体挂在 middleware.Auth() 之内 —— 这里能列出"我在哪买了什么套餐"，
// 匿名可读等于把上游账号的采购与资产状况公开。
func init() {
	router.NewGroupRouter("/api/v1/subscription").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listManualSubscription),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createManualSubscription),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Handle(updateManualSubscription),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteManualSubscription),
		)
}

func listManualSubscription(c *gin.Context) {
	resp.Success(c, op.ManualSubscriptionList())
}

func createManualSubscription(c *gin.Context) {
	var req model.ManualSubscription
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	item, err := op.ManualSubscriptionCreate(c.Request.Context(), req)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, item)
}

func updateManualSubscription(c *gin.Context) {
	var req model.ManualSubscription
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	item, err := op.ManualSubscriptionUpdate(c.Request.Context(), req)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, item)
}

func deleteManualSubscription(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "id 非法")
		return
	}
	if err := op.ManualSubscriptionDelete(c.Request.Context(), id); err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, nil)
}
