package handlers

import (
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/t-yang-only/OctoNexus/internal/op"
	"github.com/t-yang-only/OctoNexus/internal/relay"
	"github.com/t-yang-only/OctoNexus/internal/server/middleware"
	"github.com/t-yang-only/OctoNexus/internal/server/resp"
	"github.com/t-yang-only/OctoNexus/internal/server/router"
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

// parseIsTestQuery 解析 is_test 查询参数（T-trace-006）。
//
// 三态：nil = 不筛这个维度；&true = 只看测试请求；&false = 只看非测试请求。
//
// 非法值直接报 400，**不静默当成"不筛"**：那会让用户以为筛选生效了、实际看到的是全部，
// 而这种"看起来对了"的错误最难发现（本项目把这类情况叫做静默降级，是明确的缺陷类别）。
// 与 status/model 那些自由文本参数不同 —— 它们是"给什么都合法"，布尔参数有明确的非法值。
func parseIsTestQuery(c *gin.Context) (*bool, bool) {
	raw := strings.TrimSpace(c.Query("is_test"))
	if raw == "" {
		return nil, true
	}
	switch strings.ToLower(raw) {
	case "true", "1":
		value := true
		return &value, true
	case "false", "0":
		value := false
		return &value, true
	}
	resp.Error(c, http.StatusBadRequest, "invalid is_test (must be 'true' or 'false')")
	return nil, false
}

// parseDateRangeQuery 解析 from/to 日期参数（需求4「按天查询」）。
//
// 接受 YYYY-MM-DD 与 RFC3339 两种写法：前者是"按天查"的自然形态（当天 00:00 起），
// 后者让调用方能精确到秒。单给 from 时补成"当天 00:00"，单给 to 时补成"次日 00:00"
// —— 否则"查 9月24日"会变成"9月24日 00:00 之后到今天"，把后几天也带进来。
//
// 非法值报 400 而不是静默忽略：静默忽略会让用户以为筛选生效了，实际看到的是全部
// （本项目把这类"看起来对了"归为静默降级，是明确的缺陷类别）。
// parseDateRangeQuery 解析 from/to 日期范围，归一成半开区间 [from, to)。
//
// 两种形状，语义刻意不同：
//
//	YYYY-MM-DD      按**整天**处理：from → 当天 00:00，to → 次日 00:00。
//	                 （半开区间，"查 9月24日"才不含 9月25日 00:00 之后的数据。）
//	RFC3339         按**精确时刻**处理，原样采用。
//	                 （用户传 14:30 就是要 14:30，压成当天 00:00 会把他要的精度丢掉。）
//
// 早先这里对两种形状都做「截断到当天 00:00」，于是 RFC3339 分支形同虚设 ——
// 传 2026-09-29T14:30:00Z 想查 14:30 之后，实际查到当天 00:00 之后。
// 支持精确时刻却又把它抹掉，比一开始不支持更糟：用户以为筛对了。
//
// 非法值一律 400，不静默忽略 —— 静默忽略会让用户以为筛选生效、实际看到全部，
// 那是最难发现的一类错（没有报错，只是数不对）。
func parseDateRangeQuery(c *gin.Context) (*time.Time, *time.Time, bool) {
	// parseDay 只认 YYYY-MM-DD，归一到当天 00:00（本地时区）。
	parseDay := func(raw string) (time.Time, bool) {
		t, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			return time.Time{}, false
		}
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location()), true
	}
	// parseExact 只认 RFC3339，原样采用（保留时刻与时区）。
	parseExact := func(raw string) (time.Time, bool) {
		t, err := time.Parse(time.RFC3339, raw)
		return t, err == nil
	}

	var from, to *time.Time
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		// 先试精确时刻：能解析成 RFC3339 就按时刻用，不再截断。
		if t, ok := parseExact(raw); ok {
			from = &t
		} else if t, ok := parseDay(raw); ok {
			from = &t
		} else {
			resp.Error(c, http.StatusBadRequest, "invalid from (want YYYY-MM-DD or RFC3339)")
			return nil, nil, false
		}
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		if t, ok := parseExact(raw); ok {
			to = &t
		} else if t, ok := parseDay(raw); ok {
			// 半开区间：to 取次日 00:00，"查 9月24日"才不含 9月25日 00:00 之后的数据。
			next := t.AddDate(0, 0, 1)
			to = &next
		} else {
			resp.Error(c, http.StatusBadRequest, "invalid to (want YYYY-MM-DD or RFC3339)")
			return nil, nil, false
		}
	}
	return from, to, true
}

// listHistory 按状态/模型/渠道/Key/关键字/日期范围倒序分页查询历史日志。
// 查询参数: status/model/channel/apikey/q/is_test/from/to/limit/offset, 全部可选。
func listHistory(c *gin.Context) {
	isTest, ok := parseIsTestQuery(c)
	if !ok {
		return
	}
	from, to, ok := parseDateRangeQuery(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	logs, total := op.RelayLogList(model.RelayLogFilter{
		Status:  c.Query("status"),
		Model:   c.Query("model"),
		Channel: c.Query("channel"),
		APIKey:  c.Query("apikey"),
		Q:       c.Query("q"),
		IsTest:  isTest,
		From:    from,
		To:      to,
		Limit:   limit,
		Offset:  offset,
	})
	resp.Success(c, gin.H{"items": logs, "total": total})
}

// exportHistory 把同一套筛选条件下的请求级明细导成 CSV (U-key-001 余项)。
// 查询参数与 /history 一致 (status/model/channel/apikey/q/from/to), 但不分页: 导出就是"把当前筛选的结果全给出去"。
// 逐行流式写出, 内存不随条数增长; 首行前的 UTF-8 BOM 让 Excel 正确识别中文表头。
func exportHistory(c *gin.Context) {
	isTest, ok := parseIsTestQuery(c)
	if !ok {
		return
	}
	from, to, ok := parseDateRangeQuery(c)
	if !ok {
		return
	}
	filter := model.RelayLogFilter{
		Status:  c.Query("status"),
		Model:   c.Query("model"),
		Channel: c.Query("channel"),
		APIKey:  c.Query("apikey"),
		Q:       c.Query("q"),
		IsTest:  isTest,
		From:    from,
		To:      to,
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

	// 心跳 10 秒: 与分组事件流同口径, 明显小于常见的 15 秒空闲上限, 避免与客户端超时同刻竞速。
	heartbeat := time.NewTicker(10 * time.Second)
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
