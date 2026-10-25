import io

p_h = "internal/relay/handler.go"
s_h = io.open(p_h, encoding="utf-8").read()

# 检查第 674 行
target = 'request.markFailed(err, string(responseBody), result.usage, "", "")'
assert target in s_h
new_target = 'request.markFailed(err, string(responseBody), result.usage, streamErrReason, streamErrSource)'
s_h = s_h.replace(target, new_target, 1)

# 检查 headers
old_h_headers = """			if c.Writer.Header().Get("Content-Type") == "" {
				c.Header("Content-Type", "text/event-stream")
			}
			var encoded bytes.Buffer"""

new_h_headers = """			if c.Writer.Header().Get("Content-Type") == "" {
				c.Header("Content-Type", "text/event-stream")
			}
			c.Header("Cache-Control", "no-cache")
			c.Header("Connection", "keep-alive")
			c.Header("X-Accel-Buffering", "no")
			var encoded bytes.Buffer
			var chunks []*httpclient.StreamEvent
			event := result.first
			last := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
			committed := false
			streamErrReason := stopReasonCommitGuard
			streamErrSource := stopSourceUpstream"""

# 先看当前 s_h 这一块内容
idx = s_h.find('c.Header("Content-Type", "text/event-stream")')
print("Found Content-Type at:", idx)
part = s_h[idx-20:idx+200]
print("Snippet around Content-Type:")
print(repr(part))

io.open("tools/debug_part.txt", "w", encoding="utf-8").write(part)
