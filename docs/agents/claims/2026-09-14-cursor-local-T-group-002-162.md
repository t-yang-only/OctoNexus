# Claim：T-group-002 分组嵌套选路递归展开（NM-CUR-162）

- 认领会话：Cursor（`NM-CUR-162`，2026-09-14 03:20）
- 前置：T-group-001 done（158 终裁后转 done，迁移+模型在树且全绿）
- 状态：完成，2026-09-14 03:5x

## 落盘真相核验（本轮实查结论）

- 树上 `op.FlattenGroupItems` + `WithItems` + handler 接线 + groupSnapshot 回填已在 158/161 线：
  158 claim 自述"无落盘"不实——`WithItems`/`FlattenGroupItems`/`PickGroupItem` 外壳均在树（158 号自己写了 route.go 外壳），
  161 则补齐 handler/route/snapshot 接线与 claim；故本轮不重复实现展开算法。
- 唯一真缺口：**Flatten 自己的四单测缺失**（relay 包无测试文件；op 只有嵌套写侧单测）。

## 本轮交付

- `internal/op/group_flat_test.go`：四单测
  （展平顺序/防循环+自引用/深度截断按 Model.MaxDepth 链/空树+悬空引用），内存 swapGroupCache 隔离，不碰 DB；
  其中 DepthCap 沿用"截断"口径（与 Flatten 实现 `depth>max return` 一致），与 op 写侧"超限拒绝"口径分层互补
  （写侧拒绝坏配置在先；读侧脏缓存/手改库截断在后——两者语义不同，注释已区分）。
- 冲突处置：本轮初期曾按 119 旧 claim 另起 `relay/group_tree*.go`（防环报错口径）；
  实查发现与 158/161 线"剪除+截断"口径实质冲突且造成 itemOf 重声明编译破，**已删除自建两文件**，
  以在树 158/161 口径为准（读侧宽容剪除；转发不炸整树）。

## 验证

- `go test ./... -count=1` 七包全 ok；
- `go test ./... -count=3` 无 FAIL/panic；
- `go vet ./...` EXIT 0；`go build -tags=jsoniter ./...` EXIT 0。
