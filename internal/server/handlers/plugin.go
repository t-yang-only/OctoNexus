package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/plugin"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// R-plugin-001 社区反代扩展插件：社区玩家自制的反代工具由 octopus 托管运行，出网经节点池出口。
//
// 为什么开放这一层：账号池形态千差万别（官方网页版、第三方中转、自建站），主仓不可能为每一家
// 写适配器。把"把某个账号池变成 OpenAI 兼容接口"这件事交给社区，octopus 只负责环境（端口 + 进程托管）、
// 全局隐秘代理（出口注入）与接进主链路（自动注册成渠道 ⇒ 分压/监控/选路/余额全部复用）。
//
// 安全口径：
//   - 全部路由挂 middleware.Auth()（管理面）；转发口不暴露插件管理。
//   - 出口未就绪时**拒绝启动**（绝不让插件以为已经隐藏出口、实际用真实 IP 出去）。
//   - 插件令牌（自动注册渠道的凭据）只经环境变量注入插件进程，任何响应都不回显。
func init() {
	router.NewGroupRouter("/api/v1/plugin").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/list", http.MethodGet).Handle(pluginList),
		).
		AddRoute(
			router.NewRoute("/scan", http.MethodPost).Handle(pluginScan),
		).
		AddRoute(
			router.NewRoute("/:slug", http.MethodPut).
				Use(middleware.RequireJSON()).
				Handle(pluginUpdate),
		).
		AddRoute(
			router.NewRoute("/:slug", http.MethodDelete).Handle(pluginRemove),
		).
		AddRoute(
			router.NewRoute("/:slug/start", http.MethodPost).Handle(pluginStart),
		).
		AddRoute(
			router.NewRoute("/:slug/stop", http.MethodPost).Handle(pluginStop),
		).
		AddRoute(
			router.NewRoute("/:slug", http.MethodGet).Handle(pluginStatus),
		)
}

func pluginList(c *gin.Context) {
	items, err := plugin.Statuses(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": items, "total": len(items), "dir": plugin.Dir()})
}

// pluginScan 重新扫描插件目录：玩家把工具拷进目录后点一下就能接进来（不必重启实例）。
func pluginScan(c *gin.Context) {
	items, failures, err := plugin.Scan(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	statuses := make([]model.PluginStatus, 0, len(items))
	for _, row := range items {
		status, statusErr := plugin.Status(c.Request.Context(), row.Slug)
		if statusErr != nil {
			continue
		}
		statuses = append(statuses, status)
	}
	warnings := map[string]string{}
	for slug, message := range failures {
		warnings[slug] = message
	}
	resp.Success(c, gin.H{"items": statuses, "total": len(statuses), "failures": warnings, "dir": plugin.Dir()})
}

func pluginStart(c *gin.Context) {
	status, err := plugin.Start(c.Request.Context(), c.Param("slug"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": status})
}

func pluginStop(c *gin.Context) {
	if err := plugin.Stop(c.Request.Context(), c.Param("slug")); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	status, err := plugin.Status(c.Request.Context(), c.Param("slug"))
	if err != nil {
		resp.Success(c, gin.H{})
		return
	}
	resp.Success(c, gin.H{"item": status})
}

func pluginStatus(c *gin.Context) {
	status, err := plugin.Status(c.Request.Context(), c.Param("slug"))
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": status})
}

func pluginUpdate(c *gin.Context) {
	var in model.PluginUpdateRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	row, err := plugin.Update(c.Request.Context(), c.Param("slug"), in)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	status, err := plugin.Status(c.Request.Context(), row.Slug)
	if err != nil {
		resp.Success(c, gin.H{"item": row})
		return
	}
	resp.Success(c, gin.H{"item": status})
}

func pluginRemove(c *gin.Context) {
	if err := plugin.Remove(c.Request.Context(), c.Param("slug")); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{})
}
