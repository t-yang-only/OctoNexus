import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.59] - 2026-10-25
### Usability & Reliability
- **分组创建与子分组引用状态语义收敛修复 (Round 22)**：
  - **R22-F1 (HIGH) GroupCreate 引用不存在子分组准确回 404**：此前 createGroup 在请求引用不存在的子分组时、以及 GroupUpdate 传入不存在的 active_item_id 时，因未实现 NotFound 接口返回裸错误并被包装为 500 Internal Server Error，导致监控误报网关故障。现通过 op.NotFoundf 与 writeOpError 统一收口，对不存在的子分组引用或当前活跃项精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：21 / 10 (第三周期 Round 21)', '- 当前周期累计已完成轮次：22 / 10 (第三周期 Round 22)', 1)
anchor = '- Round 21（分组更新状态语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r22 = '''
- Round 22（分组创建与子分组引用语义收敛修复，8222317 / v0.79.59 已部署）：createGroup 引用不存在子分组准确返回 404 而非 500(R22-F1)；消除子分组不存在时的虚假服务端故障误报；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.80s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r22 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
