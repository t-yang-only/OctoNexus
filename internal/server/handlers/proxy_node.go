package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// R-proxy-001 代理节点池：给"每个渠道 / 每个账号"各配一个独立出口。
//
// 为什么要它：同一上游站点上挂多账号时，如果所有账号都从同一个 IP 出去，上游不需要任何别的证据
// 就能把它们关联成同一批人（同 IP 多账号是最常见的风控特征）。给每个账号绑一个独立出口节点，
// 出口 IP 就分散开了。
//
// 安全口径：
//   - 节点参数与订阅地址都是凭据，密文落库（internal/secret，AAD: pool:proxy-node / pool:proxy-subscription），
//     任何响应都不回显明文（model 里 Params/URLCipher 都是 json:"-"）。
//   - 订阅地址在错误信息与提示里一律走 clashcfg.RedactURL（末段令牌打码）。
//   - 本组路由全部挂 middleware.Auth()（管理面），转发口不暴露节点池。
func init() {
	router.NewGroupRouter("/api/v1/proxy").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/nodes", http.MethodGet).Handle(proxyNodeList),
		).
		AddRoute(
			router.NewRoute("/nodes", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(proxyNodeCreate),
		).
		AddRoute(
			router.NewRoute("/nodes/import", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(proxyNodeImport),
		).
		AddRoute(
			router.NewRoute("/nodes/:id", http.MethodPut).
				Use(middleware.RequireJSON()).
				Handle(proxyNodeUpdate),
		).
		AddRoute(
			router.NewRoute("/nodes/:id", http.MethodDelete).Handle(proxyNodeDelete),
		).
		AddRoute(
			router.NewRoute("/nodes/:id/probe", http.MethodPost).Handle(proxyNodeProbeExit),
		).
		AddRoute(
			router.NewRoute("/subscriptions", http.MethodGet).Handle(proxySubscriptionList),
		).
		AddRoute(
			router.NewRoute("/subscriptions", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(proxySubscriptionCreate),
		).
		AddRoute(
			router.NewRoute("/subscriptions/:id", http.MethodPut).
				Use(middleware.RequireJSON()).
				Handle(proxySubscriptionUpdate),
		).
		AddRoute(
			router.NewRoute("/subscriptions/:id", http.MethodDelete).Handle(proxySubscriptionDelete),
		).
		AddRoute(
			router.NewRoute("/subscriptions/:id/refresh", http.MethodPost).Handle(proxySubscriptionRefresh),
		)
}

func proxyNodeList(c *gin.Context) {
	nodes, err := op.ProxyNodeList(nil, false)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	usage, err := op.ProxyNodeUsageMap(nil)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": nodes, "total": len(nodes), "usage": usage})
}

func proxyNodeCreate(c *gin.Context) {
	var in model.ProxyNodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	node, err := op.ProxyNodeCreate(nil, in)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": node})
}

func proxyNodeUpdate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid node id")
		return
	}
	var in model.ProxyNodeInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	node, err := op.ProxyNodeUpdate(nil, id, in)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": node})
}

func proxyNodeDelete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid node id")
		return
	}
	if err := op.ProxyNodeDelete(nil, id); err != nil {
		if errors.Is(err, op.ErrProxyNodeInUse) {
			resp.Error(c, http.StatusConflict, err.Error())
			return
		}
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"id": id, "removed": true})
}

// proxyNodeImport 两种来源：JSON 里的 yaml 字段（粘贴/上传内容），或 sub_id 触发该订阅拉取并导入。
func proxyNodeImport(c *gin.Context) {
	var body struct {
		YAML  string `json:"yaml"`
		SubID int    `json:"sub_id"`
		Name  string `json:"name"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if body.SubID > 0 {
		result, err := op.ProxySubscriptionRefresh(c.Request.Context(), nil, body.SubID)
		if err != nil {
			// 「订阅不存在」是 404；其余（拉取失败、解密失败）保持 502 ——
			// 那是真正的上游/网关问题，502 语义正确（T-usability-012）。
			if op.IsNotFound(err) {
				resp.Error(c, http.StatusNotFound, err.Error())
				return
			}
			resp.Error(c, http.StatusBadGateway, err.Error())
			return
		}
		resp.Success(c, result)
		return
	}
	if strings.TrimSpace(body.YAML) == "" {
		resp.Error(c, http.StatusBadRequest, "缺少 yaml 内容（或传 sub_id 触发订阅拉取）")
		return
	}
	source := strings.TrimSpace(body.Name)
	if source == "" {
		source = "manual"
	}
	result, err := op.ProxyNodeImportYAML(nil, []byte(body.YAML), source, 0)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, result)
}

func proxySubscriptionList(c *gin.Context) {
	subs, err := op.ProxySubscriptionList(nil)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{"items": subs, "total": len(subs)})
}

func proxySubscriptionCreate(c *gin.Context) {
	var in model.ProxySubscriptionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	sub, err := op.ProxySubscriptionCreate(nil, in)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": sub})
}

func proxySubscriptionUpdate(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid subscription id")
		return
	}
	var in model.ProxySubscriptionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	sub, err := op.ProxySubscriptionUpdate(nil, id, in)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": sub})
}

func proxySubscriptionDelete(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid subscription id")
		return
	}
	if err := op.ProxySubscriptionDelete(nil, id); err != nil {
		if errors.Is(err, op.ErrProxyNodeInUse) {
			resp.Error(c, http.StatusConflict, err.Error())
			return
		}
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"id": id, "removed": true})
}

func proxySubscriptionRefresh(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid subscription id")
		return
	}
	result, err := op.ProxySubscriptionRefresh(c.Request.Context(), nil, id)
	if err != nil {
		// 同上一处：订阅不存在 → 404；拉取失败 → 502（T-usability-012）。
		if op.IsNotFound(err) {
			resp.Error(c, http.StatusNotFound, err.Error())
			return
		}
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	resp.Success(c, result)
}
