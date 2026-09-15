# 路由策略迭代：GroupMode lowest_cost（R-route-001 第一阶段）

> 触发：用户指令 **"Iterative Upgrades"**（按项目自身路线图做迭代升级）
> 依据：`docs/agents/需求登记.md` 第 13 行 **R-route-001**（"GroupMode 新增 lowest_cost（数据现成），后续 lowest_latency"）
> 规格：`docs/research/2026-09-13-T-research-003-路由策略设计稿.md`（"先做 lowest_cost：价格已知、无需窗口统计"）
> 执行：dsh-local ｜ 日期：2026-09-15 ｜ 状态：**第一阶段已交付并实测**

## 1. 迭代边界

设计稿把路由策略拆成 lowest_cost / lowest_latency / least_busy 三种骨架，并显式排除
complexity/quality/adaptive 与 tag 体系。本次只做 **第一阶段 lowest_cost**，
且按项目既有做法让"模式本身即开关"：分组不选 `lowest_cost` 时行为一字不变，
不引入全局灰开关，`route_balance_enabled`（T-route-002 的加权轮询开关）语义不变。

## 2. 定序口径（写进代码注释，可复核）

1. **只改"本轮先试谁"的顺序**：亲和/冷却/探测/成员尝试上限仍归 `pickGroupItem` 既有链路；
2. **过滤口径与加权轮询完全一致**：抽出 `partitionCandidates`，渠道或凭据停用、剩余额度归零的成员剔除，
   冷却成员压队尾（到期即回），可尝试成员为空 → `ErrNoEligibleMember`，调用方按无目标等待；
3. **排序键**：单位成本升序 → priority 升序 → ID 升序（稳定排序，同输入同输出）；
4. **无价格数据一律沉底**：价表查不到（`GetLLMPrice` 返回 nil）或价表写 0 都算无数据。
   本轮导入的 137 条价格里 **48 条为 0**，若不沉底，0 价成员会永远当选、把真实流量推给没标价的成员；
   该口径与 `balance.go` 延迟排序的"无数据沉底"一致；
5. **单位成本 = 价表 Input + Output**（每百万 token 单价之和）：输入输出 1:1 混合时的两倍均价，
   与单项单价同序，避免为此引入用量统计依赖。

## 3. 改动

| 层 | 文件 | 内容 |
|----|------|------|
| 模型 | `internal/model/group.go` | `GroupModeLowestCost` 常量；新增 `GroupMode.IsValid()` 作为模式白名单唯一出口；三处 `binding:"oneof=..."` 同步 |
| 选路 | `internal/relay/strategy.go`（新） | `CostProvider`、`unitPriceFromPrice`、`memberUnitPrice`、`rankByLowestCost`、`pickGroupItemLowestCost` |
| 选路 | `internal/relay/balance.go` | 抽出 `partitionCandidates` 供加权轮询与策略共用（过滤/冷却语义只有一份） |
| 分发 | `internal/relay/route.go` | 新增 `pickGroupItemByMode`：`lowest_cost` 走价格定序（模式即开关）、`failover` 依全局开关走加权轮询、其余走原语义 |
| 校验 | `internal/server/handlers/setting.go` | 导入备份的模式校验改用 `GroupMode.IsValid()`（原先硬编码 manual/failover，会把新模式的备份判为非法） |
| 前端 | `web/src/api/group.ts` | `GroupMode` 联合类型加 `'lowest_cost'` |
| 前端 | `web/src/components/modules/group/Editor.tsx` | 模式下拉新增"最低成本" |
| 前端 | `.../group/MemberStatus.tsx`、`.../log/Item.tsx` | 冷却/亲和倒计时与"人工切换成员"的判定由"是否 failover"改为"是否非 manual"（策略模式同样是自动选路，人工切换应禁用、倒计时应显示） |
| 文案 | `web/src/locales/{zh_hans,zh_hant,en}.json` | 新增 `form.lowest_cost`，`form.modeHint` 补充最低成本说明 |

## 4. 验证

**单测**（`internal/relay/strategy_test.go`，6 例全过）：价格定序（便宜在前、同价按 priority/ID）、
无数据沉底（查不到与 0 价）、冷却压尾 + 不可用/归零剔除 + 全剔除报错、折算函数（nil/0/负数=无数据）、
**模式真的改变顺序**（同一批成员：lowest_cost 选便宜但 priority 靠后的成员，failover 仍选 priority 首个）、
manual 语义不受接线影响。

**全量**：`go build ./...` 0、`go vet ./...` 0、`go test ./... -count=1` 九包全绿；
前端 `pnpm build`（tsc + vite）通过。

**活体 A/B**（真实上游，同一渠道 `53HK-L` 的两个不同价成员，优先级都把贵的放前面）：

| 分组 | 模式 | 期望 | 实测 target_model | 成本核对 |
|------|------|------|------------------|----------|
| `DS-TEST-costmode` | lowest_cost | 便宜成员 | **glm-5.3-flash** | 6.0375e-05 = (15×0.075 + 237×0.25)/1e6 ✓ |
| `DS-TEST-costfailover` | failover | priority 首个 | **deepseek-v4-flash-0731** | 4.5075e-04 = (33×0.15 + 743×0.6)/1e6 ✓ |

即：同一批成员、同样的优先级顺序，只换模式就换选中的人，且成本与价表算式逐项吻合
（脚本：`%TEMP%\octopus-dstest\run_costmode_test.py`，6/6 全绿；实例重启后新前端 chunk 由实例正常 serve）。

## 5. 踩坑（供后续迭代复用）

- `pnpm build` 的 `emptyOutDir: true` 会**删掉被 git 跟踪的 `static/out/README.md`**：构建后须
  `git checkout -- static/out/README.md` 还原，否则工作树凭空多出一个删除；本次已还原。
- 前端产物**只出 `.js.gz`（没有明文 .js）**：核对"某字符串是否进了产物"必须先解压再搜，直接 grep 会得到假阴性（我踩过一次）。
- 本机 Go 1.27 的 gofmt 与项目基线（1.26）对若干既有文件判定不同：**只格式化自己改动的文件**，不要顺手全量格式化。

## 6. 后续（本轮不做）

- **lowest_latency**：`balance.go` 里的 `LatencyProvider` 接缝已在，但还缺"成员级真实延迟指标的写入点"
  （设计稿的 `memberMetrics.avgLatencyMs`），需要在成功请求旁路落指标后才能接线；
- **least_busy**：需在途计数（按成员聚合 running 请求）；
- **失败率入权重**：设计稿 phase1 的 `unit_price × (1 + recent_fail_rate)` 未做，当前是纯价格定序
  （坏成员仍由既有的失败/冷却/探测链路挡住）；
- 需求台 R-route-001 未整体关闭：以上三项做齐才算做完。
