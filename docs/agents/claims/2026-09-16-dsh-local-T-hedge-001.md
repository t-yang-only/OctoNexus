# CLAIM: T-hedge-001 首字竞速 + T-test-004 真实上游测试门禁

- 任务：T-hedge-001（用户 2026-09-16 指令：高峰期/突然唤醒时并发请求分组内排序靠前的多个成员，取最优首字）、
  T-test-004（同一指令：不要在空闲时间用真实 API 测试）
- 认领：dsh-local（NM-DS-016），2026-09-16
- 锁定范围：`internal/relay/hedge.go`（新）、`internal/relay/handler.go`（转发轮次接入）、`internal/relay/state.go`（inFlightByModel / retargetRound）、
  `internal/relay/route.go`（hotRouteDeps）、`internal/model/group.go`（四个 hedge 配置项）、
  `web/src/api/group.ts`、`web/src/components/modules/group/Editor.tsx`、`web/src/components/modules/group/Card.tsx`、
  `web/src/locales/{zh_hans,zh_hant,en}.json`、`scripts/api-tests/run_hedge_test.py`（新）、`scripts/api-tests/run_all.py`、
  `scripts/api-tests/run_real_tests.py`、`scripts/api-tests/run_costmode_test.py`、
  `docs/research/2026-09-16-T-hedge-001-首字竞速设计稿.md`、`docs/worklog/2026-09-16-首字竞速与测试门禁.md`、台账与需求登记。
- 边界：不改变未开启竞速时的任何行为（未开启走原单路分支）；落选成员被我们自己取消 → 不算失败、不进冷却、不计入统计；
  竞速默认关闭（会多打上游、多花钱）；真实上游套件默认不跑。
- 验证口径：`scripts/api-tests/run_hedge_test.py` 5/5（**只用本地 mock**）；全量矩阵 20 套件（含 hedge）；`go test ./... -count=1`；
  `pnpm build`；`go build -o octopus.exe` 后重启实例复跑竞速套件。
- 状态：已完成（见 ledger T-hedge-001 / T-test-004 与 docs/worklog/2026-09-16-首字竞速与测试门禁.md）。
- 未做：权重模型（包月/按次/余额/倍率/质量，R-weight-001）——口径草案在设计稿 §10，需用户先确认优先级（例如"贵但快"是否可压过"便宜但常错"）。
