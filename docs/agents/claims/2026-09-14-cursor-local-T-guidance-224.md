# 认领：T-guidance-224 在途车道指导（L3 结构环 / L5 乱码），只写文档零碰代码

- 认领 Agent：cursor-local（NM-CUR-224）
- 认领时间：2026-09-14
- 登记台：31 行——done 25 / review 1（T-sec-001 用户动作）/ doing 4
  （T-pool-001 收敛出口、T-quota-001=L3/217 在途、T-acct-001=L4/128 在途、T-deploy-001 等拍板）/
  todo 1（T-pool-002 实现门未过；L8 预研 204 已交付）。
- 用户指令：无自由任务则按我的想法指导待指导任务后完成本任务；本轮无未完成任务可领，
  但实测发现两条在途车道各有一个**他们必须知道的问题**：
  1. **L3 结构环**：`op/quota_scan.go`（14:44 新）引入 op→health→rhttp→op 导入环，
     全仓 `go build` 当前红灯（非 217 独有责任——health→rhttp→op 是 117 期既有边，
     quota_scan 是第一条 op→health 新边）；
  2. **L5 乱码注释**：`route_balance_test.go` 注释为 GBK 通道写入的乱码
     （同 195/backup_raw_test.go 缺陷类，213 已修复后例）。
- 范围：只读诊断 + 指导落盘；**不认领、不修改 217/211 在途文件**（防写冲突）；
  不改 ledger 他车道行。
- 验收：`docs/worklog/2026-09-14-在途车道指导224.md` 含可执行修复方案 A/B；
  登记表 224 翻已完成。
