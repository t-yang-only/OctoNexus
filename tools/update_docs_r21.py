import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.58] - 2026-10-25
### Usability & Reliability
- **分组更新与查询状态语义收敛修复 (Round 21)**：
  - **R21-F1 (HIGH) updateGroup 更新不存在分组准确回 404**：此前 deleteGroup 已使用 writeOpError 处理资源不存在场景（404），但 updateGroup 在 GroupUpdate 失败或 GroupGet 找不到时仍固定报 500 Internal Server Error，破坏 RESTful 契约并导致监控产生虚假服务端故障告警。现通过 op.NotFoundf 与 writeOpError 统一收口，对不存在的分组精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：20 / 10 (第二周期 10 轮达标，准备推送周期报告)', '- 当前周期累计已完成轮次：21 / 10 (第三周期 Round 21)', 1)
anchor = '- Round 20（渠道更新状态语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r21 = '''
- Round 21（分组更新状态语义收敛修复，52068a3 / v0.79.58 已部署）：updateGroup 准确返回 404 而非 500(R21-F1)；消除分组未找到时的虚假服务端故障误报；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.79s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r21 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
