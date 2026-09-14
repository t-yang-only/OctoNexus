# 认领：T-deploy-001 验收遗留口复核（NM-CUR-193）

- 认领 Agent：cursor-local（NM-CUR-193，2026-09-14 13:20）
- 登记台：T-deploy-001（doing，183 exec 收口中，无常驻认领但 183/182 报告在途）
- 做事口径：只读复核 + 单文件归档补强（183 报告自己点的名"关键修复未提交，随归档入库"）；
  不碰多车道 doing 文件（quota-001/acct-001/group-003/deploy-001 exec 均在途）。
- 状态：完成，2026-09-14 13:4x

## 复核发现（183 报告原文交叉验证）

1. **rawCreate P0 未提交归档确认在树 WIP**：`internal/op/backup.go` 工作区 M 态，
   `rowCopy := row; q.Create(&rowCopy)` + `Omit(Associations)` + 整表 `createRowsRaw` 分发表均在位，
   与 183 报告 D2 描述逐行一致。**本轮未改 backup.go 一行**——归档是 183 会话自己的提交动作。
2. **真正缺口：导入链零单测**——`DBImport*` 全仓无测试文件；
   183 的"导入 200 与基线一致"是隔离实例一次性证据，不可回归。
   本轮补 `internal/op/backup_raw_test.go` 三单测：
   关联绕过回归（关联字段行 2 行如数落库）/零行 0 返回/未知形状报错。
3. **非缺口（逐项核销）**：
   - D8/D7D9 标注属实（迁移单测在位、轻量版已写明，不构成 done 阻断）；
   - 明文凭据差异已在 D2 登记且归 R-sec 建议项，不拦 R1；
   - T-group-003 review（171 实施+184 复验）不是 deploy 门（152 冻结项不含前端）。

## 验证

- `go test ./internal/op/ -run 'TestRawCreate|TestCreateRowsRaw'` 三例过；
  `go test ./... -count=1` 八包全 ok；`go vet ./...` EXIT 0。
