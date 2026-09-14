# 认领：待指导任务技术指导（NM-CUR-170）+ T-group-003 i18n 冲突避让

- 认领 Agent：cursor-local（2026-09-14）
- 类型：只读指导 + 冲突避让，不新增业务代码
- 盘点结论（登记台 31 行全扫）：
  - 无自由 todo：7 个 todo 均有车道前置未过门（T-group-003 F 车道在途 NM-CUR-169；
    T-pool-002/T-route-002/T-proto-001 前置 W1#3/W2#2/W3 全未过；T-deploy-001/002 残门=T-pool-001 报告）
  - doing 3 行归 B(117)/C(128)/E(135) 车道会话，不交叉
  - review 1 行 T-sec-001 只能用户执行（key 轮换），不可代办
  - T-group-003 前端已有 169 会话在途（api/group.ts+三语 locale 已 dirty），不并行写同一文件
- 指导对象：T-group-003 i18n 骨架冲突避让 + T-pool-001 报告残门文本指导（163/166 已授权代写/收回）
- 输出：`docs/worklog/2026-09-14-待指导任务指导170.md`（含 T-pool-001 报告四节大纲；
  T-group-003 冲突避让口径：仅补键位不同的三语文本，不碰已 dirty 文件的同一键区）
