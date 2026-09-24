package model

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

type SettingKey string

const (
	SettingKeyProxyURL                SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval       SettingKey = "stats_save_interval"        // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval SettingKey = "model_info_update_interval" // 模型信息更新间隔(小时)
	SettingKeyCORSAllowOrigins        SettingKey = "cors_allow_origins"         // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyModelFilter             SettingKey = "model_filter"               // 渠道获取模型时的全局过滤表达式; 留空表示不过滤
	SettingKeyQuotaScanInterval       SettingKey = "quota_scan_interval"        // 余额采集扫描周期(分钟), T-quota-001; 0 表示停用扫描任务
	// 用量报告（吸收上游 lingyuins/octopus 的 Usage Reports）：按周期把用量与成本摘要
	// 推到已配置的通知渠道。默认关闭——通知是打扰，只有用户主动开了才发。
	SettingKeyUsageReportEnabled SettingKey = "usage_report_enabled" // 是否启用周期用量报告
	SettingKeyUsageReportPeriod  SettingKey = "usage_report_period"  // daily / weekly / monthly
	SettingKeyUsageReportHour    SettingKey = "usage_report_hour"    // 发送时刻(0..23, 本地时区整点)
	// 加权综合选路（weighted 模式）的维度权重, 取值 0..100, 全部缺省时用下面这组保守默认值。
	// 留成设置项是为了让「哪一维更重要」由用户决定, 不写死在代码里。
	SettingKeyRouteWeightCost    SettingKey = "route_weight_cost"    // 成本（价表 input+output）
	SettingKeyRouteWeightQuality SettingKey = "route_weight_quality" // 质量（成员近期成功率）
	SettingKeyRouteWeightLatency SettingKey = "route_weight_latency" // 延迟（成员最近一次尝试耗时）
	SettingKeyRouteWeightBusy    SettingKey = "route_weight_busy"    // 在途（该成员此刻并发数）
	SettingKeyRouteWeightLoad    SettingKey = "route_weight_load"    // 近期消耗（60s 窗口 token/请求）
	// 计费类维度（R-weight-001 第二阶段）: 价表反映不了倍率/按次/余额/包月, 这四项补充"实际有多贵"。
	SettingKeyRouteWeightMultiplier SettingKey = "route_weight_multiplier"        // 倍率（渠道计价倍率, 越低越好）
	SettingKeyRouteWeightPerCall    SettingKey = "route_weight_per_call"          // 按次单价（每请求成本, 越低越好）
	SettingKeyRouteWeightBalance    SettingKey = "route_weight_balance"           // 余额（最近一次扫描的剩余额度, 越多越好）
	SettingKeyRouteWeightMonthly    SettingKey = "route_weight_monthly"           // 包月余量（剩余比例, 越多越好）
	SettingKeyRouteMonthlyAction    SettingKey = "route_monthly_exhausted_action" // 包月额度用尽时: demote(降权, 默认) 或 exclude(剔除)
	// 额度分压（mode=allocate, T-allocate-001）：把"每个 key 只有一点钱"变成"按各自还能干多少次活分配流量"。
	SettingKeyRouteAllocateTokens    SettingKey = "route_allocate_estimate_tokens"       // 请求没给出 token 估算时的兜底单次 token 量（把余额折成"还能发多少次"用）; 默认 1000
	SettingKeyRouteAllocateHealth    SettingKey = "route_allocate_health_weight"         // 健康折扣强度 0..100: 成功率低/耗时长/刚被限流的成员权重打几折; 默认 40, 0 表示不看健康只看余额
	SettingKeyRouteAllocateSlowMs    SettingKey = "route_allocate_slow_latency_ms"       // 慢成员阈值(毫秒): 最近一次尝试超过它即按超出比例打折; 0 表示不按延迟打折
	SettingKeyRouteSlowLatencyMs     SettingKey = "route_slow_latency_ms"                // 慢成员分区阈值(毫秒, T-route-003): 超过即排到候选队尾, failover 等全部模式共用; 默认 30000, 0 表示关闭
	SettingKeyRouteAllocateMinReq    SettingKey = "route_allocate_min_requests"          // 剩余请求数低于该值即剔除(次数已不足一次); 默认 1, 0 表示不按余量剔除
	SettingKeyRouteMemberRPMLimit    SettingKey = "route_member_rpm_limit"               // 成员级每分钟请求上限(RPM), 达到即让开一轮; 0 表示不限（上游真实限额不可知, 这是"自己先刹车"）
	SettingKeyRouteMemberTPMLimit    SettingKey = "route_member_tpm_limit"               // 成员级每分钟 token 上限(TPM), 达到即让开一轮; 0 表示不限
	SettingKeyRouteThrottleCapSecond SettingKey = "route_ratelimit_cooldown_max_seconds" // 上游 429 带 Retry-After 时的冷却上限(秒), 默认 300; 0 表示不按上游提示、一律用分组配置的冷却
	// 速度观测与速度应对 (T-speed-001): 既要让"谁慢"可观测, 也要真的动手削减慢成员的流量、并把"等首帧"的时间收紧。
	SettingKeyRouteSpeedWeight     SettingKey = "route_allocate_speed_weight"      // 速度折扣强度 0..100: 首帧慢/吞吐低的成员在分压里打几折; 默认 40, 0 表示不看速度
	SettingKeyRouteSpeedSlowTtfb   SettingKey = "route_speed_slow_ttfb_ms"         // 首帧慢阈值(毫秒): 最近一次流式首帧超过它即按倍数打折; 默认 3000, 0 表示不按首帧判慢
	SettingKeyRouteSpeedSlowTPS    SettingKey = "route_speed_slow_tps"             // 吞吐慢阈值(token/s): 窗口吞吐低于它即按倍数打折; 默认 8, 0 表示不按吞吐判慢
	SettingKeyRouteSpeedFeMultiple SettingKey = "route_speed_first_event_multiple" // 自适应首帧看门狗倍数: 流式等首帧的预算 = min(分组配置, 该成员实测首帧 × 它); 默认 4, 0 表示关闭
	SettingKeyRouteSpeedFeFloor    SettingKey = "route_speed_first_event_floor_ms" // 自适应看门狗下限(毫秒): 实测首帧再小也不把预算压到这个值以下; 默认 5000
	// 入站请求体上限 (T-bodylimit-001): 依赖库 axonhub/llm 把读入站正文的上限写死成 64 MiB（包内私有常量, 改不了）,
	// 超过就回 "failed to read request body: request body too large"。而 Codex 的 remote compact 这类场景正文会超过它,
	// 于是我们自己读正文、按本设置项定上限（0 = 不限制）。默认 256 MiB: 比旧上限宽 4 倍, 同时仍是一道防呆闸。
	// 注意内存: 正文会整份驻留内存并在每轮改写时复制, 上限开得越大, 单请求峰值内存越高。
	SettingKeyRelayMaxRequestBody SettingKey = "relay_max_request_body_bytes" // 入站请求体上限(字节); 默认 268435456(256MiB), 0 表示不限制
	SettingKeyQuotaAlertThreshold SettingKey = "quota_alert_threshold"        // 余额告警阈值(额度点), 剩余额度低于该值记告警事件; 留空或<=0 表示不告警 (归零停用不受其影响, 恒按 remaining<=0 判定)
	// 总余额聚合 (T-balance-001): 把各渠道读到的剩余额度折成同一个货币口径, 供 /v1/dashboard/billing/* 与 /v1/balance 查询。
	SettingKeyBalancePointsPerUnit SettingKey = "balance_points_per_unit"      // 换算口径: 多少额度点 = 1 个货币单位; 默认 500000 (new-api 惯例)
	SettingKeyBalanceUserSelfPath  SettingKey = "balance_user_self_path"       // 余额接口路径; 各家站点不同, 默认 new-api 系 /api/user/self
	SettingKeyBalanceCurrency      SettingKey = "balance_currency"             // 折算后的货币名, 仅用于显示; 默认 USD
	SettingKeyRouteBalanceEnabled  SettingKey = "route_balance_enabled"        // 故障转移分组是否用加权轮询定序候选 (T-route-002); 默认关闭, 走原有优先级选路
	SettingKeyRequestFaultAction   SettingKey = "relay_request_fault_action"   // 上游判定"请求本身非法"(400/413/422 一类)时的取向: failover(换成员再试, 默认) 或 failfast(立刻回上游原文)
	SettingKeyAlertWebhookURL      SettingKey = "alert_webhook_url"            // 告警事件 webhook 地址, 留空不推送; 余额告警/归零停用等事件 POST JSON 到该地址
	SettingKeyRouteProbeEnabled    SettingKey = "route_probe_enabled"          // 冷却成员主动探活开关 (R-probe-001); 默认关闭: 每次探测都是一次真实计费请求
	SettingKeyRouteProbeInterval   SettingKey = "route_probe_interval_seconds" // 主动探活周期(秒), 0 表示停用探活任务; 默认 300
	// 号池扩展层（R-pool-ext-001 第三批，用户对"四个边界"回复「我全都要」后落地）：
	// 声明式适配器让"接一个新的反代工具包"变成提交一份 JSON，因此必须显式划边界——
	// 白名单为空即一律拒绝（fail closed），宁可用不了也不要默认敞着一个"按 JSON 请求任意 URL"的入口。
	SettingKeyPoolDeclarativeHosts    SettingKey = "pool_declarative_hosts"    // 声明式适配器允许访问的域名白名单(逗号分隔, 支持后缀); 留空 = 一律拒绝注册
	SettingKeyPoolDeclarativeAdapters SettingKey = "pool_declarative_adapters" // 已注册的声明式适配器(整份 JSON 的密文, 凭据在其中, 接口永不回显)

	// 通知渠道 (R-alert-001 余项): 一个事件同时投递到全部启用渠道。
	// 密钥口径: 三个群机器人的 webhook 地址本身即凭据(与既有 alert_webhook_url 同性质, 面板可见可编辑);
	// SMTP 密码不进设置表, 只从环境变量 OCTOPUS_SMTP_PASSWORD 读——避免把邮箱密码写进库与备份转储。
	SettingKeyAlertChannels SettingKey = "alert_channels" // 启用的通知渠道, 逗号分隔: webhook,feishu,dingtalk,wecom,smtp; 默认 webhook
	// R-proxy-001 代理内核（自托管 mihomo）：把 Clash 节点变成 http://127.0.0.1:<port> 出口。
	SettingKeyProxyCorePath      SettingKey = "proxy_core_path"       // mihomo 内核二进制路径; 留空用 <数据目录>/core/mihomo[.exe]
	SettingKeyProxyCoreAutostart SettingKey = "proxy_core_autostart"  // 启动时自动拉起内核; 默认 true（有启用节点时）
	SettingKeyProxyCorePortStart SettingKey = "proxy_core_port_start" // 出口端口池起始（冷门高位段）; 默认 41000
	SettingKeyProxyCorePortEnd   SettingKey = "proxy_core_port_end"   // 出口端口池结束; 默认 41999
	SettingKeyProxyExitProbeURL  SettingKey = "proxy_exit_probe_url"  // 出口 IP 探测地址; 默认 https://api.ipify.org
	// R-plugin-001 社区反代扩展插件：社区玩家自制的"反代工具"由 octopus 托管运行，
	// 出网一律经节点池出口（全局隐秘代理），本组设置管它的端口池与默认出口。
	SettingKeyPluginPortStart        SettingKey = "plugin_port_start"        // 插件入站端口池起始; 默认 42000（与内核端口池分开，便于两侧各自排障）
	SettingKeyPluginPortEnd          SettingKey = "plugin_port_end"          // 插件入站端口池结束; 默认 42999
	SettingKeyPluginDefaultEgressID  SettingKey = "plugin_default_egress_id" // 未单独指定出口的插件用哪个节点; 0 = 没有默认出口（此时拒绝启动，绝不直连真实 IP）
	SettingKeyPluginHTTPHosts        SettingKey = "plugin_http_hosts"        // runtime=http 允许连的远端主机白名单（逗号分隔）; 默认空 = 只允许本机回环
	SettingKeyAlertFeishuWebhook     SettingKey = "alert_feishu_webhook"     // 飞书群机器人 webhook 地址
	SettingKeyAlertDingTalkWebhook   SettingKey = "alert_dingtalk_webhook"   // 钉钉群机器人 webhook 地址
	SettingKeyAlertWeComWebhook      SettingKey = "alert_wecom_webhook"      // 企业微信群机器人 webhook 地址
	SettingKeyAlertServerChanSendKey SettingKey = "alert_serverchan_sendkey" // Server酱(Turbo/³)的 SendKey; 它本身即凭据, 接口不回显, 也可用环境变量 OCTOPUS_SERVERCHAN_SENDKEY 替代
	SettingKeyAlertSMTPHost          SettingKey = "alert_smtp_host"          // SMTP 服务器地址 (不含端口)
	SettingKeyAlertSMTPPort          SettingKey = "alert_smtp_port"          // SMTP 端口: 465 走隐式 TLS, 其余走 STARTTLS/明文
	SettingKeyAlertSMTPUser          SettingKey = "alert_smtp_user"          // SMTP 登录用户; 留空表示不做认证
	SettingKeyAlertSMTPFrom          SettingKey = "alert_smtp_from"          // 发件人地址
	SettingKeyAlertSMTPTo            SettingKey = "alert_smtp_to"            // 收件人地址, 多个用逗号分隔
	// 受信反向代理地址（IP 或 CIDR，逗号分隔）。只影响「怎么判断请求来源 IP」这一件事。
	//
	// 默认空 = 不信任任何代理：c.ClientIP() 只取 TCP 对端，X-Forwarded-For / X-Real-IP
	// 一律忽略。这样即使有人伪造转发头也改不了网关看到的来源，API Key 的 IP 白名单
	// 才拦得住东西。只有网关确实挂在可信反代（如 Caddy/nginx）后面时，才把该反代的
	// 地址填进来，此时转发头才会被采信。
	SettingKeyTrustedProxies SettingKey = "trusted_proxies"
	// WebDAV 云备份（T-backup-001）：把导出转储按周期推到远端，本机坏了还有一份。
	//
	// 口令**不进设置表**，只从环境变量 OCTOPUS_WEBDAV_PASSWORD 读——与 SMTP 密码同一纪律：
	// 设置表会随导出转储与备份一起流转，把口令写进去等于把它复制到每一个备份里。
	SettingKeyWebDAVURL      SettingKey = "webdav_url"            // 目录地址，如 https://dav.example.com/remote.php/dav/files/me/octopus
	SettingKeyWebDAVUsername SettingKey = "webdav_username"       // 登录用户名
	SettingKeyWebDAVEnabled  SettingKey = "webdav_enabled"        // 是否启用定时上传
	SettingKeyWebDAVInterval SettingKey = "webdav_interval_hours" // 上传间隔（小时），默认 24
	SettingKeyWebDAVKeep     SettingKey = "webdav_keep"           // 远端保留份数，0 = 不清理
)

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
		{Key: SettingKeyStatsSaveInterval, Value: "10"},       // 默认10分钟保存一次统计信息
		{Key: SettingKeyCORSAllowOrigins, Value: ""},          // CORS 默认不允许跨域，设置为 "*" 才允许所有来源
		{Key: SettingKeyModelInfoUpdateInterval, Value: "24"}, // 默认24小时更新一次模型信息
		{Key: SettingKeyModelFilter, Value: ""},               // 默认不过滤模型
		{Key: SettingKeyQuotaScanInterval, Value: "5"},        // 余额扫描默认 5 分钟一轮 (P5 低频口径)
		// 用量报告默认关闭 + 每天 9 点: 用户主动开启后才发, 时刻选在上班时段便于当天看到昨天的情况。
		{Key: SettingKeyUsageReportEnabled, Value: "false"},
		{Key: SettingKeyUsageReportPeriod, Value: "daily"},
		{Key: SettingKeyUsageReportHour, Value: "9"},
		{Key: SettingKeyQuotaAlertThreshold, Value: ""},               // 默认不设告警阈值; 归零停用恒生效, 不经该阈值
		{Key: SettingKeyBalancePointsPerUnit, Value: "500000"},        // 总余额换算默认 new-api 惯例: 500000 点 = 1 个货币单位
		{Key: SettingKeyBalanceUserSelfPath, Value: "/api/user/self"}, // 默认 new-api 系; 自建额度接口的站点改这一项
		{Key: SettingKeyBalanceCurrency, Value: "USD"},                // 只是显示名: 换 CNY 不改变任何折算, 改口径请调上面的点数
		// R-proxy-001 代理内核: 默认路径留空 → 用 <数据目录>/core/mihomo[.exe]; 端口池默认取高位冷门段。
		{Key: SettingKeyProxyCorePath, Value: ""},
		{Key: SettingKeyProxyCoreAutostart, Value: "true"},
		{Key: SettingKeyProxyCorePortStart, Value: "41000"},
		{Key: SettingKeyProxyCorePortEnd, Value: "41999"},
		{Key: SettingKeyProxyExitProbeURL, Value: "https://api.ipify.org"},
		// R-plugin-001 插件体系: 端口池与内核分开（41000-41999），默认没有绑定的全局出口。
		{Key: SettingKeyPluginPortStart, Value: "42000"},
		{Key: SettingKeyPluginPortEnd, Value: "42999"},
		{Key: SettingKeyPluginDefaultEgressID, Value: "0"},
		{Key: SettingKeyPluginHTTPHosts, Value: ""},
		{Key: SettingKeyRouteBalanceEnabled, Value: "false"}, // 加权轮询热路径默认关闭, 行为与既有优先级选路一致
		{Key: SettingKeyAlertWebhookURL, Value: ""},          // 告警 webhook 默认不推送
		{Key: SettingKeyRouteProbeEnabled, Value: "false"},   // 主动探活默认关闭: 探测是真实计费请求, 开不开由用户决定
		{Key: SettingKeyRouteProbeInterval, Value: "300"},    // 探活默认 5 分钟一轮 (低频, 冷却期通常远大于它)
		{Key: SettingKeyAlertChannels, Value: "webhook"},     // 默认只发通用 webhook: 与改造前行为一致
		{Key: SettingKeyAlertFeishuWebhook, Value: ""},
		{Key: SettingKeyAlertDingTalkWebhook, Value: ""},
		{Key: SettingKeyAlertWeComWebhook, Value: ""},
		{Key: SettingKeyAlertServerChanSendKey, Value: ""},
		{Key: SettingKeyAlertSMTPHost, Value: ""},
		{Key: SettingKeyAlertSMTPPort, Value: "587"}, // 587 是 STARTTLS 的通行端口; 465 会走隐式 TLS
		{Key: SettingKeyAlertSMTPUser, Value: ""},
		{Key: SettingKeyAlertSMTPFrom, Value: ""},
		{Key: SettingKeyAlertSMTPTo, Value: ""},
		// 默认不信任任何反向代理：来源 IP 只认 TCP 对端。
		// 这是安全默认值 —— 默认采信 X-Forwarded-For 会让任何客户端都能伪造来源 IP。
		{Key: SettingKeyTrustedProxies, Value: ""},
		// WebDAV 云备份：默认关闭。地址为空时即使 enabled=true 也不会跑（避免无意义的重试噪音）。
		{Key: SettingKeyWebDAVURL, Value: ""},
		{Key: SettingKeyWebDAVUsername, Value: ""},
		{Key: SettingKeyWebDAVEnabled, Value: "false"},
		{Key: SettingKeyWebDAVInterval, Value: "24"}, // 每天一份：备份是防"本机整个坏掉"，不需要更密
		{Key: SettingKeyWebDAVKeep, Value: "7"},      // 默认留 7 份 ≈ 一周，够回溯又不会把远端塞满
		// 加权综合选路（weighted 模式）的维度权重: 保守默认 —— 成本与质量最重, 延迟/在途次之, 近期消耗最轻。
		// 这三行把「哪一维更重要」留给用户: 想要"贵但稳"就把质量调高, 想要"能用最便宜的"就把成本拉满。
		{Key: SettingKeyRouteWeightCost, Value: "30"},
		{Key: SettingKeyRouteWeightQuality, Value: "30"},
		{Key: SettingKeyRouteWeightLatency, Value: "15"},
		{Key: SettingKeyRouteWeightBusy, Value: "15"},
		{Key: SettingKeyRouteWeightLoad, Value: "10"},
		// 计费类维度默认权重: 倍率与余额最重（它们直接决定"还能不能用得起"）, 按次与包月次之。
		{Key: SettingKeyRouteWeightMultiplier, Value: "15"},
		{Key: SettingKeyRouteWeightPerCall, Value: "10"},
		{Key: SettingKeyRouteWeightBalance, Value: "15"},
		{Key: SettingKeyRouteWeightMonthly, Value: "10"},
		{Key: SettingKeyRouteMonthlyAction, Value: "demote"},
		// 额度分压（allocate 模式）的默认值: 全部"开了有用、不开也不出错"的保守取值。
		// 自限流两把默认 0（不限）—— 上游真实限额我们并不知道, 默认替用户设一个数会把请求无故挡下来;
		// 想要"自己先刹车减少 429"的用户再把它们填上即可。
		{Key: SettingKeyRouteAllocateTokens, Value: "1000"},
		{Key: SettingKeyRouteAllocateHealth, Value: "40"},
		{Key: SettingKeyRouteAllocateSlowMs, Value: "0"},
		{Key: SettingKeyRouteSlowLatencyMs, Value: "30000"},
		{Key: SettingKeyRouteAllocateMinReq, Value: "1"},
		{Key: SettingKeyRouteMemberRPMLimit, Value: "0"},
		{Key: SettingKeyRouteMemberTPMLimit, Value: "0"},
		{Key: SettingKeyRouteThrottleCapSecond, Value: "300"},
		// 速度维度 (T-speed-001): 折扣强度与两个"慢"阈值都给非零默认值 —— 用户明确提出"速度不是很好
		// 都要给我做出应对的办法", 因此默认就会动手; 只要把强度改成 0 就能整体关掉速度维度。
		// 首帧看门狗默认 4 倍 / 下限 5 秒: 只对"已经量到首帧的成员"生效, 且结果永远不大于分组配置。
		{Key: SettingKeyRouteSpeedWeight, Value: "40"},
		{Key: SettingKeyRouteSpeedSlowTtfb, Value: "3000"},
		{Key: SettingKeyRouteSpeedSlowTPS, Value: "8"},
		{Key: SettingKeyRouteSpeedFeMultiple, Value: "4"},
		{Key: SettingKeyRouteSpeedFeFloor, Value: "5000"},
		// 入站正文上限 (T-bodylimit-001): 旧行为是依赖库里写死的 64 MiB, 大到 Codex remote compact 会被自己人挡掉;
		// 默认提高到 256 MiB, 并且改成可调（0 = 不限制）。理由与内存代价见常量区注释。
		{Key: SettingKeyRelayMaxRequestBody, Value: "268435456"},
		// 请求本身非法时的取向: 默认与改造前一致 —— 换个成员再试, 全部成员都拒绝才失败。
		// 用户要求两种都要, 因此这里只定默认值, 另一种由使用方显式切换（热生效, 不必重启）。
		{Key: SettingKeyRequestFaultAction, Value: "failover"},
		// 声明式适配器默认全拒: 白名单为空时连注册都会被拒绝（fail closed）。
		{Key: SettingKeyPoolDeclarativeHosts, Value: ""},
		{Key: SettingKeyPoolDeclarativeAdapters, Value: ""},
	}
}

func (s *Setting) Validate() error {
	switch s.Key {
	case SettingKeyModelInfoUpdateInterval:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("model info update interval must be an integer")
		}
		return nil
	case SettingKeyModelFilter:
		if s.Value == "" {
			return nil
		}
		// 与渠道侧一致用 ECMAScript 方言校验, 避免设置能存但探测时编译失败。
		if _, err := regexp2.Compile(s.Value, regexp2.ECMAScript); err != nil {
			return fmt.Errorf("model filter regex is invalid: %w", err)
		}
		return nil
	case SettingKeyProxyCoreAutostart:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("proxy core autostart must be a boolean")
		}
		return nil
	case SettingKeyUsageReportEnabled:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("usage report enabled must be a boolean")
		}
		return nil
	case SettingKeyUsageReportPeriod:
		if !IsValidUsageReportPeriod(s.Value) {
			return fmt.Errorf("usage report period must be one of daily, weekly, monthly")
		}
		return nil
	case SettingKeyUsageReportHour:
		hour, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("usage report hour must be an integer")
		}
		// 越界直接拒绝而不是夹回：用户写 25 时显然是想表达别的意思，
		// 静默改成 23 会让"报告怎么不发"变成一个查不出来的问题。
		if hour < 0 || hour > 23 {
			return fmt.Errorf("usage report hour must be between 0 and 23")
		}
		return nil
	case SettingKeyProxyCorePortStart, SettingKeyProxyCorePortEnd:
		port, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("proxy core port must be an integer")
		}
		// 与 PortRange 的夹回口径一致: 落在特权端口或越界时直接拒绝, 让用户当场看到问题
		if port < 1024 || port > 65535 {
			return fmt.Errorf("proxy core port must be between 1024 and 65535")
		}
		return nil
	case SettingKeyProxyCorePath:
		return nil // 允许为空 (= 用数据目录下的默认位置); 路径是否存在由内核启动时报错说明
	case SettingKeyProxyExitProbeURL:
		if s.Value == "" {
			return nil
		}
		if err := validateHTTPURL(s.Value, "proxy exit probe url"); err != nil {
			return err
		}
		return nil
	case SettingKeyTrustedProxies:
		// 写错一个网段就该当场拒绝：静默忽略会让"明明填了却不生效"变成查不出来的问题，
		// 而这条设置直接决定来源 IP 可不可信，留个坏值比拒绝保存危险得多。
		if _, err := ParseCIDRList(splitNotifyChannels(s.Value)); err != nil {
			return err
		}
		return nil
	case SettingKeyWebDAVURL:
		if s.Value == "" {
			return nil // 允许为空 = 不启用云备份
		}
		// 必须 http(s)：WebDAV 走网络，写个裸域名会让上传在运行时才失败，
		// 而定时任务失败只在日志里、面板上看不到，用户会以为备份一直在跑。
		if !strings.HasPrefix(s.Value, "http://") && !strings.HasPrefix(s.Value, "https://") {
			return fmt.Errorf("webdav url must start with http:// or https://")
		}
		return nil
	case SettingKeyWebDAVUsername:
		return nil // 允许为空：部分 WebDAV 服务用匿名或令牌写在 URL 里
	case SettingKeyWebDAVEnabled:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("webdav enabled must be a boolean")
		}
		return nil
	case SettingKeyWebDAVInterval:
		hours, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("webdav interval must be an integer")
		}
		// 越界直接拒绝而不是夹回：写 0 的人多半想表达"关闭"，静默改成 1 会变成每小时传一次。
		if hours < 1 || hours > 720 {
			return fmt.Errorf("webdav interval must be between 1 and 720 hours")
		}
		return nil
	case SettingKeyWebDAVKeep:
		keep, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("webdav keep must be an integer")
		}
		if keep < 0 || keep > 365 {
			return fmt.Errorf("webdav keep must be between 0 and 365")
		}
		return nil
	case SettingKeyPluginPortStart, SettingKeyPluginPortEnd:
		port, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("plugin port must be an integer")
		}
		if port < 1024 || port > 65535 {
			return fmt.Errorf("plugin port must be between 1024 and 65535")
		}
		return nil
	case SettingKeyPluginDefaultEgressID:
		nodeID, err := strconv.Atoi(s.Value)
		if err != nil || nodeID < 0 {
			return fmt.Errorf("plugin default egress id must be a non-negative integer")
		}
		return nil
	case SettingKeyPluginHTTPHosts:
		return nil // 逗号分隔主机名; 空 = 只允许本机回环, 非法项在启动插件时按条目报错
	case SettingKeyProxyURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("proxy URL is invalid: %w", err)
		}
		validSchemes := map[string]bool{
			"http":    true,
			"https":   true,
			"socks5":  true,
			"socks5h": true,
		}
		if !validSchemes[parsedURL.Scheme] {
			return fmt.Errorf("proxy URL scheme must be http, https, socks5, or socks5h")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("proxy URL must have a host")
		}
		return nil
	case SettingKeyQuotaScanInterval:
		minutes, err := strconv.Atoi(s.Value)
		if err != nil || minutes < 0 {
			return fmt.Errorf("quota scan interval must be a non-negative integer (minutes)")
		}
		return nil
	case SettingKeyQuotaAlertThreshold:
		if s.Value == "" {
			return nil
		}
		threshold, err := strconv.ParseFloat(s.Value, 64)
		if err != nil || threshold < 0 {
			return fmt.Errorf("quota alert threshold must be empty or a non-negative number")
		}
		return nil
	case SettingKeyBalancePointsPerUnit:
		// 换算口径必须为正数: 0 或负数会让总额除出 +Inf/负值, 而不是"用默认值"。
		points, err := strconv.ParseFloat(s.Value, 64)
		if err != nil || points <= 0 {
			return fmt.Errorf("balance points per unit must be a positive number")
		}
		return nil
	case SettingKeyBalanceCurrency:
		if strings.TrimSpace(s.Value) == "" || len(s.Value) > 16 || strings.ContainsAny(s.Value, " \t\n") {
			return fmt.Errorf("balance currency must be a short non-empty token (e.g. USD or CNY)")
		}
		return nil
	case SettingKeyPoolDeclarativeHosts:
		// 只收域名/后缀: 带 scheme、带空格、带路径一律报错——这一项是安全边界, 不接受"看起来像域名"的输入。
		for _, part := range strings.Split(s.Value, ",") {
			host := strings.TrimSpace(part)
			if host == "" {
				continue
			}
			if strings.ContainsAny(host, " \t\n/:\\") {
				return fmt.Errorf("pool declarative hosts must be comma separated host names without scheme or path")
			}
		}
		return nil
	case SettingKeyPoolDeclarativeAdapters:
		// 这一项由号池接口写入整份密文, 这里只挡住明显不合理的形状（换行/超长）, 不做解密校验:
		// 解密校验在 op 层做, 失败时表现为"加载不到适配器", 不影响启动。
		if len(s.Value) > 1<<20 || strings.ContainsAny(s.Value, "\n\r") {
			return fmt.Errorf("pool declarative adapters must be a single-line ciphertext no longer than 1MiB")
		}
		return nil
	case SettingKeyRouteProbeEnabled:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("route probe enabled must be a boolean")
		}
		return nil
	case SettingKeyRouteProbeInterval:
		seconds, err := strconv.Atoi(s.Value)
		if err != nil || seconds < 0 {
			return fmt.Errorf("route probe interval must be a non-negative integer (seconds)")
		}
		return nil
	case SettingKeyRouteWeightCost, SettingKeyRouteWeightQuality, SettingKeyRouteWeightLatency,
		SettingKeyRouteWeightBusy, SettingKeyRouteWeightLoad,
		SettingKeyRouteWeightMultiplier, SettingKeyRouteWeightPerCall,
		SettingKeyRouteWeightBalance, SettingKeyRouteWeightMonthly:
		// 权重只接受 0..100: 越界直接报错而不是静默夹紧, 免得用户以为自己改成了 500。
		weight, err := strconv.Atoi(s.Value)
		if err != nil || weight < 0 || weight > 100 {
			return fmt.Errorf("route weight must be an integer between 0 and 100")
		}
		return nil
	case SettingKeyRouteMonthlyAction:
		// 包月用尽后的动作只有两种: 降权(还能被选到) 或 剔除(不再参与)。默认降权, 见默认值表。
		if s.Value != "demote" && s.Value != "exclude" {
			return fmt.Errorf("route monthly exhausted action must be demote or exclude")
		}
		return nil
	case SettingKeyRouteAllocateTokens, SettingKeyRouteAllocateHealth, SettingKeyRouteAllocateSlowMs,
		SettingKeyRouteAllocateMinReq, SettingKeyRouteMemberRPMLimit, SettingKeyRouteMemberTPMLimit,
		SettingKeyRouteThrottleCapSecond,
		SettingKeyRouteSpeedWeight, SettingKeyRouteSpeedSlowTtfb, SettingKeyRouteSpeedSlowTPS,
		SettingKeyRouteSpeedFeMultiple, SettingKeyRouteSpeedFeFloor,
		SettingKeyRouteSlowLatencyMs:
		// 分压/速度设置全部是非负整数: 越界直接报错而不是静默夹紧, 免得用户以为自己改成了别的数。
		value, err := strconv.Atoi(s.Value)
		if err != nil || value < 0 {
			return fmt.Errorf("route allocate setting must be a non-negative integer")
		}
		// 折扣强度是百分比, 上限 100; 其它几项上界很宽, 由使用侧夹紧。
		if (s.Key == SettingKeyRouteAllocateHealth || s.Key == SettingKeyRouteSpeedWeight) && value > 100 {
			return fmt.Errorf("route allocate health weight must be between 0 and 100")
		}
		return nil
	case SettingKeyRelayMaxRequestBody:
		// 入站正文上限: 非负整数, 单位字节, 0 表示不限制（显式选择, 不当作"没填"）。
		value, err := strconv.ParseInt(s.Value, 10, 64)
		if err != nil || value < 0 {
			return fmt.Errorf("relay max request body bytes must be a non-negative integer")
		}
		return nil
	case SettingKeyRequestFaultAction:
		// 请求本身非法时只有两种取向: 换成员再试(failover, 默认) 或 立刻回上游原文(failfast)。
		// 越界直接报错而不是静默回落, 免得用户以为改成了别的取值。
		if s.Value != "failover" && s.Value != "failfast" {
			return fmt.Errorf("relay request fault action must be failover or failfast")
		}
		return nil
	case SettingKeyAlertWebhookURL:
		return validateHTTPURL(s.Value, "alert webhook URL")
	case SettingKeyAlertFeishuWebhook:
		return validateHTTPURL(s.Value, "feishu webhook URL")
	case SettingKeyAlertDingTalkWebhook:
		return validateHTTPURL(s.Value, "dingtalk webhook URL")
	case SettingKeyAlertWeComWebhook:
		return validateHTTPURL(s.Value, "wecom webhook URL")
	case SettingKeyAlertServerChanSendKey:
		// SendKey 会被拼进推送地址的路径段, 出现空白/斜杠/查询符就得当场拒绝,
		// 否则等到真出事故时收到的是一条 404, 而不是告警。
		return validateSendKey(s.Value)
	case SettingKeyAlertChannels:
		for _, kind := range splitNotifyChannels(s.Value) {
			if !isNotifyChannel(kind) {
				return fmt.Errorf("unknown alert channel %q (want webhook, feishu, dingtalk, wecom, smtp or serverchan)", kind)
			}
		}
		return nil
	case SettingKeyAlertSMTPPort:
		port, err := strconv.Atoi(s.Value)
		if err != nil || port < 0 || port > 65535 {
			return fmt.Errorf("smtp port must be an integer between 0 and 65535")
		}
		return nil
	case SettingKeyAlertSMTPHost:
		if s.Value == "" {
			return nil
		}
		// SMTP 地址只写主机名: 端口是独立设置, 带上 svc:// 或 :port 会构造出无法拨号的地址。
		if strings.ContainsAny(s.Value, ":/ ") {
			return fmt.Errorf("smtp host must be a bare hostname without scheme or port")
		}
		return nil
	case SettingKeyAlertSMTPFrom, SettingKeyAlertSMTPTo:
		return validateMailboxList(s.Value)
	}

	return nil
}

// validateHTTPURL 校验 webhook 类设置: 留空合法(表示未配置), 否则必须是带主机名的 http(s) 地址。
func validateHTTPURL(value, name string) error {
	if value == "" {
		return nil
	}
	parsedURL, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("%s scheme must be http or https", name)
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("%s must include a host", name)
	}
	return nil
}

// validateSendKey 校验 Server酱的 SendKey: 留空合法(表示未配置), 否则必须是一个能放进 URL 路径的令牌。
func validateSendKey(value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsAny(value, " \t/?#&") {
		return fmt.Errorf("serverchan sendkey must not contain spaces, slashes or query characters")
	}
	return nil
}

// splitNotifyChannels 切分渠道设置并去空白; 空串切成空表(表示一个渠道都不发)。
func splitNotifyChannels(value string) []string {
	parts := strings.Split(value, ",")
	kinds := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			kinds = append(kinds, trimmed)
		}
	}
	return kinds
}

func isNotifyChannel(kind string) bool {
	switch kind {
	case "webhook", "feishu", "dingtalk", "wecom", "smtp", "serverchan":
		return true
	}
	return false
}

// validateMailboxList 校验收件人/发件人: 留空合法, 否则每项都必须是形如 a@b 的地址。
func validateMailboxList(value string) error {
	if value == "" {
		return nil
	}
	for _, address := range splitNotifyChannels(value) {
		at := strings.Index(address, "@")
		if at <= 0 || at == len(address)-1 || strings.ContainsAny(address, " \t") {
			return fmt.Errorf("email address %q is invalid", address)
		}
	}
	return nil
}
