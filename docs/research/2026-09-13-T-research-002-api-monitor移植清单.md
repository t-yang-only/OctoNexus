# T-research-002：api-monitor connectors 精读 → octopus 移植清单

> 任务：T-research-002 ｜ 认领：`cursor-local`（NM-CUR-021）
> 日期：2026-09-13 ｜ 状态：done ｜ 支撑需求：R-quota-001（上游余额/额度监控）

## 1. 设计精读（`reference/api-monitor/internal/`）

### Connector 三件套（`connectors/connectors.go`）
`Connector` 接口 = `Test`（探测可达）+ `Discover`（发现监控目标）+ `Scan`（定期扫描）；
`Registry` 按 `ProviderKind` 注册 12 种实现：`newAPIUser/newAPIToken`、`sub2APIUser/sub2APIToken`、
`openAIAccount/geminiAccount/anthropicAccount`、`openAIAdmin`、`openAIKey/anthropicKey`、
`manualSubscription`、`genericHTTP`。

### new-api 关键调用（`connectors/newapi.go` + `auth_helpers.go`）
- 鉴权：用户登录取 session → `newAPIUserHeaders`；**不要求 admin 权限**。
- 余额：`GET {root}/api/user/self` → `newAPIBalance(user)` + `inferQuota(user)`（宽容字段匹配几十个别名）。
- Key 枚举：`GET /api/token/` → 每个 key 取 quota/plan/30d 用量（`newAPITokenRaw` 再取 `/api/log` 求和）。
- 通告/目录 Watch（`connectors/watch_sources.go` + `scanner.go`）：
  - 公告：`GET /api/notice`
  - 分组+倍率：`GET /api/user/self/groups` 或 `/api/user/groups` + `/api/ratio_config`
  - 模型目录：`GET /api/user/models` 或 `/api/models`
  - 价格：`GET /api/pricing` 或 `/api/ratio_config`
- 变化检测：**SHA256 指纹**存档，`watchJSONResult/watchPayloadResult` 只在指纹变化时触发规则（`scanNewAPIWatch`）。

### 扫描调度（`scanner/scanner.go`）
`DiscoverInstance` 一次性发现目标（`UpsertTarget` + `RiskScore` + `NextScanAt`）；
`batchSize=50` 分批 `Scan`；扫描间隔 per-instance（`ScanIntervalSeconds`）。

### 通知（`notify/notify.go`）
单 `Service.Send(ctx, channel, alert, target)` 按 `channel.Type` 分发：
`dingtalk/feishu/wecom/webhook/phone/email_smtp/sendgrid_email/sms_twilio/sms_aliyun/sms_tencent`；
模板变量渲染（`renderAlert`）+ 加签（钉钉 HMAC-SHA256 等）。

### 加密（`crypto/secrets.go`）
`Service{key: sha256(APP_SECRET)}` → **AES-GCM**（nonce 前缀 + base64），
`Encrypt/Decrypt` 供 credential/secret 存储；另有 `Fingerprint` 只存 key 指纹。

### domain（`domain/types.go`）
`ProviderKind / TargetKind(user/apikey/announcement/group/model/pricing/news/deprecation) /
Capability / HealthStatus(healthy/warning/critical) / Money / Quota / PlanInfo /
ProbeResult / MonitorTarget / ScanResult / NotificationChannel`。

## 2. octopus 移植清单（建议都走 `internal/health/` 新包 + `M1` spec）

| # | 移植项 | 落点 | 说明 |
|---|--------|------|------|
| P1 | Connector 接口三件套 | `internal/health/monitor.go` | `Probe/DiscoverTargets/Scan`；用 octopus 的 `rhttp` 客户端复用代理 |
| P2 | new-api 用户余额监控 | `internal/health/newapi.go` | `GET /api/user/self` → 对标 `ChannelStats` 写 `HealthBalance{Quota,Used,Remaining}`；**复用渠道 BaseURL，监控凭证与转发 key 分离存储** |
| P3 | 官方价格/下架观察 | `internal/health/watch.go` | 复用 models.dev 价格同步链路（`internal/price` 已有），加下架公告 watch + 指纹 diff |
| P4 | SHA256 指纹 diff | `internal/health/fingerprint.go` | 规范化 JSON → sha256；仅变化触发事件（与 op 缓存刷新解耦） |
| P5 | 轮询调度 | `internal/task` 注册 | per-channel `ScanIntervalSeconds`，默认 5min；低频避免烧上游额度 |
| P6 | 健康信号喂选路 | `relay/route.go` | 不健康成员直接从 failover 候选中剔除（比等冷却更早），恢复后自动回归 |
| P7 | 通知渠道抽象 | `internal/notify/` | 先实现 `webhook + email_smtp` 两通道 + 发送前真实测试（对应 T-research-004） |
| P8 | AES-GCM 凭据加密 | `internal/conf` + `op` | `security.secret` 配置；现状凭据明文（R-sec-001，前置依赖） |

## 3. 不移植
Redis 缓存/失效同步、PostgreSQL 特定实现（octopus 用 gorm 多库）、短信/电话通道（个人场景低频）、
OpenAI Admin API（需要 admin，与"普通用户"原则相反）。
