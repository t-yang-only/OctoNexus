import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.56] - 2026-10-25
### Usability & Reliability
- **API Key 更新状态语义收敛修复 (Round 19)**：
  - **R19-F1 (HIGH) updateAPIKey 更新不存在 Key 准确回 404**：此前 deleteAPIKey 已使用 writeOpError 处理资源不存在场景（404），但 updateAPIKey 在查找不到目标 Key 时依然固定返回 500 Internal Server Error，破坏 RESTful 语义并导致监控误报网关故障。现通过 op.NotFoundf 与 writeOpError 统一收口，对不存在的 Key 精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：18 / 10', '- 当前周期累计已完成轮次：19 / 10', 1)
anchor = '- Round 18（API Key 新建白名单字段持久化修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r19 = '''
- Round 19（API Key 更新状态语义收敛修复，cddb9e7 / v0.79.56 已部署）：updateAPIKey 准确返回 404 而非 500(R19-F1)；消除资源未找到时的虚假服务端故障误报；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.81s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r19 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
