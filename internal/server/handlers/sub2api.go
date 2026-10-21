package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/op"
	"github.com/t-yang-only/OctoNexus/internal/server/middleware"
	"github.com/t-yang-only/OctoNexus/internal/server/resp"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
)

func init() {
	router.NewGroupRouter("/api/v1/sub2api").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/accounts", http.MethodGet).
				Handle(sub2apiAccountList),
		).
		AddRoute(
			router.NewRoute("/accounts", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(sub2apiAccountCreate),
		).
		AddRoute(
			router.NewRoute("/accounts/:id", http.MethodPut).
				Use(middleware.RequireJSON()).
				Handle(sub2apiAccountUpdate),
		).
		AddRoute(
			router.NewRoute("/accounts/:id", http.MethodDelete).
				Handle(sub2apiAccountDelete),
		).
		AddRoute(
			router.NewRoute("/accounts/:id/probe", http.MethodPost).
				Handle(sub2apiAccountProbe),
		)
}

func sub2apiAccountList(c *gin.Context) {
	list, err := op.Sub2APIAccountList(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, list)
}

func sub2apiAccountCreate(c *gin.Context) {
	var req op.Sub2APIAccountCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, "请求参数非法: "+err.Error())
		return
	}
	acct, err := op.Sub2APIAccountCreate(c.Request.Context(), req)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, acct)
}

func sub2apiAccountDelete(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "非法账号 ID")
		return
	}
	if err := op.Sub2APIAccountDelete(c.Request.Context(), id); err != nil {
		if errors.Is(err, op.ErrSub2APIAccountNotFound) {
			resp.Error(c, http.StatusNotFound, "账号不存在")
			return
		}
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"id": id, "deleted": true})
}

func sub2apiAccountProbe(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "非法账号 ID")
		return
	}
	status, err := op.Sub2APIAccountProbe(c.Request.Context(), id)
	if err != nil {
		resp.Error(c, http.StatusBadGateway, "探测失败: "+err.Error())
		return
	}
	resp.Success(c, gin.H{"id": id, "status": status})
}

func sub2apiAccountUpdate(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "非法账号 ID")
		return
	}
	var req op.Sub2APIAccountUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, "请求参数非法: "+err.Error())
		return
	}
	acct, err := op.Sub2APIAccountUpdate(c.Request.Context(), id, req)
	if err != nil {
		if errors.Is(err, op.ErrSub2APIAccountNotFound) {
			resp.Error(c, http.StatusNotFound, "账号不存在")
			return
		}
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, acct)
}
