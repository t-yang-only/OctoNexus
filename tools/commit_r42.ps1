[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$msg = @"
feat(relay): Round 42 非流式响应写出立即 Flush 消除反代缓冲延迟

R42-F1(HIGH): 核心模型非流式响应写入客户端后立即调用 c.Writer.Flush()。
  此前流式循环在每帧写入后均调用了 c.Writer.Flush()，但非流式响应在 c.Writer.Write(result.body) 成功后未显式 Flush；
  在底层基于 chunked 传输或经过 Caddy/Nginx 反代及低速链路时，非流式响应体可能会滞留在 gin.ResponseWriter 的内存缓冲中直至请求处理函数退出；
  现补齐立即 Flush，确保客户端在收到完备字节的同时建立立即可读响应边界，进一步降低长响应端到端延迟。
测试：独立源码守卫测试 TestNonStreamResponseFlushes PASS；
变异：Mutation R42-F1 精准杀灭（移除 Flush 守卫测试当场红并被 RC=1 拦截）；
回归：全仓 21 包 0 FAIL；前端检查全绿。
"@
git add internal/relay/handler.go internal/relay/flush_r42_test.go tools/run_mut_test_r42.py tools/apply_r42_v2.py
git commit -m $msg
git log --oneline -1
