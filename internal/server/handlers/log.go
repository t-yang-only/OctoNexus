package handlers

import (
	"net/http"
	"runtime"
	"strconv"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/relay"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/charmbracelet/log"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/log").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/overview/stream", http.MethodGet).
				Handle(streamOverview),
		).
		AddRoute(
			router.NewRoute("/:id/request-body", http.MethodGet).
				Handle(getRequestBody),
		).
		AddRoute(
			router.NewRoute("/:id/response-body", http.MethodGet).
				Handle(getResponseBody),
		).
		AddRoute(
			router.NewRoute("/:request_id/:round/stop", http.MethodPost).
				Handle(interruptRound),
		).
		AddRoute(
			router.NewRoute("/clear", http.MethodDelete).
				Handle(clearLog),
		).
		AddRoute(
			router.NewRoute("/history", http.MethodGet).
				Handle(listHistory),
		).
		AddRoute(
			router.NewRoute("/export", http.MethodGet).
				Handle(exportHistory),
		)
}

// interruptRound 中止请求当前轮次匹配的上游调用。
func interruptRound(c *gin.Context) {
	requestID, err := strconv.ParseUint(c.Param("request_id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	round, err := strconv.Atoi(c.Param("round"))
	if err != nil || round < 1 {
		resp.Error(c, http.StatusBadRequest, "invalid round")
		return
	}
	relay.Interrupt(requestID, round)
	c.Status(http.StatusNoContent)
}

// clearLog 删除全部已完成的请求记录，并在释放记录引用后主动执行垃圾回收。
func clearLog(c *gin.Context) {
	relay.Clear()
	runtime.GC()
	c.Status(http.StatusNoContent)
}

// getRequestBody 返回指定请求的原始请求体。
func getRequestBody(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	resp.Success(c, relay.RequestBody(id))
}

// getResponseBody 返回指定请求当前保存的响应体。
func getResponseBody(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, "invalid request id")
		return
	}
	resp.Success(c, relay.ResponseBody(id))
}

// listHistory 按状态/模型/渠道/Key/关键字倒序分页查询历史日志。
// 查询参数: status/model/channel/apikey/q/limit/offset, 全部可选。
func listHistory(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	logs, total := op.RelayLogList(model.RelayLogFilter{
		Status:  c.Query("status"),
		Model:   c.Query("model"),
		Channel: c.Query("channel"),
		APIKey:  c.Query("apikey"),
		Q:       c.Query("q"),
		Limit:   limit,
		Offset:  offset,
	})
	resp.Success(c, gin.H{"items": logs, "total": total})
}

// exportHistory 把同一套筛选条件下的请求级明细导成 CSV (U-key-001 余项)。
// 查询参数与 /history 一致 (status/model/channel/apikey/q), 但不分页: 导出就是"把当前筛选的结果全给出去"。
// 逐行流式写出, 内存不随条数增长; 首行前的 UTF-8 BOM 让 Excel 正确识别中文表头。
func exportHistory(c *gin.Context) {
	filter := model.RelayLogFilter{
		Status:  c.Query("status"),
		Model:   c.Query("model"),
		Channel: c.Query("channel"),
		APIKey:  c.Query("apikey"),
		Q:       c.Query("q"),
	}
	filename := "octopus-relay-logs-" + time.Now().Format("20060102150405") + ".csv"
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="`+filename+`"`)
	// 导出可能很大: 显式禁用中间层缓冲, 让浏览器尽早开始落盘。
	c.Header("X-Accel-Buffering", "no")

	written, err := op.RelayLogExportCSV(c.Writer, filter)
	if err != nil {
		// 正文可能已经写出了一部分, 此时改不了状态码; 只能记日志, 让截断的 CSV 明确地不完整。
		log.Errorf("relay log export failed after %d rows: %v", written, err)
		return
	}
	if written >= op.RelayLogExportMaxRows {
		log.Warnf("relay log export truncated at %d rows", written)
	}
}

// streamOverview 逐条发送建立连接时的概览及后续请求更新。
func streamOverview(c *gin.Context) {
	prepareSSE(c)
	snapshot, updates := relay.OpenRequestStream()
	defer relay.CloseRequestStream(updates)
	if len(snapshot) == 0 {
		// Flush a real SSE comment so proxies forward the empty-state response immediately.
		if _, err := c.Writer.Write([]byte(": connected\n\n")); err != nil {
			return
		}
		c.Writer.Flush()
	}
	for _, request := range snapshot {
		if err := sse.Encode(c.Writer, sse.Event{Event: "log", Data: request}); err != nil {
			return
		}
		c.Writer.Flush()
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := c.Writer.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			c.Writer.Flush()
		case request, ok := <-updates:
			if !ok {
				return
			}
			if err := sse.Encode(c.Writer, sse.Event{Event: "log", Data: request}); err != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}

// prepareSSE 设置实时日志连接需要的响应头。
func prepareSSE(c *gin.Context) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
}
