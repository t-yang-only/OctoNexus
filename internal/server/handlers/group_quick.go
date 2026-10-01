package handlers

// 分组快速建立（需求1）：选渠道 + 模型（可指定凭据）一键建组。

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/op"
	"github.com/t-yang-only/OctoNexus/internal/server/middleware"
	"github.com/t-yang-only/OctoNexus/internal/server/resp"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
)

func init() {
	router.NewGroupRouter("/api/v1/group").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/quick-create", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(groupQuickCreate),
		)
}

// groupQuickCreate 一键建出 `渠道/模型` 分组。
//
// 前端复用分组编辑器已经在用的 channel/grants 缓存来选渠道与模型，
// 所以这里**不再单开一个 candidates 端点**：381 条授权一次请求 + react-query 缓存
// 就够，专门的按渠道端点要先增接口、再增一套前端请求，收益只是让单次响应小一点。
func groupQuickCreate(c *gin.Context) {
	var req op.QuickGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	result, err := op.GroupQuickCreate(c.Request.Context(), &req)
	if err != nil {
		// 这些错误都是"用户该改输入"型（渠道不存在/模型无可用凭据/同名分组成员不同），
		// 一律 400 并带上原文，不回 500 —— 500 会让人以为是服务端故障。
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"group":     result.Group,
		"grant_ids": result.GrantIDs,
		"key_names": result.KeyNames,
		"reused":    result.Reused,
	})
}
