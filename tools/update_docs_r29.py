import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.66] - 2026-10-25
### Usability & Reliability
- **代理节点更新与删除状态语义收敛修复 (Round 29)**：
  - **R29-F1 (HIGH) proxyNodeUpdate 与 proxyNodeDelete 未找到节点时返回 404**：此前 ProxyNodeUpdate 在节点主键 ID 不存在时返回 gorm.ErrRecordNotFound，ProxyNodeDelete 在未找到行时未检查 RowsAffected，且 Handler 层（proxyNodeUpdate / proxyNodeDelete）统一以 400 BadRequest 报错，破坏了 RESTful 规范（资源不存在应为 404）。现通过 RowsAffected 检查、op.NotFoundf 与 writeOpError 统一收口，对不存在的代理节点精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：28 / 10 (第三周期 Round 28)', '- 当前周期累计已完成轮次：29 / 10 (第三周期 Round 29)', 1)
anchor = '- Round 28（模型价格更新与删除语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r29 = '''
- Round 29（代理节点更新与删除语义收敛修复，f8dcd3c / v0.79.66 已部署）：proxyNodeUpdate/proxyNodeDelete 准确返回 404 而非 400(R29-F1)；消除代理节点查空时的状态码语义混淆；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.98s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r29 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
