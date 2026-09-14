# 认领：T-group-003 分组嵌套前端（树形展示/子分组选择器+三语 i18n）

- 认领 Agent：cursor-local（NM-CUR-120）
- 认领时间：2026-09-13
- 登记台：`docs/agents/ledger.md` T-group-003（todo，无认领）
- 前置：T-group-001（113 模型层 review 在仓）/ T-group-002（119 选路 doing 在途）
- 计划：
  1. `web/src/api/group.ts` 类型对齐后端（`child_group_id`/`child_group_name` + 双引用输入）
  2. `SelectedMember` 支持子分组引用；`memberKey` 双引用区分；Card/Editor/Create 提交双引用
  3. Editor 加子分组选择区（防自引由后端校验，前端只做同名提示）；ItemList/卡片展示子分组徽标
  4. en/zh_hans/zh_hant 三语补键；tsc + 构建验证
  5. 只做 UI，不改选路；不碰后端 Go 文件
