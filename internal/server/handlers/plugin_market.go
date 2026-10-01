package handlers

// 插件市场端点（需求1/2/3）：账号余额插件与号池插件的统一安装入口。
//
// 两个来源共用同一条解析与校验链：
//   - 上传：POST /plugin/install，body 是 multipart 表单里的插件文件；
//   - GitHub：POST /plugin/install-github，body 是 {"url": "..."}。
//
// 两者都只做到"解析 + 校验 + 转交载荷"，**不直接落库**：落库要走既有的
// 采集器/号池通路（那些地方有装载门禁、加密、白名单），这里新开一层统一格式，
// 不新开一套安全面。界面拿到转交结果后，调既有接口完成真正的安装。
//
// 安全口径：整组挂 middleware.Auth()（管理面）。

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/pluginmarket"
	"github.com/t-yang-only/OctoNexus/internal/server/middleware"
	"github.com/t-yang-only/OctoNexus/internal/server/resp"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
)

func init() {
	router.NewGroupRouter("/api/v1/plugin").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/install", http.MethodPost).
				Handle(pluginInstallUpload),
		).
		AddRoute(
			router.NewRoute("/install-github", http.MethodPost).
				Use(middleware.RequireJSON()).
				Handle(pluginInstallFromGitHub),
		).
		AddRoute(
			// 市场清单：本实例支持的插件类型与各自的形状要求。
			// 给界面用来渲染"我能装什么"，也让错误信息有地方可查。
			router.NewRoute("/market", http.MethodGet).
				Handle(pluginMarketInfo),
		)
}

// pluginInstallUpload 从上传的文件安装插件。
//
// 用 FormFile 而不是自己读 body：前者有框架的大小与形状保护，
// 后者要把整个 body 读进内存才能判断是不是 multipart。
func pluginInstallUpload(c *gin.Context) {
	file, err := c.FormFile("plugin")
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "缺少上传文件（字段名 plugin）")
		return
	}
	opened, err := file.Open()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "读取上传文件失败："+err.Error())
		return
	}
	defer opened.Close()

	// 大小上限在这里就挡掉：等解析时才发现太大，意味着已经把整个文件读进了内存。
	if file.Size > pluginmarket.MaxPluginBytes {
		resp.Error(c, http.StatusBadRequest, "插件文件过大")
		return
	}
	raw := make([]byte, file.Size)
	if _, err := opened.Read(raw); err != nil && err.Error() != "EOF" {
		resp.Error(c, http.StatusBadRequest, "读取上传文件失败")
		return
	}

	plugin, err := pluginmarket.Parse(raw)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := plugin.Prepare()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": result})
}

// pluginInstallFromGitHub 从 GitHub 链接安装插件。
func pluginInstallFromGitHub(c *gin.Context) {
	var payload struct {
		URL string `json:"url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		resp.Error(c, http.StatusBadRequest, "请求体需要 {\"url\": \"<GitHub 链接>\"}")
		return
	}
	plugin, err := pluginmarket.FetchFromGitHub(payload.URL)
	if err != nil {
		// 链接形态问题（400）与下载失败（502）分开：前者用户改链接即可，
		// 后者可能是网络或仓库不存在，处置不同。
		if strings.Contains(err.Error(), "链接") || strings.Contains(err.Error(), "形态") {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}
	result, err := plugin.Prepare()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"item": result})
}

// pluginMarketInfo 返回市场清单：支持的插件类型、各自必需的字段、来源方式。
func pluginMarketInfo(c *gin.Context) {
	resp.Success(c, gin.H{
		"kinds": []gin.H{
			{
				"kind":        pluginmarket.KindBalance,
				"title":       "账号余额插件",
				"description": "把某站点怎么登录、怎么读余额打包成一份，装上即可长期自动读账",
				"requires":    []string{"hosts", "read"},
				"target":      "采集凭据源（/api/v1/collector/sources）",
			},
			{
				"kind":        pluginmarket.KindPool,
				"title":       "号池插件",
				"description": "声明一个只读的号池适配器（列条目、取条目）",
				"requires":    []string{"kind", "base_url"},
				"target":      "号池声明式适配器（/api/v1/pool/adapters）",
			},
		},
		"sources":       []string{"upload", "github"},
		"max_bytes":     pluginmarket.MaxPluginBytes,
		"github_forms":  []string{"…/blob/<分支>/<文件>", "raw.githubusercontent.com 直链", "api.github.com/repos/<o>/<r>/contents/<路径>"},
		"no_credential": "插件文件不得携带账号密码，请在安装时填写",
	})
}
