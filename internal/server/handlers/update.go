package handlers

import (
	"net/http"
	"strings"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/update"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/update").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(latest),
		).
		AddRoute(
			router.NewRoute("/now-version", http.MethodGet).
				Handle(getNowVersion),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Handle(updateFunc),
		)
}

func latest(c *gin.Context) {
	latestInfo, err := update.GetLatestInfo()
	if err != nil {
		// 检查更新失败（GitHub 不可达 / 被墙 / 仓库不存在）**不是服务器错误**：
		// 面板只是拿不到最新版本号。原来回 500，结果设置页的网络日志里挂着一条红色 500、
		// 前端也把它当成"服务坏了"。这里改成正常响应 + 明确原因，让界面能如实显示"检查失败"。
		resp.Success(c, gin.H{
			"tag_name":    "",
			"check_error": trimUpdateError(err.Error()),
		})
		return
	}
	resp.Success(c, *latestInfo)
}

// trimUpdateError 把上游错误压成一行短文本（面板直接展示，不写日志）。
func trimUpdateError(message string) string {
	message = strings.TrimSpace(message)
	if len(message) > 200 {
		message = message[:200] + "…"
	}
	return message
}

func getNowVersion(c *gin.Context) {
	resp.Success(c, conf.Version)
}

func updateFunc(c *gin.Context) {
	err := update.UpdateCore()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, "update success")
}
