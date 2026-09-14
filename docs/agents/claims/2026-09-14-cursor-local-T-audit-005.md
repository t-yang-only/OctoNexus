# 认领：T-audit-005 custom_balance 负剩余复核审计（used-only/超额边界）

- 认领 Agent：cursor-local（NM-CUR-157）
- 认领时间：2026-09-14 02:07
- 类型：已完成修改复核审计 + 缺陷修复（验证支线，登记台无空闲 todo）
- 背景：跳转页审计 T-audit-004 在扫描期已被 NM-CUR-153 认领并 done；T-audit-003 runtime
  -race 经实测为 Windows TSan 影子内存环境阻塞（error 87），非缺 syso。按用户指令转验证支线，
  对象为尚未审过的 review 态核心：`internal/health/custom_balance.go`。
- 发现并修复：used-only 与 quota<used 超额两条边界均产出负 Remaining（误触发归零停用风险），
  与 ParseBalancePayload 147 口径对齐修复 + 2 回归单测。
- 报告：docs/worklog/2026-09-14-验证审核157-custom_balance负剩余.md
