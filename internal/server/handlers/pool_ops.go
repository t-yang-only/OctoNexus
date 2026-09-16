package handlers

import (
	"errors"
	"net/http"

	"github.com/bestruirui/octopus/internal/pool"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/gin-gonic/gin"
)

// 第二批号池接口（生命周期操作）：单条详情 + 探活/刷新/启停 + kind 级同步。
//
// 统一口径：
//   - 未知 kind → 404（这个后端没注册）；
//   - 条目不存在 → 404；
//   - 后端没声明/没实现该能力 → **501**，并在消息里点名缺哪个能力位——
//     外部工具据此就能回答"为什么这个按钮不能用"，而不是收到一个含糊的 400。
func poolGetEntry(c *gin.Context) {
	entry, err := pool.Get(c.Request.Context(), c.Param("kind"), c.Param("id"))
	if err != nil {
		writePoolError(c, err)
		return
	}
	resp.Success(c, entry)
}

func poolProbeEntry(c *gin.Context) {
	entry, err := pool.Probe(c.Request.Context(), c.Param("kind"), c.Param("id"))
	if err != nil {
		// 探活失败仍把当前快照回给调用方：探活的意义就是"告诉你这条现在什么状态"。
		status, message := poolErrorStatus(err)
		resp.Error(c, status, message)
		return
	}
	resp.Success(c, entry)
}

func poolRefreshEntry(c *gin.Context) {
	entry, err := pool.Refresh(c.Request.Context(), c.Param("kind"), c.Param("id"))
	if err != nil {
		writePoolError(c, err)
		return
	}
	resp.Success(c, entry)
}

func poolEnableEntry(c *gin.Context)  { writePoolError(c, setPoolEnabled(c, true)) }
func poolDisableEntry(c *gin.Context) { writePoolError(c, setPoolEnabled(c, false)) }

func setPoolEnabled(c *gin.Context, enabled bool) error {
	entry, err := pool.SetEnabled(c.Request.Context(), c.Param("kind"), c.Param("id"), enabled)
	if err != nil {
		return err
	}
	resp.Success(c, entry)
	return nil
}

func poolSyncKind(c *gin.Context) {
	report, err := pool.Sync(c.Request.Context(), c.Param("kind"))
	if err != nil {
		// 部分失败不回错误：报告里已经写明哪个服务商失败，整体结论照给（与"一个后端坏了不打没整张表"同一考虑）。
		if errors.Is(err, pool.ErrUnknownKind) || errors.Is(err, pool.ErrUnsupported) {
			writePoolError(c, err)
			return
		}
		resp.Success(c, gin.H{"report": report, "warning": err.Error()})
		return
	}
	resp.Success(c, report)
}

func writePoolError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	status, message := poolErrorStatus(err)
	resp.Error(c, status, message)
}

// poolErrorStatus 把包级错误映射成 HTTP 状态：这套映射是接口契约的一部分，外部工具靠它分支。
func poolErrorStatus(err error) (int, string) {
	switch {
	case errors.Is(err, pool.ErrUnknownKind):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, pool.ErrEntryNotFound):
		return http.StatusNotFound, err.Error()
	case errors.Is(err, pool.ErrUnsupported):
		// 501 而不是 400/403：这不是参数错，也不是权限问题，而是这个后端没有这项能力。
		return http.StatusNotImplemented, err.Error()
	default:
		return http.StatusBadGateway, err.Error()
	}
}
