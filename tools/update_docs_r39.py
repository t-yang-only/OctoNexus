import io

p = 'CHANGELOG.md'
s = io.open(p, encoding='utf-8').read()
old = '所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。'
entry = '''所有重大技术更新与架构迭代均遵循 Keep a Changelog 格式记录。

## [v0.79.76] - 2026-10-25
### Usability & Reliability
- **号池查询、批量操作及声明式适配器状态语义收敛修复 (Round 39)**：
  - **R39-F1 (HIGH) poolEntries / poolExport / poolBatch 对未知 kind 返回 404**：此前 Entries 查空时返回 pool.ErrUnknownKind，而上层处理函数无论遇到何种错误一律以 400 BadRequest 报错，破坏了统一号池接口契约（未知 kind 应为 404）；现通过 errors.Is(err, pool.ErrUnknownKind) 优先返回 404 Not Found。
  - **R39-F2 (MEDIUM) 声明式适配器读取与删除健壮性收敛**：新增 poolAdapterGet (GET /api/v1/pool/adapters/:kind) 端点支持单个适配器查询（未知 404）；优化 poolstore.Remove 逻辑：增加内置适配器检查并容忍未在运行时注册的残余配置被正确清理，消除数据无法清除的隐患。'''
assert old in s
io.open(p, 'w', encoding='utf-8', newline='').write(s.replace(old, entry, 1))

p2 = 'ITERATION_STATE.md'
s2 = io.open(p2, encoding='utf-8').read()
s2 = s2.replace('- 当前周期累计已完成轮次：8 / 10 (第四周期 Round 38)', '- 当前周期累计已完成轮次：9 / 10 (第四周期 Round 39)', 1)
anchor = '- Round 38（系统设置更新语义收敛修复'
i = s2.index(anchor)
eol = s2.index(chr(10), i)
r39 = '''
- Round 39（号池查询与声明式适配器语义收敛修复，e28ded3 / v0.79.76 已部署）：poolEntries/poolExport/poolBatch 准确返回 404 而非 400(R39-F1)；新增 poolAdapterGet 并优化 Remove 容错清理(R39-F2)；消除号池未注册 kind 时的状态码语义混淆；独立单元测试通过；变异测试精准杀灭；上线后真实转发 200（0.84s）、DB 无损（21/60/sealed 60）'''
s2 = s2[:eol] + r39 + s2[eol:]
io.open(p2, 'w', encoding='utf-8', newline='').write(s2)
print('DOCS_UPDATED')
