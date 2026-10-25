[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$msg = @"
feat(setting): Round 38 系统设置更新语义收敛至 404 (NotFound)

R38-F1(HIGH): setSetting 对不存在的设置项准确返回 404 而非 500。
  此前 SettingSetString 在未命中缓存或更新行数为空时仅返回普通 error "setting not found"，
  setSetting 无论遇到何种错误一律返回 500 InternalServerError，
  将业务层“配置项不存在”误报为服务端内部故障，破坏了 RESTful 契约。
  现通过 SettingSetString/SettingGetString 返回 op.NotFoundf 并在 setSetting 中统一由 writeOpError 收口，对不存在的配置项精确返回 404 Not Found。
测试：独立单元用例 TestSetSettingReturns404WhenKeyNotFound PASS；
变异：Mutation R38-F1 精准杀灭（改回 500 测试当场红并被 RC=1 拦截）；
回归：全仓 21 包 0 FAIL；前端检查全绿。
"@
git add internal/op/setting.go internal/server/handlers/setting.go internal/server/handlers/setting_r38_test.go tools/run_mut_test_r38.py tools/apply_r38.py
git commit -m $msg
git log --oneline -1
