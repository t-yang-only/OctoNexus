package handlers

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/op"
	"github.com/t-yang-only/OctoNexus/internal/server/middleware"
	"github.com/t-yang-only/OctoNexus/internal/server/resp"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
)

// 上游价格与用量快照（T-price-001 / T-price-002，管理面）。
//
// 与 /api/v1/balance 同级的敏感面：快照里有各站余额、用量与成本，
// 匿名可读等于把上游账号的资产状况公开，所以必须挂 middleware.Auth()。
//
// 抓取端（脚本 / 采集凭据 / 浏览器）把站点返回原样 POST 到 /import，
// 本模块只负责入库与按"对比"口径汇总——抓取方式可以换，入库口径不变。
func init() {
	router.NewGroupRouter("/api/v1/price").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			// 导入一次抓取结果：body 就是站点的原始快照 {site, captured_at, plaza, usage_stats, me}。
			router.NewRoute("/import", http.MethodPost).
				Handle(priceImport),
		).
		AddRoute(
			// 价格对比：同一模型在各站的实付价并排（?model= 可过滤）。
			router.NewRoute("/models", http.MethodGet).
				Handle(priceModels),
		).
		AddRoute(
			// 用量对比：各站 token 统计 + 异常判定。
			router.NewRoute("/usage", http.MethodGet).
				Handle(priceUsage),
		)
}

func priceImport(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 8<<20))
	if err != nil || len(body) == 0 {
		resp.Error(c, http.StatusBadRequest, "读取请求体失败或为空")
		return
	}
	prices, usages, err := op.PriceSnapshotImport(c.Request.Context(), body)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"price_rows": prices, "usage_rows": usages})
}

func priceModels(c *gin.Context) {
	// 排序参数（需求9「每个参数都支持从大到小排序」）：
	//   sort  = 对外列名，取值见 op.priceSortColumns 白名单；
	//   order = asc | desc，缺省 desc（用户要的就是"从大到小"）。
	// 非法列名由 op 层回落到默认序，这里不报错 —— 旧链接带过来的参数不该让整页打不开。
	rows, err := op.PriceModelList(c.Request.Context(), c.Query("model"), op.PriceModelSort{
		Column:     c.Query("sort"),
		Descending: c.Query("order") != "asc",
	})
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows, "total": len(rows)})
}

func priceUsage(c *gin.Context) {
	rows, err := op.PriceUsageCompare(c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": rows, "total": len(rows)})
}
