import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.63] - 2026-10-25
### Usability & Reliability
- **模型映射更新与状态切换语义收敛修复 (Round 26)**：
  - **R26-F1 (HIGH) toggleModelMapping 与 updateModelMapping 未找到规则时返回 404**：此前 ModelMappingUpdate、ModelMappingGet 与 ModelMappingToggle 在目标模型映射规则主键 ID 不存在时，仅返回裸字符串错误，并在 Handler 层被错误包装为 400 Bad Request。资源不存在本属于 404 范畴，报 400 会误导客户端认为是载荷语法问题。现通过 op.NotFoundf 与 writeOpError 统一收口，对不存在的模型映射规则精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：25 / 10 (第三周期 Round 25)', '- 当前周期累计已完成轮次：26 / 10 (第三周期 Round 26)', 1)
anchor = '- Round 25（渠道启停与详情查询状态语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r26 = '''
- Round 26（模型映射更新与状态切换语义收敛修复，73ca937 / v0.79.63 已部署）：toggleModelMapping/updateModelMapping 准确返回 404 而非 400(R26-F1)；消除模型映射查空时的状态码语义混淆；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.99s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r26 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
