# 认领：T-group-003 分组嵌套三语 i18n 骨架（NM-CUR-169）

- 登记台：`docs/agents/ledger.md` T-group-003（todo，无认领 Agent；F 车道 W2#1；
  前置 T-group-001 done、T-group-002（162 口径）已齐——允许开工"三语 i18n 骨架"，禁写 child_group_id 字段）
- 用户指令：领取任务并完成；遇阻登记未完成并跳过，执行其他支线；无未完成问题且有需指导任务时按想法指导后完成。
- 前置确认：T-group-002 done（162：四单测在树、全量绿）；F 车道允许 i18n 骨架开工。
- 本轮范围（只做 i18n 骨架，不碰 child_group_id 逻辑）：
  1. `web/src/locales/{zh_hans,zh_hant,en}.json` 的 `group.form` 追加嵌套说明键：
     `nestedHint`（嵌套语义：子分组行在选路时展平，路由状态挂顶层；Available 表示至少一叶子可用）；
  2. `web/src/api/group.ts` 仅注释补一句可用性口径（不增字段、不改形状）；
  3. `pnpm exec tsc --noEmit` 过（或等价前端校验）；
- 红线：禁写/禁引 `child_group_id` 字段与任何树形组件；不改后端 Go 文件；不碰 B/C 车道 doing 线；
  不复制其他会话 claim 内容。
- 验收：三语键齐（同名同层）、tsc 过、台账同步。
