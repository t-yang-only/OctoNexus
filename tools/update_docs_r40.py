import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.77] - 2026-10-25
### Usability & Reliability
- **用量报告周期校验消除静默降级与分页限额治理 (Round 40)**：
  - **R40-F1 (HIGH) previewUsageReport / sendUsageReport 对非法 period 返回 400**：此前 reportPeriod 在遇到非法周期参数时直接静默回退为系统默认值或 Daily，严重误导调用方；现通过 parseReportPeriod 显式校验非法值并返回 400 BadRequest，请求体带格式错误时亦严格返回 400。
  - **R40-F2 (MEDIUM) usageReportHistory 增加可选 limit 参数合法性校验**：限制在 1-100 区间，杜绝越界与类型解析异常。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：9 / 10 (第四周期 Round 39)', '- 当前周期累计已完成轮次：10 / 10 (第四周期 Round 40 达成，已推 Server酱)', 1)
anchor = '- Round 39（号池查询与声明式适配器语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r40 = '''
- Round 40（用量报告周期校验消除静默降级与分页限额治理，5e8db25 / v0.79.77 已部署）：preview/sendUsageReport 准确返回 400 拒绝非法周期(R40-F1)；usageReportHistory 支持 1-100 校验(R40-F2)；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（1.32s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r40 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
