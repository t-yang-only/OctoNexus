package handlers

import (
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/pool"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// R-pool-ext-001 第三批 · 边界②：外部工具包的**只读 APIKey 通道**。
//
// 为什么单独开一条通道而不是让外部工具用管理面账号：外部工具包（面板、巡检脚本、账号 hub）大多
// 已经拿着一个转发用的 APIKey，让它们为此再存一套管理员口令，等于把管理面凭据散到更多地方。
// 这条通道挂在**转发端口**上、走 middleware.APIKeyAuth，且**只有只读接口**：
//
//	GET /v1/pool/kinds    有哪些号池后端、各自能做什么
//	GET /v1/pool/entries  统一视图（kind 过滤、筛选、排序与 /api/v1/pool/entries 同口径）
//	GET /v1/pool/stats    聚合计数
//	GET /v1/pool/summary  按状态/服务商/到期窗口切分
//
// 三条硬边界，刻意写死而不是"暂时这样"：
//   - **只读**：没有 probe/refresh/toggle/sync 这些会改外部系统或改本机状态的动词，
//     想动号池只能走管理面（那里的路由用 middleware.Auth()）。
//   - **凭据永不回显**：这里复用的是统一视图，Entry 只带元数据；kinds 里的 secret 标记只是
//     告诉工具"这个字段是凭据、别指望接口回显"。
//   - **不额外放大权限**：能调模型的 Key 就能读号池规模与状态，但读不到任何凭据、也改不了任何东西。
func init() {
	router.NewGroupRouter("/v1/pool").
		ServeOn(router.ServerRelay).
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/kinds", http.MethodGet).
				Handle(poolAPIKeyKinds),
		).
		AddRoute(
			router.NewRoute("/entries", http.MethodGet).
				Handle(poolAPIKeyEntries),
		).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Handle(poolAPIKeyStats),
		).
		AddRoute(
			router.NewRoute("/summary", http.MethodGet).
				Handle(poolAPIKeySummary),
		)
}

// poolAPIKeyKinds 与 /api/v1/pool/kinds 同源：外部工具靠它自发现"这个号池能做什么"。
func poolAPIKeyKinds(c *gin.Context) {
	kinds := pool.Kinds()
	resp.Success(c, gin.H{"items": kinds, "total": len(kinds), "channel": "api_key", "read_only": true})
}

// poolAPIKeyEntries 与 /api/v1/pool/entries 同源（同一份筛选与排序口径）。
func poolAPIKeyEntries(c *gin.Context) {
	filter, err := pool.ParseFilter(queryParams(c))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := pool.List(c.Request.Context(), filter, time.Now())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if result.Items == nil {
		result.Items = []pool.Entry{}
	}
	resp.Success(c, gin.H{
		"items":               result.Items,
		"total":               result.Total,
		"returned":            result.Returned,
		"scanned":             result.Scanned,
		"kind":                filter.Kind,
		"warnings":            result.Warnings,
		"channel":             "api_key",
		"read_only":           true,
		"capabilities_notice": "此通道只读；probe/refresh/toggle/sync 请走管理面接口。",
	})
}

// poolAPIKeyStats 与 /api/v1/pool/stats 同源。
func poolAPIKeyStats(c *gin.Context) {
	resp.Success(c, gin.H{
		"stats":     pool.Snapshot(c.Request.Context()),
		"channel":   "api_key",
		"read_only": true,
	})
}

// poolAPIKeySummary 与 /api/v1/pool/summary 同源：按状态/服务商/到期窗口切分。
func poolAPIKeySummary(c *gin.Context) {
	resp.Success(c, gin.H{
		"summary":   pool.SummaryOf(c.Request.Context(), time.Now()),
		"channel":   "api_key",
		"read_only": true,
	})
}
