import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.78] - 2026-10-25
### Usability & Reliability
- **流式反代防缓冲响应头与流式收尾终止原因归因收敛 (Round 41)**：
  - **R41-F1 (HIGH) 核心模型转发流式响应补齐反代防缓冲响应头**：此前流式转发入口仅声明 Content-Type: text/event-stream，未设置 Cache-Control: no-cache、Connection: keep-alive 及 X-Accel-Buffering: no；在经过 Nginx 等反代时易被中间层开启缓冲区聚合，导致 SSE 流式输出卡顿甚至超时断流；现补齐三项防缓冲关键头，彻底消除反代流式卡顿隐患。
  - **R41-F2 (HIGH) 流式收尾失败原因精准归因，终结全仓唯一 unrecorded 盲区**：此前流式循环在发生看门狗超时、本地写客户端失败或上游异常断流时，向 markFailed 传空字符串，导致落入 unrecorded 盲区；现明确定义 stopReasonStreamIdle = "stream_idle_timeout"，并在事件循环中按写入失败 (client)、上游截断 (upstream) 与看门狗超时 (config) 精准标注 reason 与 source。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：10 / 10 (第四周期 R31-R40 已全部达成，Server酱推送成功 pushid:63786139)', '- 当前周期累计已完成轮次：1 / 10 (第五周期 Round 41)', 1)
anchor = '- Round 40（用量报告周期校验消除静默降级'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r41 = '''
- Round 41（流式反代防缓冲响应头与流式收尾终止原因归因收敛，da991e1 / v0.79.78 已部署）：补齐 Cache-Control/Connection/X-Accel-Buffering(R41-F1)；流式收尾失败精准标注 stream_idle_timeout/response_committed(R41-F2)；终结全仓唯一 unrecorded 盲区；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（1.10s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r41 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
