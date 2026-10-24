import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.61] - 2026-10-25
### Security & Reliability
- **数据库增量导入系统设置校验过滤加固 (Round 24)**：
  - **R24-F1 (HIGH) createUpsertSettings 导入设置前置执行 Validate 校验**：此前 DBImportIncremental 导入备份文件中的 Settings 时，直接使用 createUpsertSettings 批量写入数据库，未作任何合法性校验。若备份被手改或含有损坏的非布尔值/非法邮箱/错误正则等脏数据，会导致系统全局设置被污染甚至引发运行时崩溃。现对导入设置逐项执行 setting.Validate 校验过滤，跳过非法条目，确保数据库设置项严格合法。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：23 / 10 (第三周期 Round 23)', '- 当前周期累计已完成轮次：24 / 10 (第三周期 Round 24)', 1)
anchor = '- Round 23（设置热生效分发容错与事务一致性加固'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r24 = '''
- Round 24（数据库增量导入系统设置校验过滤加固，47e084f / v0.79.61 已部署）：createUpsertSettings 前置 Validate 过滤脏设置(R24-F1)；消除手改或损坏备份对全局配置的潜在污染；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.77s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r24 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
