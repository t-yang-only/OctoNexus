package handlers

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

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
			router.NewRoute("/summary", http.MethodGet).
				Handle(poolSummary),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Handle(poolExport),
		).
		AddRoute(
			router.NewRoute("/kinds/:kind/sync", http.MethodPost).
				Handle(poolSyncKind),
		).
		AddRoute(
			router.NewRoute("/kinds/:kind", http.MethodGet).
				Handle(poolKind),
		).
		AddRoute(
			router.NewRoute("/openapi.json", http.MethodGet).
				Handle(poolOpenAPI),
		).
		AddRoute(
			router.NewRoute("/entries/batch", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(poolBatch),
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
	// scanned 与 total 都回：调用方一眼看出"是过滤掉了"还是"后端本来就空"。
	resp.Success(c, gin.H{
		"items":    result.Items,
		"total":    result.Total,
		"returned": result.Returned,
		"scanned":  result.Scanned,
		"kind":     filter.Kind,
		"warnings": result.Warnings,
	})
}

// poolSummary 返回汇总视图：总数/启用/健康/带错/临期/过期 + 分后端/分服务商/分状态。
//
// 与 /stats 的区别：stats 只数个数；summary 按状态与到期窗口切分，并显式列出取不到数据的后端，
// 让"池子是空的"与"某个后端坏了"可以区分开。
func poolSummary(c *gin.Context) {
	resp.Success(c, pool.SummaryOf(c.Request.Context(), time.Now()))
}

// poolExport 导出统一视图（不含任何凭据字段）。
//
// format=json（默认）给外部工具直接吃；format=csv 给人/表格软件看。
// 筛选参数与 /pool/entries 完全同一套（同一个 pool.ParseFilter），面板才能"按当前筛选导出"。
// 注意导出不分页：limit/offset 会被忽略，导出就是把当前筛选结果整个拿走。
func poolExport(c *gin.Context) {
	filter, err := pool.ParseFilter(queryParams(c))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	rows, failures, err := pool.ExportRows(c.Request.Context(), filter, time.Now())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.EqualFold(c.Query("format"), "csv") {
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", "attachment; filename=pool-entries.csv")
		// 带 BOM：CSV 这条路的用户是"人/表格软件"，少了 BOM 中文名字在 Windows 表格软件里是乱码。
		_, _ = c.Writer.WriteString("\ufeff")
		// CSV 里没有地方放"哪个后端这次没取到数据"，用响应头给外部工具一个机器可读的信号。
		if len(failures) > 0 {
			kinds := make([]string, 0, len(failures))
			for _, failure := range failures {
				kinds = append(kinds, failure.Kind)
			}
			c.Header("X-Pool-Error-Kinds", strings.Join(kinds, ","))
		}
		writer := csv.NewWriter(c.Writer)
		_ = writer.Write([]string{"kind", "id", "name", "provider", "status", "enabled", "healthy", "plan_tier", "expires_at", "last_error"})
		for _, row := range rows {
			_ = writer.Write([]string{
				row.Kind, row.ID, row.Name, row.Provider, row.Status,
				strconv.FormatBool(row.Enabled), strconv.FormatBool(row.Healthy),
				row.PlanTier, row.ExpiresAt, row.LastError,
			})
		}
		writer.Flush()
		return
	}
	resp.Success(c, gin.H{
		"items":    rows,
		"total":    len(rows),
		"warnings": failures,
	})
}

// poolStats 返回号池聚合计数（总数/启用/健康/分后端）。
func poolStats(c *gin.Context) {
	resp.Success(c, pool.Snapshot(c.Request.Context()))
}

// poolAPIVersion 是号池接口面自己的版本号（能力位/路由变化时递增）。
const poolAPIVersion = "1.0.0"

// queryParams 把查询参数压成 map 交给 pool.ParseFilter（参数越加越多，不再用长参数列表）。
// 同名参数取第一个：接口语义是"标量参数"，取第一个比取最后一个更符合直觉。
func queryParams(c *gin.Context) map[string]string {
	params := map[string]string{}
	for key, values := range c.Request.URL.Query() {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}
	return params
}

// poolKind 取单个号池后端的自描述；未注册回 404（与其它未知 kind 一致）。
func poolKind(c *gin.Context) {
	kind := c.Param("kind")
	for _, info := range pool.Kinds() {
		if info.Kind == kind {
			resp.Success(c, info)
			return
		}
	}
	resp.Error(c, http.StatusNotFound, "unknown pool kind: "+kind)
}

// poolBatch 批量动作：逐条独立，一条失败不影响其余。
//
// 需要 ids 或 filter+max —— 不允许"不写条件就全量打一遍"：那是运维事故而不是功能。
func poolBatch(c *gin.Context) {
	var body struct {
		Action string            `json:"action" binding:"required"`
		Kind   string            `json:"kind"`
		IDs    []string          `json:"ids"`
		Filter map[string]string `json:"filter"`
		Max    int               `json:"max"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	request := pool.BatchRequest{Action: body.Action, Kind: body.Kind, IDs: body.IDs, Max: body.Max}
	if len(body.Filter) > 0 {
		filter, err := pool.ParseFilter(body.Filter)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		// 批量按条件取目标时必须显式给 kind 或让 filter 自己带 kind，避免误伤整个号池。
		if filter.Kind == "" {
			filter.Kind = body.Kind
		}
		request.Filter = &filter
	}
	result, err := pool.Batch(c.Request.Context(), request, time.Now())
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, result)
}

// poolOpenAPI 返回号池接口的 OpenAPI 文档（由注册表推导：注册了新后端，文档自动跟着长）。
func poolOpenAPI(c *gin.Context) {
	resp.Success(c, pool.OpenAPIDocument(poolAPIVersion))
}
