# Claim：T-group-002 分组嵌套选路递归展开（NM-CUR-158）

- 认领会话：Cursor（`NM-CUR-158`，2026-09-14）
- 前置：T-group-001 已由本车按用户授权终裁过门（156 修复三红例后全量绿，证据见 guidance-156 记录）
- 更正记录：本轮盘点初我曾误判"T-group-002 已在树"（grep 工具空结果误读），实查 relay 包
  无任何 flatten 代码与测试文件——任务确实未实施，正常认领。

## 语义冻结口径（按登记台）

- 亲和/冷却/上限按**顶层分组**：RouteState 仍按顶层 group.ID 保存，冷却/探测/亲和的键是
  展平后具体成员行 ID（跨树唯一），子分组不持有独立路由状态。
- 子分组 manual 模式不引入第二套运行时语义：整组按 Priority 展平并入引用行位置。
- 成员指向已删除子分组：跳过该引用行（读侧宽容，写侧 GroupDel 已级联清理）。

## 交付设计

- `op.FlattenGroupItems(group)`：深度优先展平为授权成员平面表（引用行位置 splice 子组成员）；
  读侧三线防御与写侧口径一致：路径回边剪除（脏缓存/手改库）、超过 GroupItemMaxDepth 层截断、
  成员行按 ID 去重（菱形共享子组只并一次）。
- `groupSnapshot` 子分组成员 `Available` 按展平后代回填（兑现 148 线注释承诺）。
- relay `pickGroupItem(group, items)` 改收展平列表（纯函数化，manual 与 failover 两分支统一走展平），
  handler 每轮 `op.FlattenGroupItems` 后传入；冷却探测语义不变。
- 四单测（登记门口径）：展平顺序/防循环/深度截断/空树；另 relay 选路纯函数测试（冷却跳位/亲和保持/空树零值）。

## 验证

- 实施后跑 `go test ./... -count=1` 与 `internal/op` `internal/relay` `-count=3`、vet、jsoniter 构建。
