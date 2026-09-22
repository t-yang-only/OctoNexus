package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/auth"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

func Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie("auth")
		if err != nil || token == "" {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		if !auth.VerifyJWTToken(token) {
			c.SetCookie("auth", "", -1, "/", "", false, false)
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		c.Next()
	}
}

func APIKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		var apiKey string

		if key := c.Request.Header.Get("x-api-key"); key != "" {
			apiKey = key
		} else if authorization := c.Request.Header.Get("Authorization"); authorization != "" {
			apiKey = strings.TrimPrefix(authorization, "Bearer ")
		}

		if apiKey == "" {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}

		apiKeyObj, err := op.APIKeyGetByAPIKey(apiKey, c.Request.Context())
		if err != nil {
			resp.Error(c, http.StatusUnauthorized, resp.ErrUnauthorized)
			c.Abort()
			return
		}
		if !apiKeyObj.Enabled {
			resp.Error(c, http.StatusUnauthorized, "API key is disabled")
			c.Abort()
			return
		}
		if apiKeyObj.ExpireAt > 0 && apiKeyObj.ExpireAt < time.Now().Unix() {
			resp.Error(c, http.StatusUnauthorized, "API key has expired")
			c.Abort()
			return
		}
		// 来源 IP 白名单：空列表 = 不限制（老 Key 行为逐字不变）。
		//
		// c.ClientIP() 的结果取决于 gin 的受信代理配置（见 server.go）：
		// 默认不信任任何代理，只按 TCP 对端判定；只有配置了 trusted_proxies
		// 之后才会采信 X-Forwarded-For。两者必须一起才有意义。
		if len(apiKeyObj.AllowedCIDRs) > 0 {
			networks, err := model.ParseCIDRList(apiKeyObj.AllowedCIDRs)
			if err != nil {
				// 存进库的网段理论上都过了保存时校验；仍兜一层，
				// 免得脏数据把白名单变成"谁都过"。
				resp.Error(c, http.StatusForbidden, "API key has an invalid IP allowlist")
				c.Abort()
				return
			}
			if !model.IPAllowedByCIDRs(c.ClientIP(), networks) {
				resp.Error(c, http.StatusForbidden, "API key is not allowed from this IP")
				c.Abort()
				return
			}
		}
		statsAPIKey := op.StatsAPIKeyGet(apiKeyObj.ID)
		if apiKeyObj.MaxCost > 0 && apiKeyObj.MaxCost < statsAPIKey.StatsMetrics.OutputCost+statsAPIKey.StatsMetrics.InputCost {
			resp.Error(c, http.StatusUnauthorized, "API key has reached the max cost")
			c.Abort()
			return
		}
		// Key 级限流 (litellm G5 对标): RPM 预检查 + TPM 窗口判额, 超限 429 并带 Retry-After。
		// 限流只该发生在转发面; 本中间件同时挂在 admin 端口的 /apikey/stats|login,
		// 以路径前缀区分, 管理查询不限流。
		if !strings.HasPrefix(c.Request.URL.Path, "/api/v1/apikey/") {
			if !op.AllowKeyRPM(apiKeyObj.ID, apiKeyObj.RPM) {
				c.Header("Retry-After", "60")
				resp.Error(c, http.StatusTooManyRequests, "API key RPM limit exceeded")
				c.Abort()
				return
			}
			if !op.AllowKeyTPM(apiKeyObj.ID, apiKeyObj.TPM) {
				c.Header("Retry-After", "60")
				resp.Error(c, http.StatusTooManyRequests, "API key TPM limit exceeded")
				c.Abort()
				return
			}
			// 记账回调: 转发终态处 (relay.FinishKeyUsage) 携带实际词元调用, 请求未走到终态则按 0 记。
			recorded := false
			c.Set("key_usage_recorder", func(tokens int64) {
				if !recorded {
					recorded = true
					op.RecordKeyUsage(apiKeyObj.ID, tokens)
				}
			})
			defer func() {
				if !recorded {
					op.RecordKeyUsage(apiKeyObj.ID, 0)
				}
			}()
		}
		c.Set("supported_models", apiKeyObj.SupportedModels)
		c.Set("api_key_id", apiKeyObj.ID)
		c.Next()
	}
}
