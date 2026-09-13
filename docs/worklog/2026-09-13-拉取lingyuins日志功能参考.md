# 2026-09-13 拉取 lingyuins/octopus 日志功能参考（只拉不合）

- 仓库：https://github.com/lingyuins/octopus｜台账：`NM-CUR-064`｜HEAD=`54f7eb7`（v2.6.0 changelog，2026-08-28）
- 动作：`git clone --depth 1` 到 `reference/lingyuins-octopus`（`.gitignore:20` 的 `reference/` 不入库，工作树无残留）
- 目标功能：截图"日志页"每个条目的富信息卡（模型→协议链、首字/总耗时、TPS、真实输入/输出、缓存命中率、费用、思考强度）
- 状态：**已拉取并精读，未合并**；合并点待用户拍板后实施（M-spec 见 §4）

## 1. 架构差异（先说结论：两边不是同一种日志）

| 维度 | 本地 octopus（当前） | lingyuins fork（参考） |
|---|---|---|
| 存储 | 进程内内存快照（重启即失，`maxFinished=50`） | 持久化 `relay_logs` + `relay_log_attempts`（DB，分页/筛选/保留策略） |
| 推送 | SSE 全量 `RelayLogOverview`（`/api/v1/log/overview/stream`） | 分页 API + SSE 轻量 `ToListItem`（`/api/v1/log` 系列：列表/详情/清空/内容清理/流） |
| 单条信息 | `usage`+`cost`+`duration`+`target_channel/model`（`web/src/api/log.ts`） | 除上列外另有 `ftut` 首字时间、`attempts[]` 全链路、`reasoning_*`、`semantic_cache_hit`、`cache_read_tokens`、`billing_window`（`model/log.go`） |
| 卡片渲染 | 8 指标栅格（`LogMetrics`，`web/src/components/modules/log/Item.tsx`） | 富卡片：首字/总耗时/TPS/缓存命中率/思考强度/出入 token/费用 + 可折叠 attempts 明细 + request/response JSON 视图（61KB `Item.tsx`，`motion` 动画 + `morphing-dialog`） |
| 筛选 | 无（仅按 RequestID 实时更新） | 渠道/凭据/端点类型/状态/测试标记/多模型精确匹配 + 穿透到 attempts 维度 + 模型搜索框（`LogFilterBar`，`index.tsx` 21KB） |

## 2. 可取部分（按移植成本排序）

1. **卡片派生指标（纯前端，零后端改动）**：`formatTPS`（tokens/timeMs）、`formatCacheHitRate`（cacheRead/total）、
   `formatDuration`（ms→s）、`costFmt`（2 有效数字 CNY）——本地 `RelayLogOverview.usage` 已有全部输入，
   直接抄公式重写进 `Item.tsx` 的 `LogMetrics`（详见 `reference/lingyuins-octopus/.../log/Item.tsx:60-100` 的公式区，
   **重写勿复制**：对方仓库未标 license，按思路重实现）。
2. **字段可见性偏好（纯前端）**：`ui-store.ts`（99 行，zustand persist：endpointType/channelName/actualModel/apiKeyName/
   clientIP/cost/tps/cacheHitRate/reasoningEffort/reasoningTokens 十开关 + 自动刷新间隔）——与"偏好弹窗"需求同源，
   可与 NM-CUR-025 批准门一起定。
3. **`display.ts` 容错合并（纯前端）**：`resolveLogDisplayFields`（detail 缺字段时从 attempts 尾部/渠道表/模型名回退）
   + `formatJsonForCopy`（复制时 pretty-print）——本地 SSE 偶发缺 `usage.details` 时同样需要。
4. **筛选栏（前后端联动，M-spec）**：`LogFilterBar` + `LogFilter`（含 `include_attempts` 穿透语义）——本地无持久化查询口径，
   先做前端本地过滤（内存列表），持久化筛选等 relay_logs 落地后再接。
5. **持久化日志（大工程，另立项）**：`model/log.go`（139 行：RelayLog/RelayLogListItem/RelayLogAttempt 三表）+
   `op/relaylog/relaylog.go`（940 行：写入队列/保留策略/流订阅）+ `handlers/log.go`（247 行：5 路由）——
   与"UsageHourly 小时桶"（U-perf-001）同属持久化基建，建议合并为一个 M-spec（日志+统计一次建模）。

## 3. 不取部分（明确排除）

- `motion`/`morphing-dialog` 动画栈：本地卡片无动画依赖，不为日志页单开新依赖。
- `ErrorLogView` 前端错误上报链（`error_log.go` + `/api/v1/error-log`）：与本次截图功能无关，另立项。
- 权限中间件 `RequirePermission(auth.PermLogsRead/Write)`：本地 admin 单端口无 RBAC，不移植。
- 后端 `relay_log_keep_*` 全套保留策略：等持久化 M-spec 时再定（默认 7 天/不按条数仅作参考值）。

## 4. 待拍板（M-spec 输入，不在本任务实施）

- [ ] 卡片新增 TPS/缓存命中率/首字时间三指标？（建议做，纯前端，1 天量级）
- [ ] 字段可见性十开关 + 自动刷新？（建议与"偏好设置"合并做）
- [ ] 筛选栏先做内存过滤版，还是等持久化一次到位？（建议前者，2 天量级）
- [ ] 持久化 `relay_logs` 是否与 UsageHourly 合并建模？（建议合并，见 U-perf-001）
- [ ] fork 代码重写时的 license 声明口径（对方仓库未标 license，只借鉴公式与字段语义，不复制文件）

## 5. 本任务变更（仅参考+文档，未改业务代码，未做提交）

- 新增 `reference/lingyuins-octopus`（gitignore，不入库，不提交）。
- 新增本篇 + worklog 索引一行；登记表 064 行 started→done；日志追加 064 节；文件清单补 064 行。
