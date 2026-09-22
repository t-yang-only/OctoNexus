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

// 模型名智能重写（吸收上游 lingyuins/octopus 与 New-API 的共同做法，管理面）。
//
// 整组挂在 middleware.Auth() 之内：规则表能反推出"这台机器对外暴露了哪些模型名、
// 它们被指向哪个分组"，等于把内部拓扑交出去，匿名可读不合适。
func init() {
	router.NewGroupRouter("/api/v1/model-mapping").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).
				Handle(listModelMapping),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createModelMapping),
		).
		AddRoute(
			router.NewRoute("/update/:id", http.MethodPost).
				Handle(updateModelMapping),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteModelMapping),
		).
		AddRoute(
			router.NewRoute("/toggle/:id", http.MethodPost).
				Handle(toggleModelMapping),
		).
		AddRoute(
			router.NewRoute("/test", http.MethodPost).
				Handle(testModelMapping),
		).
		AddRoute(
			router.NewRoute("/dry-run", http.MethodPost).
				Handle(dryRunModelMapping),
		)
}

func listModelMapping(c *gin.Context) {
	resp.Success(c, op.ModelMappingList())
}

func createModelMapping(c *gin.Context) {
	var req model.ModelMappingCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	item, err := op.ModelMappingCreate(c.Request.Context(), &req)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, item)
}

func updateModelMapping(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid id")
		return
	}
	var req model.ModelMappingUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	item, err := op.ModelMappingUpdate(c.Request.Context(), id, &req)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, item)
}

func deleteModelMapping(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid id")
		return
	}
	if err := op.ModelMappingDelete(c.Request.Context(), id); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, "deleted")
}

func toggleModelMapping(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	item, err := op.ModelMappingToggle(c.Request.Context(), id, body.Enabled)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, item)
}

// testModelMapping 用当前生效的规则集重写一个模型名（面板上的"试一下"）。
func testModelMapping(c *gin.Context) {
	var req model.ModelMappingTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, op.ModelMappingTest(req.ModelName))
}

// dryRunModelMapping 在保存前试跑一条还没落库的规则，用来回答"这条规则会不会误伤"。
func dryRunModelMapping(c *gin.Context) {
	var req struct {
		MatchType model.ModelMatchType `json:"match_type" binding:"required"`
		Pattern   string               `json:"pattern" binding:"required"`
		ModelName string               `json:"model_name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	matched, err := op.ModelMappingDryRun(req.MatchType, req.Pattern, req.ModelName)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"matched": matched})
}
