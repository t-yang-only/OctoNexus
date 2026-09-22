package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// CLI 配置导出（吸收上游 lingyuins/octopus 的 CLI Config Export，管理面）。
//
// 挂在 middleware.Auth() 之内：导出内容里**包含 API Key 明文**（那正是它有用的原因——
// 用户要拿去粘贴），匿名可读等于把钥匙直接给出去。
func init() {
	router.NewGroupRouter("/api/v1/cli-export").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/generate", http.MethodPost).
				Handle(generateCLIExport),
		)
}

func generateCLIExport(c *gin.Context) {
	var req struct {
		Tool    string `json:"tool" binding:"required"`
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
		Model   string `json:"model"`
		// APIKeyID 是可选的便利入口：只给 key 的 id，由服务端取明文，
		// 免得用户为了导出一份配置还得先把 key 复制一遍。
		APIKeyID int `json:"api_key_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	if !op.IsValidCLITarget(req.Tool) {
		resp.Error(c, http.StatusBadRequest, "unsupported tool: "+req.Tool)
		return
	}

	key := req.APIKey
	if key == "" && req.APIKeyID > 0 {
		item, err := op.APIKeyGet(req.APIKeyID, c.Request.Context())
		if err != nil {
			resp.Error(c, http.StatusNotFound, err.Error())
			return
		}
		key = item.APIKey
	}

	export, err := op.CLIExportBuild(op.CLITarget(req.Tool), req.BaseURL, key, req.Model)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, export)
}
