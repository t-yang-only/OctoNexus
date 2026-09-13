# Claim: T-log-001 日志富化（卡片指标 + 可见性/刷新 + 内存筛选 + relay_logs 建模）

- 任务 ID：`T-log-001`
- 认领 Agent：cursor-local（NM-CUR-074 接管；原 074 登记行为 started 占位，本会话接管实施）
- 认领日期：2026-09-13
- 来源：`NM-CUR-074` 登记行「日志富化全都要」+ `NM-CUR-064` 参考报告 §2 可取部分（lingyuins/octopus 只借鉴公式与字段语义，逐文件重写，不复制文件；对方仓库未标 license）
- 范围：
  1. 卡片派生指标（纯前端）：TPS / 缓存命中率 / 首字时间（后端补 `first_byte_at` 字段，`markCommitted` 处打点）。
  2. 字段可见性十开关 + 自动刷新间隔（zustand persist 本地偏好，不动业务接口）。
  3. 内存筛选栏（状态/模型/渠道/Key/关键字，前端本地过滤；持久化筛选等 relay_logs 落地后再接）。
  4. `relay_logs` 持久化建模（与 UsageHourly 合并建模，M-spec 级：先建模+写入+保留策略，分页查询接口视工作量收敛）。
- 红线：不复制 fork 文件；不改现有 SSE 消息的必需字段（只追加可选字段）；密钥不入库不贴原文。
