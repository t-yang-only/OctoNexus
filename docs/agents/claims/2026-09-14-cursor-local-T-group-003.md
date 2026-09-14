# 认领：T-group-003 分组嵌套前端（NM-CUR-171）

- 认领 Agent：cursor-local（NM-CUR-171，2026-09-14）
- 登记台：`docs/agents/ledger.md` T-group-003（todo → doing）
- 前置门：T-group-001 done（158 指导收口：ChildGroupID/互斥/防环/013 迁移演练单测）+ T-group-002 done（162：Flatten/WithItems/handler 接线+四单测），门已开
- 后端形状（已在树，group.go：ChildGroupID *int/ChildGroupName/GrantRef/ChildRef/ValidateGroupItemRef/MaxDepth8；op：Flatten/validateChildGroupRefs/snapshot 回填；handlers：整体替换+SSE 同构）：
  - 读取 `GroupItem`：`channel_grant_id?: number`（授权成员）+ `child_group_id?: number`（子分组成员）+ `child_group_name: string`
  - 提交 `GroupItemInput`：`{channel_grant_id, child_group_id}` 双引用互斥（0 表不引用）
- 120 行旧 claim（2026-09-13-cursor-local-T-group-003.md，NM-CUR-120）为前置灭失期占位，产出已回退留痕；本 claim 为门开后实质实施
- 实施范围：
  1. `web/src/api/group.ts` 类型对齐（GroupItem 双引用 + GroupItemInput 双引用）
  2. `SelectedMember` 支持子分组引用；`memberKey` 双引用区分（`g:<grant>`/`c:<child>`，附带旧 `String(grant)` 形状兼容）
  3. Editor 加子分组选择区（候选=除自身的全部分组；防自引/成环由后端校验，前端给同名提示）；ItemList/卡片展示子分组徽标
  4. 提交 Create/Editor/Card 全走双引用载荷
  5. 三语 i18n 补键（nestedHint 骨架已由 158-7c 落盘：在此之上补 addChild/selectChild/childGroup/selfRefHint/cycleHint）
  6. `tsc --noEmit` + eslint + go 构建链复验
- 红线：不改后端 Go 文件；不改选路语义（只读+提交）；不碰 B/C 车道 doing 线；密钥不贴原文
