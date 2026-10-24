import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.60] - 2026-10-25
### Reliability & Consistency
- **设置热生效分发容错与事务一致性加固 (Round 23)**：
  - **R23-F1 (HIGH) setSetting 内存分发 switch 遵循统一 fail-safe 语义**：此前 model_info_update_interval 与 route_balance_enabled 在 SettingSetString 成功落库后，若 switch 分发中发生类型转换错误，会直接返回 400 截断并中断响应，破坏了持久化与 HTTP 状态的自洽性。现将 switch 分发收口为与其他设置项一致的非阻塞注入模式，确保落库成功后响应恒定 200，杜绝状态割裂。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：22 / 10 (第三周期 Round 22)', '- 当前周期累计已完成轮次：23 / 10 (第三周期 Round 23)', 1)
anchor = '- Round 22（分组创建与子分组引用语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r23 = '''
- Round 23（设置热生效分发容错与事务一致性加固，039e0e4 / v0.79.60 已部署）：setSetting 统一 fail-safe 内存注入分发(R23-F1)；消除落库成功后被 switch 截断报错的状态割裂；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.80s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r23 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
