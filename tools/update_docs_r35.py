import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.72] - 2026-10-25
### Usability & Reliability
- **渠道人工余额设置状态语义收敛修复 (Round 35)**：
  - **R35-F1 (HIGH) balanceChannelSet 对不存在渠道返回 404**：此前 ChannelManualBalanceSet 在更新渠道余额时未检查 RowsAffected，且 ReloadChannel 未将 gorm.ErrRecordNotFound 转换为 NotFoundf，导致 balanceChannelSet 无论发生何种错误（包括渠道根本不存在）一律返回 500 InternalServerError，破坏了 RESTful 规范（资源不存在应为 404）；现通过 RowsAffected 检查、ReloadChannel 哨兵封装与 writeOpError 统一收口，对不存在的渠道精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：4 / 10 (第四周期 Round 34)', '- 当前周期累计已完成轮次：5 / 10 (第四周期 Round 35)', 1)
anchor = '- Round 34（扩展插件更新与删除语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r35 = '''
- Round 35（渠道人工余额设置语义收敛修复，f04e037 / v0.79.72 已部署）：balanceChannelSet 准确返回 404 而非 500(R35-F1)；消除人工设余额查空时的状态码语义混淆与服务器内部错误误报；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.97s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r35 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
