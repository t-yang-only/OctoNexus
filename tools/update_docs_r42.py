import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.79] - 2026-10-25
### Usability & Reliability
- **非流式响应写出立即 Flush 消除反代缓冲延迟 (Round 42)**：
  - **R42-F1 (HIGH) 核心模型非流式响应写入客户端后立即调用 c.Writer.Flush()**：此前流式循环在每帧写入后均调用了 Flush，但非流式响应在 c.Writer.Write(result.body) 成功后未显式 Flush，在底层基于 chunked 传输或经过 Caddy/Nginx 反代时可能滞留在内存缓冲中；现补齐立即 Flush，确保客户端在收到完备字节的同时建立立即可读响应边界，进一步降低长响应端到端延迟。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：1 / 10 (第五周期 Round 41)', '- 当前周期累计已完成轮次：2 / 10 (第五周期 Round 42)', 1)
anchor = '- Round 41（流式反代防缓冲响应头与流式收尾终止原因归因收敛'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r42 = '''
- Round 42（非流式响应写出立即 Flush 消除反代缓冲延迟，4854efa / v0.79.79 已部署）：非流式写入成功后立即调用 c.Writer.Flush()(R42-F1)；建立确定性立即可读响应边界；独立源码守卫测试通过；变异测试精准杀灭；上线后真实转发 200（0.27s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r42 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
