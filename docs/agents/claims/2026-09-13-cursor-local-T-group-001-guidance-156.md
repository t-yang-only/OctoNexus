# 指导记录：T-group-001 嵌套测试三红例（NM-CUR-156 代 A 车道执行）

- 被指导任务：T-group-001 重建（责任会话 NM-CUR-141，A 车道）
- 指导依据：用户号令"等待指导的任务按你的想法指导并完成"；A 车道文件自 23:48 起闲置 2h+，
  三红例阻塞 T-audit-003 runtime 复验与 T-deploy-001 冻结门。
- 本车（NM-CUR-156）代执行修复，2026-09-14 01:52-02:05 实测。

## 修复清单

1. **TestSyncGroupItemsDepthCap（测试方向反了，实现无罪）**
   建链循环 parent/child 与自身注释相反（建成 d1→d0→…，末端追加不增深，实现放行是对的）。
   改 `parent := names[i-1]; child := names[i]` 对齐注释意图 d00→…→d08，
   末端 d08→d00 经 walk 深度 9>8 正确拒绝。
   附带更正：NM-CUR-151 审计节 F-3 曾猜测"记忆化剪枝 seen<=depth 有误，应改 seen>=depth"——
   复核判定该建议**错误**：浅访问已完整展开子树，剪深访问是健全剪枝；真实根因即测试方向。已在本记录更正，防止后续照抄坏建议。
2. **TestValidateGroupTreeRefsOnCreate（全局库 nil panic）**
   `validateGroupTreeRefs` 改收 conn 形参（与 syncGroupItems 同款注入），GroupCreate 传 db.GetDB()，
   行为不变；测试传内存库直调。
3. **TestGroupDelCascadesChildRefs（panic 掩盖的第三例）**
   测试用裸 SQL 删组断言级联——级联语义在 GroupDel 事务本体而非 DB 约束。
   提取 `groupDelOn(conn, id)` 事务本体（纯移动），GroupDel 行为不变，测试改走 groupDelOn 真实生产路径。

## 验证（01:52-02:05 实测）

- `go test ./... -count=1` 六包全 ok（**首个全绿点**）；
- `go test ./internal/op/ -count=3` ok；`go vet ./...` EXIT 0；`go build -tags=jsoniter ./...` EXIT 0。

## 门影响

- T-group-001：review 维持（模型/迁移/测试齐且全绿，具备过门条件，终裁权在调度/用户 148 线）；
- T-audit-003 runtime 复验、T-quota-001 runtime、T-deploy-001 前置之"141 红灯修复"：**阻塞解除**。
