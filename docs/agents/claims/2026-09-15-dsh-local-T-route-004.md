# 认领：T-route-004 最低延迟选路（NM-DS-006）

- 日期：2026-09-15
- Agent：dsh-local（DeepSeek Harness）
- 任务：Iterative Upgrades——给网关加「按延迟自动切换」（T-route-004 的 lowest_latency 部分）
- 范围：internal/relay/{metrics.go, strategy.go, route.go, handler.go}、internal/model/group.go、
  internal/relay/latency_test.go（新增）与 4 个测试文件的调用点；前端 api/group.ts + group/Editor.tsx + 三语 locales。
  本地新增测试分组 DS-TEST-latency（lowest_latency）与 DS-TEST-latencyf（failover 对照）。
- 结论：单测 4 例 + 九包 test 全绿 + pnpm build；活体 5/5（15.0s → 0.0s，慢成员不再被浪费调用）
- 遗留：least_busy 拆为 T-route-006（todo，含要挂的钩子清单与计数泄漏自检建议）；
  样本键仍是分组项行（GroupItem.ID）而非授权，跨分组不共享学习结果
