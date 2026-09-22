package handlers

import (
	"net/http"

	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/bestruirui/octopus/internal/task"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/backup").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/webdav/run", http.MethodPost).
				Handle(runWebDAVBackup),
		).
		AddRoute(
			router.NewRoute("/webdav/list", http.MethodGet).
				Handle(listWebDAVBackup),
		)
}

// runWebDAVBackup 立即跑一轮云备份（面板「立即备份」按钮）。
//
// 与定时任务共用 task.WebDAVBackupNow，所以面板上试通了、定时就一定能跑通 ——
// 避免"手动可以、定时不行"这类只能靠读代码才发现的分歧。
func runWebDAVBackup(c *gin.Context) {
	name, removed, err := task.WebDAVBackupNow(c.Request.Context())
	if name == "" {
		// 上传失败。用 400 而不是 500：绝大多数失败是地址/凭据/网络配错，
		// 那属于"用户可修正的输入问题"，500 会让面板把它显示成服务端故障。
		message := "备份失败"
		if err != nil {
			message = err.Error()
		}
		resp.Error(c, http.StatusBadRequest, message)
		return
	}
	resp.Success(c, gin.H{
		"file":    name,
		"pruned":  removed,
		"warning": errorText(err), // 清理失败时非空：备份本身已成功
	})
}

// listWebDAVBackup 列出远端已有的备份文件（供面板展示"云端有几份、最新的是哪一份"）。
func listWebDAVBackup(c *gin.Context) {
	cfg, err := task.WebDAVConfigFromSettings()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	names, err := task.WebDAVList(c.Request.Context(), cfg)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{"files": names, "count": len(names)})
}

// errorText 把可能为 nil 的 error 转成字符串（空串表示没有错误）。
func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
