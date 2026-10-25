[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$msg = @"
feat(relay): Round 41 流式反代防缓冲响应头与流式收尾终止原因归因收敛

R41-F1(HIGH): 核心模型转发流式响应补齐反代防缓冲响应头。
  此前流式转发入口仅声明 Content-Type: text/event-stream，未设置 Cache-Control: no-cache、Connection: keep-alive 及 X-Accel-Buffering: no；
  在经过 Nginx 等反代时易被中间层开启缓冲区聚合，导致 SSE 流式输出卡顿甚至超时断流；
  现补齐三项防缓冲关键头，彻底消除反代流式卡顿隐患。
R41-F2(HIGH): 流式收尾失败原因精准归因，终结全仓唯一 unrecorded 盲区。
  此前流式循环在发生看门狗超时、本地写客户端失败或上游异常断流时，向 markFailed 传空字符串，导致落入 unrecorded 盲区；
  现在明确定义 stopReasonStreamIdle = "stream_idle_timeout"，并在事件循环中按写入失败 (client)、上游截断 (upstream) 与看门狗超时 (config) 精准标注 reason 与 source。
测试：独立单元用例 TestStopReasonStreamIdleText、TestStreamFailureRecordsPreciseStopReason 与 TestStreamHandlerPassesNonEmptyStopReason PASS；
变异：Mutation R41-F1 精准杀灭（改回传空字符串测试当场红并被 RC=1 拦截）；
回归：全仓 21 包 0 FAIL；前端检查全绿。
"@
git add internal/relay/stop_reason.go internal/relay/handler.go internal/relay/stream_r41_test.go tools/run_mut_test_r41.py tools/apply_r41.py tools/apply_r41_v2.py tools/clean_dup.py tools/debug_r41.py
git commit -m $msg
git log --oneline -1
