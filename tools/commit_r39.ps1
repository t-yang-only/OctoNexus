[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$msg = @"
feat(pool): Round 39 号池查询、批量操作及声明式适配器 404 语义收敛

R39-F1(HIGH): poolEntries / poolExport / poolBatch 对未知 kind 准确返回 404 而非 400。
  此前 Entries 查空时返回 pool.ErrUnknownKind，而上层处理函数无论遇到何种错误一律以 400 BadRequest 报错，
  破坏了统一号池接口契约（未知 kind 应为 404）；现通过 errors.Is(err, pool.ErrUnknownKind) 优先返回 404 Not Found。
R39-F2(MEDIUM): 声明式适配器读取与删除健壮性收敛。
  新增 poolAdapterGet (GET /api/v1/pool/adapters/:kind) 端点支持单个适配器查询（未知 404）；
  优化 poolstore.Remove 逻辑：增加内置适配器检查并容忍未在运行时注册的残余配置被正确清理，消除数据无法清除的隐患。
测试：独立单元用例 TestPoolEntriesReturns404WhenKindUnknown、TestPoolAdapterGetReturns404WhenNotFound 与 TestPoolAdapterRemoveReturns404WhenNotFound PASS；
变异：Mutation R39-F1 精准杀灭（改回 400 测试当场红并被 RC=1 拦截）；
回归：全仓 21 包 0 FAIL；前端检查全绿。
"@
git add internal/poolstore/store.go internal/server/handlers/pool.go internal/server/handlers/pool_adapters.go internal/server/handlers/pool_r39_test.go tools/run_mut_test_r39.py tools/apply_r39.py
git commit -m $msg
git log --oneline -1
