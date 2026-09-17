package handlers

import (
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// 标准协议余额查询（T-balance-001）: 挂在转发端口 /v1 下, 走与转发同一套 APIKeyAuth,
// 因此「用哪把钥匙调用模型, 就用哪把钥匙查余额」, 不需要额外的管理凭据。
//
// 端点形状对齐 one-api / new-api 的 OpenAI 计费口径（ChatGPT-Next-Web 一类客户端据此显示余额）:
//   - GET /v1/dashboard/billing/subscription
//   - GET /v1/dashboard/billing/usage
//
// 另加本项目自有的 GET /v1/balance: 一次拿全总余额 + 逐渠道明细 + 本 Key 额度。
func init() {
	router.NewGroupRouter("/v1").
		ServeOn(router.ServerRelay).
		Use(middleware.APIKeyAuth()).
		AddRoute(
			router.NewRoute("/dashboard/billing/subscription", http.MethodGet).
				Handle(billingSubscription),
		).
		AddRoute(
			router.NewRoute("/dashboard/billing/usage", http.MethodGet).
				Handle(billingUsage),
		).
		AddRoute(
			router.NewRoute("/balance", http.MethodGet).
				Handle(balanceDetail),
		)
}

// billingSubscription 按 OpenAI 计费口径回「还剩多少」。
//
// 客户端普遍按 hard_limit_usd - total_usage/100 显示余额, 因此这里把**总余额**放进 hard_limit_usd、
// 把本 Key 已花放进 usage（见 billingUsage）, 两者相减正好是总余额; 软限与系统硬限同值,
// 免得客户端把「还有钱」显示成「已用尽」。
func billingSubscription(c *gin.Context) {
	summary := op.BalanceSummaryGet()
	key := op.APIKeyBalanceGet(callingAPIKey(c))
	limit := summary.Total + key.Used
	resp.Success(c, gin.H{
		"object":                  "billing_subscription",
		"has_payment_method":      true,
		"soft_limit_usd":          limit,
		"hard_limit_usd":          limit,
		"system_hard_limit_usd":   limit,
		"access_until":            time.Now().AddDate(1, 0, 0).Unix(),
		"currency":                summary.Currency,
		"points_per_unit":         summary.PointsPerUnit,
		"total_available":         summary.Total,
		"total_monthly_remaining": summary.TotalMonthlyRemaining,
		"known_channels":          summary.KnownChannels,
		"unknown_channels":        summary.UnknownChannels,
		"key_used_usd":            key.Used,
		"key_remaining_usd":       key.Remaining,
		"key_unlimited":           key.Unlimited,
	})
}

// billingUsage 回本 Key 的已用量。标准协议里 total_usage 以「分」计, 故乘 100;
// start_date/end_date 只接受不解释: 本项目的用量统计只有累计口径, 假装支持区间过滤
// 会给出一个比真实值更可信的错数。
func billingUsage(c *gin.Context) {
	key := op.APIKeyBalanceGet(callingAPIKey(c))
	resp.Success(c, gin.H{
		"object":          "list",
		"total_usage":     key.Used * 100,
		"total_usage_usd": key.Used,
		"requests":        key.Requests,
		"tokens":          key.Tokens,
	})
}

// balanceDetail 回一次完整快照: 总余额 + 逐渠道（含剩余次数与凭据数）+ 本 Key 的额度。
// 只回调用方自己的 Key: 同一把钥匙不该看到别的钥匙花了多少。
func balanceDetail(c *gin.Context) {
	summary := op.BalanceSummaryGet()
	summary.Keys = []model.APIKeyBalanceRow{op.APIKeyBalanceGet(callingAPIKey(c))}
	resp.Success(c, summary)
}

// callingAPIKey 取当前请求的 Key 对象; 中间件已按 x-api-key / Bearer 鉴权并落过 ID,
// 这里只在极端情况（鉴权通过后 Key 被删）退回只带 ID 的零值, 不影响余额查询本身。
func callingAPIKey(c *gin.Context) model.APIKey {
	id := c.GetInt("api_key_id")
	key, err := op.APIKeyGet(id, c.Request.Context())
	if err != nil {
		return model.APIKey{ID: id}
	}
	return key
}
