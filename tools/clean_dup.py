import io

p = "internal/relay/handler.go"
s = io.open(p, encoding="utf-8").read()
dup = """\t\t\tlast := result.last
\t\t\tcommitted := false
\t\t\tstreamErrReason := stopReasonCommitGuard
\t\t\tstreamErrSource := stopSourceUpstream // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
\t\t\tcommitted := false"""

fixed = """\t\t\tlast := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
\t\t\tcommitted := false
\t\t\tstreamErrReason := stopReasonCommitGuard
\t\t\tstreamErrSource := stopSourceUpstream"""

assert dup in s
s = s.replace(dup, fixed, 1)
io.open(p, "w", encoding="utf-8", newline="").write(s)
print("CLEANED")
