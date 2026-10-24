import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.65] - 2026-10-25
### Usability & Reliability
- **模型价格更新与删除状态语义收敛修复 (Round 28)**：
  - **R28-F1 (HIGH) updateLLM 与 deleteLLM 未找到模型价格记录时返回 404**：此前 LLMUpdate、LLMDelete 与 LLMGet 在未找到目标模型记录时，仅返回裸字符串错误，并在 Handler 层（updateLLM / deleteLLM）统一被包裹为 500 Internal Server Error，破坏了 RESTful 规范（资源不存在应为 404）并引发虚假的服务端故障告警。现通过 op.NotFoundf 与 writeOpError 统一收口，对不存在的模型价格记录精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：27 / 10 (第三周期 Round 27)', '- 当前周期累计已完成轮次：28 / 10 (第三周期 Round 28)', 1)
anchor = '- Round 27（模型映射删除状态语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r28 = '''
- Round 28（模型价格更新与删除语义收敛修复，9aceb3d / v0.79.65 已部署）：updateLLM/deleteLLM 准确返回 404 而非 500(R28-F1)；消除模型价格查空时的虚假服务端故障误报；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.98s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r28 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
