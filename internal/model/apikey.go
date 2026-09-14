package model

type APIKey struct {
	ID              int      `json:"id" gorm:"primaryKey"`
	Name            string   `json:"name" gorm:"not null"`
	APIKey          string   `json:"api_key" gorm:"not null"`
	Enabled         bool     `json:"enabled" gorm:"default:true"`
	ExpireAt        int64    `json:"expire_at,omitempty"`
	MaxCost         float64  `json:"max_cost,omitempty"`
	RPM             int      `json:"rpm,omitempty"`                           // 每分钟请求数上限, 0 表示不限 (litellm parallel_request_limiter 对标)。
	TPM             int      `json:"tpm,omitempty"`                           // 每分钟词元数上限, 0 表示不限。
	SupportedModels []string `json:"supported_models" gorm:"serializer:json"` // 允许访问的分组名称, 空表示不限制; 以 JSON 数组存储, 读写两侧都无需再拆分隔符。
}
