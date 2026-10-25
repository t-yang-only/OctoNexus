import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.70] - 2026-10-25
### Usability & Reliability
- **告警规则更新与删除状态语义收敛修复 (Round 33)**：
  - **R33-F1 (HIGH) updateAlertRule 与 deleteAlertRule 未找到记录时返回 404**：此前 AlertRuleGet 在未找到目标规则时仅返回普通 error，导致 updateAlertRule 出错时统一以 400 BadRequest 报错，破坏了 RESTful 规范（资源不存在应为 404）；同时 deleteAlertRule 在未找到行时未检查 RowsAffected 导致空删除误报成功 200。现通过 RowsAffected 检查、op.NotFoundf 与 writeOpError 统一收口，对不存在的告警规则精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：2 / 10 (第四周期 Round 32)', '- 当前周期累计已完成轮次：3 / 10 (第四周期 Round 33)', 1)
anchor = '- Round 32（手动订阅更新与删除语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r33 = '''
- Round 33（告警规则更新与删除语义收敛修复，0b7bd40 / v0.79.70 已部署）：update/deleteAlertRule 准确返回 404 而非 400 或空删除 200(R33-F1)；消除告警规则查空时的状态码语义混淆与误报成功；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.99s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r33 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
