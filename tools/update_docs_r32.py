import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.69] - 2026-10-25
### Usability & Reliability
- **手动订阅更新与删除状态语义收敛修复 (Round 32)**：
  - **R32-F1 (HIGH) updateManualSubscription 与 deleteManualSubscription 未找到记录时返回 404**：此前 ManualSubscriptionGet 在未找到目标手动订阅时仅返回普通 error，导致 updateManualSubscription 出错时统一以 400 BadRequest 报错，破坏了 RESTful 规范（资源不存在应为 404）；同时 deleteManualSubscription 无论发生何种错误（含内部数据库故障）一律固定返回 404，掩盖了真实的内部错误。现通过 op.NotFoundf 与 writeOpError 统一收口，对不存在的手动订阅记录精确返回 404 Not Found，数据库内部异常则规范回退为 500。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：1 / 10 (第四周期 Round 31)', '- 当前周期累计已完成轮次：2 / 10 (第四周期 Round 32)', 1)
anchor = '- Round 31（Sub2API 账号探测语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r32 = '''
- Round 32（手动订阅更新与删除语义收敛修复，a27f591 / v0.79.69 已部署）：update/deleteManualSubscription 准确返回 404 而非 400(R32-F1)；消除手动订阅查空时的状态码语义混淆与内部错误掩盖；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（1.03s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r32 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
