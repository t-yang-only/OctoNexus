# 认领：L9 预览实例值守（接力 220）+ 登记台巡检（NM-CUR-223）

- 认领 Agent：cursor-local（NM-CUR-223）
- 认领时间：2026-09-14
- 登记台：`docs/agents/ledger.md` 31 行——done 23 / review 2（T-sec-001、T-group-003）/ doing 4（T-pool-001、T-quota-001、T-acct-001、T-deploy-001）/ todo 2（T-pool-002、T-deploy-002），全部有主或门锁，无自由 todo。
- 调度依据：NM-CUR-214 §3 L9 预览生命周期值守；接力 `claims/2026-09-14-cursor-local-T-patrol-L9-220.md`（220 已完成首轮存活核验），本轮同车道值守，不与其他支线双写。
- 冲突避让：不碰 L1–L8 各车道在途文件；不改 `docs/agents/ledger.md` 状态行；预览库只读值守，不做业务联调（214 红线：L3/L4 不得在 13399/11931 预览库联调）。
- 本轮值守实测（2026-09-14）：
  1. 实例存活：`octopus.exe` PID 57228 运行中（内存 ~22.8MB）；`http://127.0.0.1:13399/` 面板 200（4249B Octopus SPA HTML）。
  2. 登录链路：`POST /api/v1/user/login`（admin）→ 200 `login successfully`；`GET /api/v1/group/list` → 200。
  3. e2e 数据完好：`e2e-preview`(id=1) 携 1 成员 `child_group_id=2 / child_group_name=e2e-child`（嵌套引用在库）；`e2e-child`(id=2) 空成员——与 214 造数口径一致，无需补种。
  4. config 无 BOM 复核（214 教训项）：`config.json` 前 3 字节非 BOM，viper 解析正常，无需重启。
- 结论：预览实例健康，无需恢复动作；无空闲 todo，无可独立交付的未完成问题，按指令值守记录后停止。
- 验收：值守记录落 `docs/worklog/2026-09-14-L9预览值守223.md`；agent_word 四件套同步。
- 红线：零业务代码改动；预览库只读；密钥不贴原文；不做整删（视觉验收未完成，实例保留）。
