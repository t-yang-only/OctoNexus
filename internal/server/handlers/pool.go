package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/pool"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// R-pool-ext-001 号池扩展层的第一批接口（只读）。
//
// 设计取向：接口按"统一视图 + 适配器自描述"给，而不是按具体后端给。
// 外部反代工具包接进来之后，最需要的是先能问清楚两件事——
//  1. 有哪些号池后端、各自能做什么（GET /api/v1/pool/kinds）；
//  2. 池子里现在有什么、状态如何（GET /api/v1/pool/entries、/stats）。
//
// 这三条接口不含任何凭据字段（适配器自描述里 secret 字段只会出现在 kinds 的字段说明中，
// 用来告诉工具"这个字段是凭据、别指望接口回显"，entries 永远只回元数据）。
func init() {
	router.NewGroupRouter("/api/v1/pool").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/kinds", http.MethodGet).
				Handle(poolKinds),
		).
		AddRoute(
			router.NewRoute("/entries", http.MethodGet).
				Handle(poolEntries),
		).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Handle(poolStats),
		).
		AddRoute(
			router.NewRoute("/kinds/:kind/sync", http.MethodPost).
				Handle(poolSyncKind),
		).
		AddRoute(
			router.NewRoute("/entries/:kind/:id", http.MethodGet).
				Handle(poolGetEntry),
		).
		AddRoute(
			router.NewRoute("/entries/:kind/:id/probe", http.MethodPost).
				Handle(poolProbeEntry),
		).
		AddRoute(
			router.NewRoute("/entries/:kind/:id/refresh", http.MethodPost).
				Handle(poolRefreshEntry),
		).
		AddRoute(
			router.NewRoute("/entries/:kind/:id/enable", http.MethodPost).
				Handle(poolEnableEntry),
		).
		AddRoute(
			router.NewRoute("/entries/:kind/:id/disable", http.MethodPost).
				Handle(poolDisableEntry),
		)
}

// poolKinds 返回已注册的号池后端与它们的能力位、字段说明。
func poolKinds(c *gin.Context) {
	resp.Success(c, gin.H{
		"items": pool.Kinds(),
		"total": len(pool.Kinds()),
	})
}

// poolEntries 返回统一号池视图；?kind= 指定某一种后端，留空为全部。
//
// 单个后端取不到数据时不算失败：错误进 warnings[], 其余照回，接口仍 200——
// 号池是排查现场的地方，一个坏后端把整张表变成 500 只会让人更难定位。
func poolEntries(c *gin.Context) {
	kind := c.Query("kind")
	entries, failures, err := pool.Entries(c.Request.Context(), kind)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if entries == nil {
		entries = []pool.Entry{}
	}
	warnings := make([]pool.KindError, 0, len(failures))
	warnings = append(warnings, failures...)
	resp.Success(c, gin.H{
		"items":    entries,
		"total":    len(entries),
		"kind":     kind,
		"warnings": warnings,
	})
}

// poolStats 返回号池聚合计数（总数/启用/健康/分后端）。
func poolStats(c *gin.Context) {
	resp.Success(c, pool.Snapshot(c.Request.Context()))
}
