import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.73] - 2026-10-25
### Usability & Reliability
- **采集凭据源更新与删除状态语义收敛修复 (Round 36)**：
  - **R36-F1 (HIGH) collectorSourceUpdate 与 collectorSourceDelete 对不存在凭据源返回 404**：此前 credentialSourceRow 在通过 ID 查找采集源未命中时仅返回普通 error，导致 collectorSourceUpdate 出错时统一以 400 BadRequest 报错，破坏了 RESTful 规范（资源不存在应为 404）；同时 CredentialSourceDelete 在未找到行时未检查 RowsAffected 导致空删除误报成功 200。现通过 RowsAffected 检查、op.NotFoundf 与 writeOpError 统一收口，对不存在的采集凭据源精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：5 / 10 (第四周期 Round 35)', '- 当前周期累计已完成轮次：6 / 10 (第四周期 Round 36)', 1)
anchor = '- Round 35（渠道人工余额设置语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r36 = '''
- Round 36（采集凭据源更新与删除语义收敛修复，86ca9a7 / v0.79.73 已部署）：collectorSourceUpdate/collectorSourceDelete 准确返回 404 而非 400 或空删除 200(R36-F1)；消除采集凭据源查空时的状态码语义混淆与误报成功；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（1.12s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r36 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
