import io

p_h = "internal/relay/handler.go"
s_h = io.open(p_h, encoding="utf-8").read()

old_h_headers = """\t\t\tif c.Writer.Header().Get("Content-Type") == "" {
\t\t\t\tc.Header("Content-Type", "text/event-stream")
\t\t\t}
\t\t\tvar encoded bytes.Buffer
\t\t\tvar chunks []*httpclient.StreamEvent
\t\t\tevent := result.first
\t\t\tlast := result.last"""

new_h_headers = """\t\t\tif c.Writer.Header().Get("Content-Type") == "" {
\t\t\t\tc.Header("Content-Type", "text/event-stream")
\t\t\t}
\t\t\tc.Header("Cache-Control", "no-cache")
\t\t\tc.Header("Connection", "keep-alive")
\t\t\tc.Header("X-Accel-Buffering", "no")
\t\t\tvar encoded bytes.Buffer
\t\t\tvar chunks []*httpclient.StreamEvent
\t\t\tevent := result.first
\t\t\tlast := result.last
\t\t\tcommitted := false
\t\t\tstreamErrReason := stopReasonCommitGuard
\t\t\tstreamErrSource := stopSourceUpstream"""

assert old_h_headers in s_h
s_h = s_h.replace(old_h_headers, new_h_headers, 1)

# 删除原来的 committed := false
old_comm = """\t\t\tlast := result.last
\t\t\tcommitted := false
\t\t\tstreamErrReason := stopReasonCommitGuard
\t\t\tstreamErrSource := stopSourceUpstream // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
\t\t\tcommitted := false"""
# 注意：原代码第 578 行是：
# last := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
# committed := false
# 所以直接看原来的这几行：

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

# 查找附近的行
idx = s_h.find("errStreamIdleTimeout")
print("Found errStreamIdleTimeout around line 630:", idx)

target_timeout = "if context.Cause(roundCtx) == errStreamIdleTimeout {\n\t\t\t\terr = errStreamIdleTimeout\n\t\t\t}"
assert target_timeout in s_h
new_timeout = "if context.Cause(roundCtx) == errStreamIdleTimeout {\n\t\t\t\terr = errStreamIdleTimeout\n\t\t\t\tstreamErrReason = stopReasonStreamIdle\n\t\t\t\tstreamErrSource = stopSourceConfig\n\t\t\t}"
s_h = s_h.replace(target_timeout, new_timeout, 1)

old_failed_call = 'request.markFailed(err, string(responseBody), result.usage, "", "")'
assert old_failed_call in s_h
new_failed_call = 'request.markFailed(err, string(responseBody), result.usage, streamErrReason, streamErrSource)'
s_h = s_h.replace(old_failed_call, new_failed_call, 1)

# 清理重复声明 committed := false（如果还有）
# 让我们先写盘看 gofmt
io.open(p_h, "w", encoding="utf-8", newline="").write(s_h)
print("PATCH_WRITTEN")
