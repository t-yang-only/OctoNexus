package model

type APIKey struct {
	ID              int      `json:"id" gorm:"primaryKey"`
	Name            string   `json:"name" gorm:"not null"`
	APIKey          string   `json:"api_key" gorm:"not null"`
	Enabled         bool     `json:"enabled"`
	ExpireAt        int64    `json:"expire_at,omitempty"`
	MaxCost         float64  `json:"max_cost,omitempty"`
	RPM             int      `json:"rpm,omitempty"`                           // 每分钟请求数上限, 0 表示不限 (litellm parallel_request_limiter 对标)。
	TPM             int      `json:"tpm,omitempty"`                           // 每分钟词元数上限, 0 表示不限。
	SupportedModels []string `json:"supported_models" gorm:"serializer:json"` // 允许访问的分组名称, 空表示不限制; 以 JSON 数组存储, 读写两侧都无需再拆分隔符。
	// AllowedCIDRs 是来源 IP 白名单（单个 IP 或 CIDR 网段），空表示不限制。
	//
	// 只有把它与设置项 trusted_proxies 一起配才有意义：网关默认不信任任何反向代理，
	// 此时 c.ClientIP() 取的是 TCP 对端地址，X-Forwarded-For 一律被忽略，
	// 白名单挡的是真实直连来源；只有当网关确实挂在可信反代后面、并且把该反代地址
	// 填进 trusted_proxies 之后，ClientIP() 才会采信转发头。
	// 反过来说，若不做这层区分就给白名单放行 X-Forwarded-For，任何客户端都能
	// 伪造一个白名单内的 IP 通过校验 —— 那是比不做白名单更糟的虚假安全感。
	// 列名必须显式钉住：GORM 的命名策略把 AllowedCIDRs 拆成了 allowed_c_id_rs
	// （连续大写缩写 CIDR 被当成 C+ID+Rs）。本项目此前在 PID 上也踩过同类问题。
	AllowedCIDRs []string `json:"allowed_cidrs" gorm:"column:allowed_cidrs;serializer:json"`
}
