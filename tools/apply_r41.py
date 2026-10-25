import io

# 1. Update stop_reason.go
p_stop = "internal/relay/stop_reason.go"
s_stop = io.open(p_stop, encoding="utf-8").read()

old_stop_c = """\t// stopReasonCommitGuard 已向上游提交（首字节已写出）后的失败，不能再换成员。
\tstopReasonCommitGuard = "response_committed"
\t// stopReasonUnrecorded 该出口没有标注终止原因。"""

new_stop_c = """\t// stopReasonCommitGuard 已向上游提交（首字节已写出）后的失败，不能再换成员。
\tstopReasonCommitGuard = "response_committed"
\t// stopReasonStreamIdle 流式响应在首个事件之后长时间无进展超时（T-timeout-001）。
\tstopReasonStreamIdle = "stream_idle_timeout"
\t// stopReasonUnrecorded 该出口没有标注终止原因。"""

assert old_stop_c in s_stop
s_stop = s_stop.replace(old_stop_c, new_stop_c, 1)
io.open(p_stop, "w", encoding="utf-8", newline="").write(s_stop)

# 2. Update handler.go
p_h = "internal/relay/handler.go"
s_h = io.open(p_h, encoding="utf-8").read()

old_h_headers = """\t\t\tif c.Writer.Header().Get("Content-Type") == "" {
\t\t\t\tc.Header("Content-Type", "text/event-stream")
\t\t\t}
\t\t\tvar encoded bytes.Buffer
\t\t\tvar chunks []*httpclient.StreamEvent
\t\t\tevent := result.first
\t\t\tlast := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
\t\t\tcommitted := false"""

new_h_headers = """\t\t\tif c.Writer.Header().Get("Content-Type") == "" {
\t\t\t\tc.Header("Content-Type", "text/event-stream")
\t\t\t}
\t\t\tc.Header("Cache-Control", "no-cache")
\t\t\tc.Header("Connection", "keep-alive")
\t\t\tc.Header("X-Accel-Buffering", "no")
\t\t\tvar encoded bytes.Buffer
\t\t\tvar chunks []*httpclient.StreamEvent
\t\t\tevent := result.first
\t\t\tlast := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
\t\t\tcommitted := false
\t\t\tstreamErrReason := stopReasonCommitGuard
\t\t\tstreamErrSource := stopSourceUpstream"""

assert old_h_headers in s_h
s_h = s_h.replace(old_h_headers, new_h_headers, 1)

old_write_err = """\t\t\t\t\tif writeErr != nil {
\t\t\t\t\t\terr = writeErr
\t\t\t\t\t\tbreak
\t\t\t\t\t}"""

new_write_err = """\t\t\t\t\tif writeErr != nil {
\t\t\t\t\t\terr = writeErr
\t\t\t\t\t\tstreamErrReason = stopReasonCommitGuard
\t\t\t\t\t\tstreamErrSource = stopSourceClient
\t\t\t\t\t\tbreak
\t\t\t\t\t}"""

assert old_write_err in s_h
s_h = s_h.replace(old_write_err, new_write_err, 1)

old_next_err = """\t\t\t\tif !result.events.Next() {
\t\t\t\t\terr = result.events.Err()
\t\t\t\t\tbreak
\t\t\t\t}"""

new_next_err = """\t\t\t\tif !result.events.Next() {
\t\t\t\t\terr = result.events.Err()
\t\t\t\t\tif err == nil && !last {
\t\t\t\t\t\terr = io.ErrUnexpectedEOF
\t\t\t\t\t}
\t\t\t\t\tstreamErrReason = stopReasonCommitGuard
\t\t\t\t\tstreamErrSource = stopSourceUpstream
\t\t\t\t\tbreak
\t\t\t\t}"""

assert old_next_err in s_h
s_h = s_h.replace(old_next_err, new_next_err, 1)

old_timeout_err = """\t\t\t// 看门狗命中时上层读到的是被取消的读错误, 这里把它还原成明确的失败原因。
\t\t\tif context.Cause(roundCtx) == errStreamIdleTimeout {
\t\t\t\terr = errStreamIdleTimeout
\t\t\t}"""

new_timeout_err = """\t\t\t// 看门狗命中时上层读到的是被取消的读错误, 这里把它还原成明确的失败原因。
\t\t\tif context.Cause(roundCtx) == errStreamIdleTimeout {
\t\t\t\terr = errStreamIdleTimeout
\t\t\t\tstreamErrReason = stopReasonStreamIdle
\t\t\t\tstreamErrSource = stopSourceConfig
\t\t\t}"""

assert old_timeout_err in s_h
s_h = s_h.replace(old_timeout_err, new_timeout_err, 1)

old_mark_failed = """\t\t\t\tif ctx.Err() != nil {
\t\t\t\t\trequest.markCanceled(ctx.Err(), string(responseBody), result.usage)
\t\t\t\t} else {
\t\t\t\t\t// 流式收尾失败（看门狗/上游中断/本地写客户端）：原因不一，
\t\t\t\t\t// 此处传空字符串走 unrecorded 兜底 —— 如实表示"这里没有细分原因"，
\t\t\t\t\t// 别为了好看硬填一个可能猜错的来源。
\t\t\t\t\trequest.markFailed(err, string(responseBody), result.usage, "", "")
\t\t\t\t}"""

new_mark_failed = """\t\t\t\tif ctx.Err() != nil {
\t\t\t\t\trequest.markCanceled(ctx.Err(), string(responseBody), result.usage)
\t\t\t\t} else {
\t\t\t\t\t// 流式收尾失败（看门狗/上游中断/本地写客户端）：由事件循环精确定位原因与来源，
\t\t\t\t\t// 不再走 unrecorded 盲区，让终止日志具备完全的可追溯性。
\t\t\t\t\trequest.markFailed(err, string(responseBody), result.usage, streamErrReason, streamErrSource)
\t\t\t\t}"""

assert old_mark_failed in s_h
s_h = s_h.replace(old_mark_failed, new_mark_failed, 1)

io.open(p_h, "w", encoding="utf-8", newline="").write(s_h)
print("R41_APPLIED")
