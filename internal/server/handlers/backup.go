package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/bestruirui/octopus/internal/conf"
	"github.com/bestruirui/octopus/internal/op"
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
		).
		AddRoute(
			router.NewRoute("/webdav/restore", http.MethodPost).
				Handle(restoreWebDAVBackup),
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

// restoreWebDAVBackup 从远端某份备份恢复。
//
// 这是本模块里唯一会改动线上数据的操作，因此比其它动作多两道保护：
//
//  1. **恢复前先落一份本地兜底快照**（写到数据目录，路径随响应返回）。
//     恢复路径的唯一可靠回退手段就是"恢复前那份数据"，而用户往往在恢复之后
//     才发现恢复错了 —— 那时临时目录早被清空、内存状态也丢了。
//     刻意写进数据目录（与 credential.key 同级）而不是系统临时目录。
//
//  2. **如实回报增量语义**。导入是增量合并（插入新行 + 自然键 upsert），
//     不会删除"备份之后新增的行"，所以结果不是"回到备份那一刻"。
//     不把这条说清楚，用户会以为恢复等于回滚，进而做出错误的判断。
func restoreWebDAVBackup(c *gin.Context) {
	var body struct {
		File string `json:"file" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}

	cfg, err := task.WebDAVConfigFromSettings()
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}

	// 兜底快照：先写盘，再动数据。写失败则中止恢复 ——
	// "没有回退手段就动手"是恢复路径最不该有的姿态。
	safetyPath, snapshotErr := writeSafetySnapshot(c.Request.Context())
	if snapshotErr != nil {
		resp.Error(c, http.StatusInternalServerError,
			"恢复前无法写出本地兜底快照，已中止恢复："+snapshotErr.Error())
		return
	}

	result, err := task.WebDAVRestore(c.Request.Context(), cfg, body.File)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"result":        result,
		"safety_backup": safetyPath,
		"note": "导入为增量合并：新行插入、自然键 upsert，不会删除备份之后新增的行。" +
			"如需回退，用上面这份恢复前快照走「备份恢复」导入。",
	})
}

// writeSafetySnapshot 把当前数据导出一份到数据目录，返回落盘路径。
func writeSafetySnapshot(ctx context.Context) (string, error) {
	dump, err := op.DBExportAll(ctx)
	if err != nil {
		return "", err
	}
	raw, err := json.Marshal(dump)
	if err != nil {
		return "", err
	}
	path := filepath.Join(filepath.Dir(conf.AppConfig.Database.Path),
		fmt.Sprintf("restore-safety-%s.json", time.Now().Format("20060102-150405")))
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}
