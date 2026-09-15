# 认领：T-route-003 路由策略扩展第一阶段（NM-DS-003）

- 日期：2026-09-15
- Agent：dsh-local（DeepSeek Harness）
- 任务：按用户"Iterative Upgrades"指令实施 R-route-001 第一阶段 —— GroupMode lowest_cost（价格定序）
- 范围：`internal/model/group.go`、`internal/relay/{strategy.go,strategy_test.go,balance.go,route.go}`、
  `internal/server/handlers/setting.go`、`web/src/{api/group.ts,components/modules/group/Editor.tsx,`
  `components/modules/group/MemberStatus.tsx,components/modules/log/Item.tsx,locales/*.json}`、`static/out/`（前端产物，构建生成）。
  不碰转发主流程、不改迁移、不提交任何密钥。
- 产出：见 worklog `docs/worklog/2026-09-15-路由策略迭代-lowest_cost.md`
- 结论：单测 6/6、九包 test/vet 全绿、前端构建通过、活体 A/B 6/6（模式确实改变选中成员，成本与价表吻合）
- 遗留（未做）：lowest_latency、least_busy、失败率入权重（R-route-001 未整体关闭）
