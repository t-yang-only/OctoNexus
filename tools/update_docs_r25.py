import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.62] - 2026-10-25
### Usability & Reliability
- **渠道启停与详情查询状态语义收敛修复 (Round 25)**：
  - **R25-F1 (HIGH) enableChannel 启停不存在渠道准确回 404**：此前 enableChannel 在 ChannelEnabled 失败时直接返回 500 Internal Server Error，且底层 ChannelEnabled 与 ChannelDetailGet 均使用裸错误未实现 NotFound 接口。导致修改不存在渠道状态时误报服务端崩溃。现通过 op.NotFoundf 与 writeOpError 统一收口，对不存在的渠道精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：24 / 10 (第三周期 Round 24)', '- 当前周期累计已完成轮次：25 / 10 (第三周期 Round 25)', 1)
anchor = '- Round 24（数据库增量导入系统设置校验过滤加固'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r25 = '''
- Round 25（渠道启停与详情查询状态语义收敛修复，a14210e / v0.79.62 已部署）：enableChannel/ChannelDetailGet 准确返回 404 而非 500(R25-F1)；消除渠道启停查空时的虚假服务端故障误报；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.82s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r25 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
