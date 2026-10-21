# 持续迭代进度看板 (ITERATION_STATE.md)

## 1. 基础状态
- **当前轮次**：Round 2 / 256
- **目标截止时间**：2026-10-22 12:00 (Asia/Shanghai)
- **执行主体**：DSH (唯一实施/编码/测试/运维总控)
- **规划与审查顾问**：Codex 6.1 Sol High (通过 de_coi 派发与独立审查)
- **当前部署版本**：v0.79.38 @ `apidate.yangonly.com`

---

## 2. 轮次进度与质量台账

| 轮次 | 核心任务 | 交付物/提交 | Codex 审查评分 | 状态 | 关键缺陷/整改要点 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| **Round 1** | 弃用旧官方账号反代，接入 Sub2API 订阅反代网关架构 | `b2d4a0c`, `6b837e4` (v0.79.36/37) | **48 / 100** (建议退回整改) | 已审查 | 发现 8 项 HIGH（模型/授权缺口、enabled零值覆盖、代理未生效、探测误报、备份遗漏、明文头等） |
| **Round 2** | 根据 Codex 审查意见全面加固 Sub2API 闭环 (F1-F10) | `v0.79.38` | **已全量修复并通过验证** | 交付就绪 | 100% 修复 8 项 HIGH + 2 项 MEDIUM；打通模型物化、协议路由、敏感头隔离、备份完整性、数据库自愈迁移(017)与滚动容器 |

---

## 3. Codex Round 1 详细审查结论与整改对照 (基准 48/100 -> Round 2 全部闭环)
- **F1 (HIGH) 物化渠道无模型与授权**：已在 `internal/op/sub2api.go` 根据 9 大平台预设主流模型列表，按所选协议位（Chat/Responses/Anthropic/Adaptive）构造 `ChannelGrantConfig` 批量授权，并自动建立分组成员。
- **F2 (HIGH) 协议与平台枚举契约未打通**：在 `internal/model/sub2api.go` 建立 `ValidateSub2APIAccountType`、`ValidateSub2APIProtocol`、`ValidateSub2APIAccountMode` 等强类型校验，物化时将协议位精确映射至渠道。
- **F3 (HIGH) 跨账号与渠道写入/删除不一致**：渠道命名采用防撞格式；渠道物化失败时回滚删除账号；删除账号时联动清理渠道，失败明确报错。
- **F4 (HIGH) GORM default:true 反转显式 enabled: false**：去掉 `sub2api.go` 中 Enabled 的 GORM `default:true` 标签，DTO 改用 `*bool`，显式 `false` 完整保留并持久化。
- **F5 (HIGH) 探测绕过绑定代理节点直连上游**：Probe 严格通过 `ProxyNodeEndpoint` 注入代理 Transport，节点不可用时拒绝探测。
- **F6 (HIGH) 探测路径重叠与 401 误报**：去除重复 `/v1`（避免 `/v1/v1/models`）；凭据解密失败显式报错阻断；非 2xx 明确返回错误。
- **F7 (HIGH) 完整 JSON 备份漏掉 Sub2API 账号**：在 `internal/model/backup.go` 与 `internal/op/backup.go` 增加 `Sub2APIAccounts` 密文导出与恢复。
- **F8 (HIGH) 自定义 Headers 敏感凭据明文落库**：敏感头（Authorization/Cookie/x-api-key）剥离存入加密凭据块，Extra 仅存普通非敏感头。
- **F9 (MEDIUM) 页面丢失滚动容器被截断**：前端根容器增加 `overflow-y-auto`，自适应小屏与多卡片展示。
- **F10 (MEDIUM) 缺少生命周期更新接口与边界校验**：新增 PUT 更新接口（`/api/v1/sub2api/accounts/:id`），前端增加 `channel_id` 状态展示。

---

## 4. 自动化测试与构建门禁证据
- `internal/op/sub2api_test.go`：5 项严密单元测试 100% 通过（增删查改、enabled:false保留、敏感头加密隔离、模型授权物化、探测路径与异常拦截）。
- `internal/op/backup_roundtrip_test.go`：备份往返恢复测试 PASS（包含 `sub2api_accounts`）。
- `internal/db/fresh_install_test.go`：34 张表、32 个模型建表覆盖 PASS。
- `internal/task/webdav_test.go`：18 个 WebDAV 测试用例 100% 通过。
- 全仓 Go 单元测试 `go test ./internal/...`：全部通过，0 FAIL。
- 前端 `pnpm tsc --noEmit` & `pnpm lint`：全部通过，0 警告。
- 生产环境构建部署：HK4H8G `apidate.yangonly.com` 已运行 `v0.79.38`，服务正常活跃。

---

## 5. 汇报状态
- 当前周期累计已完成轮次：2 / 10
- 下次 Server 酱汇报阈值：Round 10