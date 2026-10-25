[Console]::OutputEncoding = [System.Text.Encoding]::UTF8
$msg = @"
feat(report): Round 40 用量报告周期校验消除静默降级与分页限额治理

R40-F1(HIGH): previewUsageReport / sendUsageReport 传入未知/非法 period 时准确返回 400 而非静默降级为 daily。
  此前 reportPeriod 在遇到非法周期参数时直接静默回退为系统默认值或 Daily，
  严重误导调用方（例如请求 biweekly 却拿到了 daily 却没有任何报错）；
  现通过 parseReportPeriod 显式校验非法值并返回 400 BadRequest，请求体带格式错误时亦严格返回 400。
R40-F2(MEDIUM): usageReportHistory 增加可选 limit 参数合法性校验（1-100）。
测试：独立单元用例 TestPreviewUsageReportRejectsInvalidPeriod 与 TestUsageReportHistoryRejectsInvalidLimit PASS；
变异：Mutation R40-F1 精准杀灭（改回静默降级测试当场红并被 RC=1 拦截）；
回归：全仓 21 包 0 FAIL；前端检查全绿。
"@
git add internal/server/handlers/usage_report.go internal/server/handlers/usage_report_r40_test.go tools/run_mut_test_r40.py tools/apply_r40.py
git commit -m $msg
git log --oneline -1
