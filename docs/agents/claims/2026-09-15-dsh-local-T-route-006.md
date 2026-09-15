# 认领：T-route-006 least_busy 最空闲选路（NM-DS-007）

- 日期：2026-09-15
- Agent：dsh-local（DeepSeek Harness）
- 任务：Iterative Upgrades——补齐路由策略族的最后一员（按在途请求数摊并发）
- 范围：internal/relay/{state.go, handler.go, metrics.go, strategy.go, route.go}、internal/model/group.go、
  internal/relay/busy_test.go（新增）；前端 api/group.ts + group/Editor.tsx + 三语 locales。
  本地新增测试分组 DS-TEST-busy（least_busy）与 DS-TEST-busyf（failover 对照）。
- 关键设计：在途数从活动请求注册表派生（Sending + TargetItemID），不用 acquire/release 计数器，杜绝漏释放
- 结论：单测 4 例 + 九包全绿 + pnpm build + 活体 4/4（并发下第二个请求 0.0s 改选空闲成员）
- 遗留：样本/在途仍按成员行聚合（跨分组不共享）；lowest_tpm_rpm 未做（需 Key 级限流余量数据）
