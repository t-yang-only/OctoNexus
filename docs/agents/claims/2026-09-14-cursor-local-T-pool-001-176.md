# 认领：T-pool-001 号池管理选型代写收口（NM-CUR-176）

- 登记台：`docs/agents/ledger.md` T-pool-001（doing，cursor-local；135/E 车道零交付超期）
- 授权：NM-CUR-160 收口调度（135 超 6 小时零交付则"代写收口并关 135"）+ NM-CUR-166 收口规划重申；
  前置 claim：`claims/2026-09-14-cursor-local-T-pool-001-165.md`（NM-CUR-165 代写收口）
- 缺口实证：唯一口径文件 `docs/requirements/2026-09-13-号池管理选型.md` 不在树（`Test-Path=False`），
  本轮代写补齐即过门
- 范围（165 claim 四节口径）：候选清单（各 2-3 个，字段统一协议/登录/额度可见性/封号风险/活跃度，
  验证码绕过/逆向/cookie 盗用一票否决）+ 合规红线（官方 OAuth/扫码，密文落库，日志无 Key）+
  并入映射（Channel 四件套+监控凭证映射+QuotaZeroStop+跳转页兜底）+ 推荐优先级（OpenAI/Gemini 先行）
- 红线：只做文档交付，不写业务代码；不预置任何账号池实施；不拉 `reference/` 入库；
  选型不等第一阶段开工
- 验收：文件在树、非空壳、四节齐、红线明确；过门解锁 T-deploy-001 D1-D9
