import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.74] - 2026-10-25
### Usability & Reliability
- **一键建组与渠道/分组读操作状态语义收敛修复 (Round 37)**：
  - **R37-F1 (HIGH) groupQuickCreate 在渠道不存在时返回 404**：此前 ChannelGet 在未命中缓存时仅返回普通 error，导致 groupQuickCreate 无论遇到何种错误一律以 400 BadRequest 报错，将“目标渠道不存在”误报为输入参数格式非法，破坏了 RESTful 规范（资源不存在应为 404）；现通过 ChannelGet 返回 op.NotFoundf，并在 groupQuickCreate 中对 op.IsNotFound 进行优先 404 分流。
  - **R37-F2 (MEDIUM) getGroup / verifyChannelModels / checkChannelUpstream 统一接入 writeOpError**：此前直接返回 StatusNotFound，一旦未来出现底层系统级错误可能误报 404；现统一由 writeOpError 收口，严格分流 404 与 500。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：6 / 10 (第四周期 Round 36)', '- 当前周期累计已完成轮次：7 / 10 (第四周期 Round 37)', 1)
anchor = '- Round 36（采集凭据源更新与删除语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r37 = '''
- Round 37（一键建组与渠道/分组读操作语义收敛修复，89a4ca1 / v0.79.74 已部署）：groupQuickCreate 准确返回 404 而非 400(R37-F1)；getGroup/verifyChannelModels/checkChannelUpstream 接入 writeOpError 防误报(R37-F2)；消除一键建组查空时的状态码语义混淆；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.79s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r37 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
