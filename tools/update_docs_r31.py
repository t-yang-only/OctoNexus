import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.68] - 2026-10-25
### Usability & Reliability
- **Sub2API 账号探测状态语义收敛修复 (Round 31)**：
  - **R31-F1 (HIGH) sub2apiAccountProbe 探测不存在账号时返回 404**：此前 Sub2APIAccountProbe 查找账号记录不存在时直接返回 gorm.ErrRecordNotFound，且 Handler 层（sub2apiAccountProbe）统一包裹为 502 Bad Gateway 报错（探测失败: record not found），让调用方误以为是上游节点网络不可达，破坏了 HTTP 契约规范。现通过 errors.Is(err, gorm.ErrRecordNotFound) 转换为 op.ErrSub2APIAccountNotFound，并在 Handler 中捕获该哨兵错误返回 404 StatusNotFound。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：30 / 10 (第三周期 10 轮达标，准备推送周期报告)', '- 当前周期累计已完成轮次：1 / 10 (第四周期 Round 31)', 1)
anchor = '- Round 30（代理订阅更新与删除语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r31 = '''
- Round 31（Sub2API 账号探测语义收敛修复，63e58c0 / v0.79.68 已部署）：sub2apiAccountProbe 准确返回 404 而非 502(R31-F1)；消除账号查空时的假性网关故障误报；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.99s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r31 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
