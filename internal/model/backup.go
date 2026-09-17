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

	StatsTotal  []StatsTotal  `json:"stats_total,omitempty"`
	StatsDaily  []StatsDaily  `json:"stats_daily,omitempty"`
	StatsHourly []StatsHourly `json:"stats_hourly,omitempty"`
	StatsAPIKey []StatsAPIKey `json:"stats_api_key,omitempty"`
}

type DBImportResult struct {
	// RowsAffected contains the rows affected for each table operation (insert/upsert depending on table).
	RowsAffected map[string]int64 `json:"rows_affected"`
	// Skipped 是按表统计的"孤儿行"数量: 引用已不存在父行、插进去只会撞外键的行。
	// 导入跳过它们并在此如实回报, 既不静默丢弃也不让整份备份因一行残留而导入失败。
	Skipped map[string]int64 `json:"skipped,omitempty"`
}
