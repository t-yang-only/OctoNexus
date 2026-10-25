import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.75] - 2026-10-25
### Usability & Reliability
- **系统设置更新状态语义收敛修复 (Round 38)**：
  - **R38-F1 (HIGH) setSetting 对不存在的设置项返回 404**：此前 SettingSetString 在未命中缓存或更新行数为空时仅返回普通 error，导致 setSetting 无论遇到何种错误一律返回 500 InternalServerError，将业务层“配置项不存在”误报为服务端内部故障，破坏了 RESTful 契约；现通过 SettingSetString/SettingGetString 封装返回 op.NotFoundf，并在 setSetting 中统一由 writeOpError 收口，对不存在的配置项精确返回 404 Not Found。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：7 / 10 (第四周期 Round 37)', '- 当前周期累计已完成轮次：8 / 10 (第四周期 Round 38)', 1)
anchor = '- Round 37（一键建组与渠道/分组读操作语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r38 = '''
- Round 38（系统设置更新语义收敛修复，aefb050 / v0.79.75 已部署）：setSetting 准确返回 404 而非 500(R38-F1)；SettingSetString/SettingGetString/SettingSetInt 统一封装 NotFoundf 哨兵错误；消除未知设置项写入时的状态码语义混淆与服务器内部错误误报；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.85s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r38 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
