import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.80] - 2026-10-25
### Observability & Traceability
- **出站与终态响应头透传 X-Octopus-Request-Id 实现端到端全链路可观测 (Round 43)**：
  - **R43-F1 (HIGH) 网关响应头显式透传 X-Octopus-Request-Id**：在 `publishDecision`（正常及故障转移出站）以及 `failRequest`（最终失败返回）逻辑中，向客户端响应头注入 `X-Octopus-Request-Id: <request.ID>`，与现有的 `X-Octopus-Route` 共同构成完全自洽的请求侧可观测协议；客户端收到任意成功或失败响应时，均可据此头与服务端的请求状态机、审计日志表（`relay_logs.id`）进行 100% 精确的一对一溯源关联。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：2 / 10 (第五周期 Round 42)', '- 当前周期累计已完成轮次：3 / 10 (第五周期 Round 43)', 1)
anchor = '- Round 42（非流式响应写出立即 Flush 消除反代缓冲延迟'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r43 = '''
- Round 43（响应头透传 X-Octopus-Request-Id 端到端可观测，8f3a07e / v0.79.80 已部署）：响应头注入 X-Octopus-Request-Id(R43-F1)；关联客户端请求与审计日志；独立源码不变量守卫测试通过；变异测试精准杀灭；上线后真实转发 200（X-Octopus-Request-Id: 1 实测透传）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r43 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_R43_UPDATED')
