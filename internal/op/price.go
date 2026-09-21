package op

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 上游站点快照的导入与对比（T-price-001 / T-price-002）。
//
// 数据来源是站点自己的 Web API（登录换 access_token 后读）：
//   - GET /api/v1/model-plaza              → 分组 × 模型 × 定价（含折扣倍率、阶梯）
//   - GET /api/v1/usage/dashboard/stats    → 用量与 token 统计
//   - GET /api/v1/auth/me                  → 余额
//
// 抓取动作不在这里做（登录态、出口、验证码都在采集侧），本文件只负责
// 「把一次抓到的结果落库」与「按用户要看的口径汇总」。这样抓取端可以换
// （脚本 / 采集凭据 / 浏览器），入库口径不变。

// priceImportPayload 是一次抓取的最小载荷，字段与站点返回同构，便于抓取端直接透传。
type priceImportPayload struct {
	Site        string          `json:"site"`
	CapturedAt  string          `json:"captured_at"`
	Plaza       json.RawMessage `json:"plaza"`
	UsageStats  json.RawMessage `json:"usage_stats"`
	Me          json.RawMessage `json:"me"`
	Subsription json.RawMessage `json:"subscriptions"`
}

type plazaResponse struct {
	Groups []struct {
		Name           string  `json:"name"`
		Platform       string  `json:"platform"`
		RateMultiplier float64 `json:"rate_multiplier"`
		Models         []struct {
			Name    string `json:"name"`
			Pricing struct {
				BillingMode     string   `json:"billing_mode"`
				InputPrice      float64  `json:"input_price"`
				OutputPrice     float64  `json:"output_price"`
				CacheReadPrice  float64  `json:"cache_read_price"`
				CacheWritePrice float64  `json:"cache_write_price"`
				PerRequestPrice *float64 `json:"per_request_price"`
				Intervals       []struct {
					TierLabel string `json:"tier_label"`
				} `json:"intervals"`
			} `json:"pricing"`
		} `json:"models"`
	} `json:"groups"`
}

// PriceSnapshotImport 把一次抓取结果落库：价格按「站点×分组×模型×档位」展开，
// 同一 (site, group, model, tier) 只保留最新一条（先删后插，避免历史无限增长；
// 需要看变化时靠 captured_at 判断本次是否为首见）。
func PriceSnapshotImport(ctx context.Context, raw []byte) (int, int, error) {
	var payload priceImportPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return 0, 0, fmt.Errorf("载荷不是合法 JSON: %w", err)
	}
	payload.Site = strings.TrimSpace(payload.Site)
	if payload.Site == "" {
		return 0, 0, fmt.Errorf("缺少 site")
	}
	if payload.CapturedAt == "" {
		payload.CapturedAt = time.Now().Format(time.RFC3339)
	}
	database := db.GetDB().WithContext(ctx)

	priceRows := 0
	if len(payload.Plaza) > 0 {
		var plaza plazaResponse
		if err := json.Unmarshal(payload.Plaza, &plaza); err == nil {
			tx := database.Where("site = ?", payload.Site).Delete(&model.PriceSnapshot{})
			if tx.Error != nil {
				return 0, 0, tx.Error
			}
			for _, group := range plaza.Groups {
				mult := group.RateMultiplier
				if mult <= 0 {
					mult = 1
				}
				for _, item := range group.Models {
					tier := ""
					if len(item.Pricing.Intervals) > 0 {
						tier = item.Pricing.Intervals[0].TierLabel
					}
					row := model.PriceSnapshot{
						Site: payload.Site, GroupName: group.Name, Multiplier: mult,
						Model: item.Name, Tier: tier,
						InputPrice:     item.Pricing.InputPrice * mult * 1e6,
						OutputPrice:    item.Pricing.OutputPrice * mult * 1e6,
						CacheReadPrice: item.Pricing.CacheReadPrice * mult * 1e6,
						OfficialInput:  item.Pricing.InputPrice * 1e6,
						OfficialOutput: item.Pricing.OutputPrice * 1e6,
						BillingMode:    item.Pricing.BillingMode,
						CapturedAt:     payload.CapturedAt,
					}
					if err := database.Create(&row).Error; err != nil {
						return priceRows, 0, err
					}
					priceRows++
				}
			}
		}
	}

	usageRows := 0
	if len(payload.UsageStats) > 0 || len(payload.Me) > 0 {
		var usage struct {
			Requests         int64   `json:"total_requests"`
			InputTokens      int64   `json:"total_input_tokens"`
			OutputTokens     int64   `json:"total_output_tokens"`
			CacheReadTokens  int64   `json:"total_cache_read_tokens"`
			CacheWriteTokens int64   `json:"total_cache_creation_tokens"`
			TotalTokens      int64   `json:"total_tokens"`
			ActualCost       float64 `json:"total_actual_cost"`
		}
		_ = json.Unmarshal(payload.UsageStats, &usage)
		var me struct {
			Balance float64 `json:"balance"`
		}
		_ = json.Unmarshal(payload.Me, &me)
		if err := database.Where("site = ?", payload.Site).Delete(&model.UsageSnapshot{}).Error; err != nil {
			return priceRows, 0, err
		}
		row := model.UsageSnapshot{
			Site: payload.Site, Requests: usage.Requests,
			InputTokens: usage.InputTokens, OutputTokens: usage.OutputTokens,
			CacheReadTokens: usage.CacheReadTokens, CacheWriteTokens: usage.CacheWriteTokens,
			TotalTokens: usage.TotalTokens, ActualCost: usage.ActualCost,
			Balance: me.Balance, CapturedAt: payload.CapturedAt,
		}
		if err := database.Create(&row).Error; err != nil {
			return priceRows, 0, err
		}
		usageRows = 1
	}
	return priceRows, usageRows, nil
}

// PriceModelRow 是面板"价格对比"的一行：同一模型在各站的实付价并排。
type PriceModelRow struct {
	Site           string  `json:"site"`
	GroupName      string  `json:"group_name"`
	Multiplier     float64 `json:"multiplier"`
	Model          string  `json:"model"`
	Tier           string  `json:"tier"`
	InputPrice     float64 `json:"input_price"`
	OutputPrice    float64 `json:"output_price"`
	CacheReadPrice float64 `json:"cache_read_price"`
	OfficialInput  float64 `json:"official_input"`
	OfficialOutput float64 `json:"official_output"`
	Discount       float64 `json:"discount"` // 1 - 倍率，展示"省了多少"
	CapturedAt     string  `json:"captured_at"`
}

// PriceModelList 回全部价格快照（按站点/模型/分组排序），可按模型名过滤。
func PriceModelList(ctx context.Context, keyword string) ([]PriceModelRow, error) {
	query := db.GetDB().WithContext(ctx).Model(&model.PriceSnapshot{})
	if keyword != "" {
		query = query.Where("model LIKE ?", "%"+keyword+"%")
	}
	var rows []model.PriceSnapshot
	if err := query.Order("model asc, site asc, input_price asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]PriceModelRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, PriceModelRow{
			Site: row.Site, GroupName: row.GroupName, Multiplier: row.Multiplier,
			Model: row.Model, Tier: row.Tier,
			InputPrice: row.InputPrice, OutputPrice: row.OutputPrice, CacheReadPrice: row.CacheReadPrice,
			OfficialInput: row.OfficialInput, OfficialOutput: row.OfficialOutput,
			Discount: 1 - row.Multiplier, CapturedAt: row.CapturedAt,
		})
	}
	return out, nil
}

// PriceUsageRow 是"用量对比"的一行，带上异常判定。
type PriceUsageRow struct {
	Site             string  `json:"site"`
	Requests         int64   `json:"requests"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	TotalTokens      int64   `json:"total_tokens"`
	ActualCost       float64 `json:"actual_cost"`
	Balance          float64 `json:"balance"`
	CapturedAt       string  `json:"captured_at"`
	// 异常信号（面板只展示结论，不各自重算）
	AvgCostPerRequest float64 `json:"avg_cost_per_request"`
	CacheReadRatio    float64 `json:"cache_read_ratio"` // 缓存读 / 输入
	Anomaly           string  `json:"anomaly"`          // 空=正常
}

// PriceUsageCompare 回用量快照并逐条判定异常：
//   - 缓存读远超输入、而缓存写为 0 → 上游把普通输入也算成缓存读（计费口径可疑）
//   - 单次请求平均成本异常高（> $0.01）→ 可能有超大请求或重复计费
func PriceUsageCompare(ctx context.Context) ([]PriceUsageRow, error) {
	var rows []model.UsageSnapshot
	if err := db.GetDB().WithContext(ctx).Order("site asc").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]PriceUsageRow, 0, len(rows))
	for _, row := range rows {
		item := PriceUsageRow{
			Site: row.Site, Requests: row.Requests, InputTokens: row.InputTokens,
			OutputTokens: row.OutputTokens, CacheReadTokens: row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens, TotalTokens: row.TotalTokens,
			ActualCost: row.ActualCost, Balance: row.Balance, CapturedAt: row.CapturedAt,
		}
		if row.Requests > 0 {
			item.AvgCostPerRequest = row.ActualCost / float64(row.Requests)
		}
		if row.InputTokens > 0 {
			item.CacheReadRatio = float64(row.CacheReadTokens) / float64(row.InputTokens)
		}
		switch {
		case row.CacheWriteTokens == 0 && item.CacheReadRatio > 1.2:
			item.Anomaly = "缓存读 token 远超输入 token 且缓存写为 0：上游很可能把普通输入计成了缓存读"
		case item.AvgCostPerRequest > 0.01:
			item.Anomaly = "单次请求平均成本偏高：核对是否有超大请求或重复计费"
		}
		out = append(out, item)
	}
	return out, nil
}
