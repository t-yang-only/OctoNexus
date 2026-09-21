package model

// PriceSnapshot 是某个上游站点"模型广场"价格的一次快照（T-price-001）。
//
// 为什么要落库而不是每次现抓：模型广场的价格会变（倍率、阶梯、上下架），
// 用户要看的是"现在各站同一模型实付多少钱"以及"什么时候变的"。
// 单位统一为 USD / 1M token（上游给的是每 token 单价，入库前 ×1e6）。
type PriceSnapshot struct {
	ID         int     `json:"id" gorm:"primaryKey"`
	Site       string  `json:"site" gorm:"index;size:128"`  // 站点 host，如 okai.la
	GroupName  string  `json:"group_name" gorm:"size:128"`  // 分组（= 号池/倍率档）
	Multiplier float64 `json:"multiplier"`                  // 该分组折扣倍率（实付 = 官方 × 倍率）
	Model      string  `json:"model" gorm:"index;size:160"` // 模型名
	Tier       string  `json:"tier" gorm:"size:64"`         // 阶梯档位标签（如 ≤272K）
	// 实付价（已乘倍率）
	InputPrice     float64 `json:"input_price"`
	OutputPrice    float64 `json:"output_price"`
	CacheReadPrice float64 `json:"cache_read_price"`
	// 官方价（未乘倍率），用于面板展示"省了多少"
	OfficialInput  float64 `json:"official_input"`
	OfficialOutput float64 `json:"official_output"`
	BillingMode    string  `json:"billing_mode" gorm:"size:32"`
	CapturedAt     string  `json:"captured_at" gorm:"size:32"`
}

// UsageSnapshot 是某站点用量/Token 统计的一次快照（T-price-002）。
//
// 存这一份的目的就是让用户横向比：同一个模型的 token 统计在不同站是否自洽。
// 实测两个站都出现「缓存读 token 远大于输入 token，而缓存写为 0」——缓存读异常
// 由 op.PriceUsageCompare 统一判定，不由面板各自算（面板只显示结论）。
type UsageSnapshot struct {
	ID               int     `json:"id" gorm:"primaryKey"`
	Site             string  `json:"site" gorm:"index;size:128"`
	Requests         int64   `json:"requests"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	ActualCost       float64 `json:"actual_cost"`
	Balance          float64 `json:"balance"`
	CapturedAt       string  `json:"captured_at" gorm:"size:32"`
}
