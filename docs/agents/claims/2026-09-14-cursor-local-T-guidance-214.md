# 认领：T-guidance-214 再次指导+新任务分配+前端预览部署

- 认领 Agent：cursor-local（NM-CUR-214，2026-09-14）
- 用户指令：再次指导，分配新的任务，尽快部署看前端状态。
- 交付：
  1. 前端预览实例：`pnpm build`（3.91s 0 错）→ `go build -tags=jsoniter` 72MB 含重嵌前端 → 隔离启动 admin 13399/relay 11931 空库，面板 200；登录+分组 create/update API 闭环；造预览数据 e2e-preview 嵌套 e2e-child（child_group_name 回名/available=false 实证）。
  2. 前端状态结论（T-group-003 代码层终验三层证据齐）：ChildPickerSection/nestedHint/三语键在位，提交形状与 GroupItemInput 对齐；浏览器截图链路因本机无 Chrome 断，视觉验收交用户开面板或 L0 备援。
  3. 新任务：L0 前端视觉验收（用户侧优先+子 agent 备援）、L9 预览实例值守（config 无 BOM 教训入册：Set-Content UTF8 带 BOM 致 viper 静默失败，第二次踩 136 同坑）。
  4. 八路 L1–L8 维持 201 口径不变；L4 红线新增"不得在 13399/11931 预览库联调"。
- 范围：只读指导+预览部署，不改业务代码；ledger 只追 T-group-003/T-deploy-001 两行口径。
- 报告：docs/worklog/2026-09-14-指导214前端预览部署与新任务分配.md
