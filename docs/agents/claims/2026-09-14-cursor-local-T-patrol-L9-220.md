# 认领：L9 预览实例值守 + 登记台巡检（NM-CUR-220）

- 认领 Agent：cursor-local（NM-CUR-220，2026-09-14）
- 调度依据：NM-CUR-214 §3 新任务 **L9 预览生命周期值守**（锁：`scripts/` 预览辅助 + `%TEMP%\oct-front-deploy`，禁碰业务树）；其余 201 L1–L8 车道均有主（213-L5 / 217-L3 / 214-L2 终验 / 205-L7 闭环 / 209-L6 闭环 / 204-L8 预研），不抢。
- 盘点结论：ledger 31 行 done 24 / review 2 / doing 4 / todo 2，无自由 todo；可执行支线 = L9 值守（214 已建预览实例在途，本轮复核其存活并值守）。
- L9 范围：
  1. 存活巡检：`%TEMP%\oct-front-deploy` 实例存活核验（http://127.0.0.1:13399/ 面板 + /status）；
  2. 按需重启恢复（config 无 BOM 教训：一律 Python utf-8 写，不用 Set-Content）；
  3. e2e 数据补种核验（e2e-preview 嵌套 e2e-child 仍在）；
  4. 值守记录入 worklog；不做整删（视觉验收未完成，保留实例）。
- 红线：禁碰业务树代码；预览库 admin/13399 只读值守不联调（214 红线：L3/L4 不得在此库联调）；密钥不贴原文。
- 验收：值守记录 + 存活证据（HTTP 200）落 worklog；agent_word 四件套同步。
