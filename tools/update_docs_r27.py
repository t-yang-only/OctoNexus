import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.64] - 2026-10-25
### Usability & Reliability
- **模型映射删除状态语义收敛修复 (Round 27)**：
  - **R27-F1 (HIGH) deleteModelMapping 删除不存在规则时返回 404**：此前 ModelMappingDelete 在删除不存在的主键 ID 时直接忽略行影响数并返回 nil，或在出错时被 Handler 层统一包裹为 400 Bad Request，破坏了 RESTful 规范（资源不存在应为 404）。现通过 RowsAffected 检查、op.NotFoundf 与 writeOpError 统一收口，对不存在的模型映射删除精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：26 / 10 (第三周期 Round 26)', '- 当前周期累计已完成轮次：27 / 10 (第三周期 Round 27)', 1)
anchor = '- Round 26（模型映射更新与状态切换语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r27 = '''
- Round 27（模型映射删除状态语义收敛修复，59f281c / v0.79.64 已部署）：deleteModelMapping 准确返回 404 而非 400(R27-F1)；消除模型映射删除查空时的状态码语义混淆；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.98s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r27 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
