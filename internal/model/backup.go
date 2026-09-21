package model

import "time"

// DBDump is a full-database JSON export format for Octopus.
// Import uses incremental semantics (insert new rows, and upsert on tables with natural keys).
type DBDump struct {
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exported_at"`

	Channels      []Channel      `json:"channels,omitempty"`       // 渠道数据。
	ChannelKeys   []ChannelKey   `json:"channel_keys,omitempty"`   // 渠道凭据数据。
	ChannelModels []ChannelModel `json:"channel_models,omitempty"` // 渠道模型数据。
	ChannelGrants []ChannelGrant `json:"channel_grants,omitempty"` // 渠道授权数据, 含统计。
	Groups        []Group        `json:"groups,omitempty"`         // 分组数据。
	GroupItems    []GroupItem    `json:"group_items,omitempty"`    // 分组成员数据。
	LLMInfos      []LLMInfo      `json:"llm_infos,omitempty"`      // 模型价格数据。
	APIKeys       []APIKey       `json:"api_keys,omitempty"`       // API Key 数据。
	Settings      []Setting      `json:"settings,omitempty"`       // 系统设置数据。

	// 出口节点池、官方账号与手动订阅这三类此前的转储里没有: 导出再导入会静默丢掉节点池与订阅
	// （channels.proxy_node_id 仍会被导出, 恢复后指向不存在的节点 —— 转发 fail-closed 直接报错）、
	// 官方账号（要逐个重新授权）与手动订阅（采购信息）。四张表一并往返。
	ProxyNodes          []ProxyNodeExport         `json:"proxy_nodes,omitempty"`
	ProxySubscriptions  []ProxySubscriptionExport `json:"proxy_subscriptions,omitempty"`
	OfficialAccounts    []OfficialAccountExport   `json:"official_accounts,omitempty"`
	ManualSubscriptions []ManualSubscription      `json:"manual_subscriptions,omitempty"`

	StatsTotal  []StatsTotal  `json:"stats_total,omitempty"`
	StatsDaily  []StatsDaily  `json:"stats_daily,omitempty"`
	StatsHourly []StatsHourly `json:"stats_hourly,omitempty"`
	StatsAPIKey []StatsAPIKey `json:"stats_api_key,omitempty"`
}

// ProxyNodeExport / ProxySubscriptionExport / OfficialAccountExport 是"带上密文"的转储形状。
//
// 三个模型都把凭据字段标成 json:"-"（防止接口响应泄露凭据），而备份必须把密文带上：密文本身
// 是安全的（AAD 隔离、换过 credential.key 解不开会如实告警），而少带一列的后果是恢复出来的
// 节点/账号"在库里、但没有凭据"，表现为探活失败、转发报错，且完全看不出是备份缺字段。
// 外层字段 Go 名与 JSON 名都和模型里的 json:"-" 字段不同（无遮蔽歧义），且这些类型只用于 JSON、
// 不进 GORM，因此不会影响接口出参与表结构。
type ProxyNodeExport struct {
	ProxyNode
	ParamsOut string `json:"params,omitempty"` // 密文：节点参数
}

type ProxySubscriptionExport struct {
	ProxySubscription
	URLCipherOut string `json:"url_cipher,omitempty"` // 密文：订阅地址
}

type OfficialAccountExport struct {
	OfficialAccount
	AccessCipherOut  string `json:"access_cipher,omitempty"`  // 密文：访问凭据
	RefreshCipherOut string `json:"refresh_cipher,omitempty"` // 密文：刷新凭据
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table operation (insert/upsert depending on table).
	RowsAffected map[string]int64 `json:"rows_affected"`
	// Skipped 是按表统计的"孤儿行"数量: 引用已不存在父行、插进去只会撞外键的行。
	// 导入跳过它们并在此如实回报, 既不静默丢弃也不让整份备份因一行残留而导入失败。
	Skipped map[string]int64 `json:"skipped,omitempty"`
	// Warnings 是"导入成功、但有需要人处理的地方"的如实回报
	// （当前只有一种: 备份里带密文、却用本实例密钥解不开的渠道凭据条数）。
	Warnings []string `json:"warnings,omitempty"`
}
