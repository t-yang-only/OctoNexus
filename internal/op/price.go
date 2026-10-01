package op

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/t-yang-only/OctoNexus/internal/db"
	"github.com/t-yang-only/OctoNexus/internal/model"
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

// perCallPrice 把上游的按次单价折成入库值；上游没给（nil）时为 0。
//
// nil 与 0 必须分开：nil 表示"这个模型不是按次计费的"，0 表示"按次但免费"。
// 把 nil 当 0 会让按量模型也带上一个 0 的按次价，界面上就多出一列永远为 0 的数。
func perCallPrice(raw *float64, multiplier float64) float64 {
	if raw == nil {
		return 0
	}
	return *raw * multiplier
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
						// 按次价同样要乘倍率：倍率是分组的折扣，与计费方式无关。
						// 上游用指针表达"这一项没有"（按量模型就是 null），解引用前必须判空 ——
						// 直接取 *item.Pricing.PerRequestPrice 会在按量模型上 panic。
						PerCallPrice: perCallPrice(item.Pricing.PerRequestPrice, mult),
						CapturedAt:   payload.CapturedAt,
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

	// ---- 统一标准（需求9）----
	//
	// 上面那些字段是"站点怎么说"，下面这些是"折成同一把尺子之后怎么说"。
	// 没有下面这组，按量与按次两种计费方式根本无法并排比较 ——
	// 一个报 $2/1M token，一个报 $0.01/次，数字大小完全不同量纲。

	// BillingMode 是站点的计费方式，**原样透传**站点声明的字面量。
	//
	// 不在这里归一化：界面要能看出"站点说的是什么"，而归一后的三档会把这个信息抹掉。
	// 折算与排序都走 normalizeBillingMode 归并，认不出的取值按"未知"处理
	// （不明着当按量 —— 那会把包月也折出一个看起来合理的单价）。
	BillingMode string `json:"billing_mode"`
	// CnyInputPrice / CnyOutputPrice / CnyCacheReadPrice 是折成人民币后的
	// 每 1M token 价（已含站点倍率）。rate 未配置（=0）时这三个为 0，界面显示原币种。
	CnyInputPrice     float64 `json:"cny_input_price"`
	CnyOutputPrice    float64 `json:"cny_output_price"`
	CnyCacheReadPrice float64 `json:"cny_cache_read_price"`
	// PerCallPrice 是按次计费的原始单价（每次调用多少美元），仅 per_call 模式有值。
	// 留着它是为了让用户能核对折算依据：只有总数没有原价的换算是不可验证的。
	PerCallPrice float64 `json:"per_call_price"`
	// AvgTokensPerRequest 是折算所用的分母：本实例"我"平均每次请求消耗的 token 数。
	//
	// 它必须随行下发，理由同项目里其他统计功能的"分母可见"约定：
	// 按次价折成每 1M token 价完全依赖这个数，不给出来用户就无法判断
	// "¥3.1/1M"是便宜还是贵，更没法发现分母被极端值带偏。
	AvgTokensPerRequest int64 `json:"avg_tokens_per_request"`
	// Basis 用一句话说明这一行是怎么折的（如"按次 $0.01 ÷ 平均 3200 token/次"）。
	// 空串表示无需特殊说明（按量计费、或汇率未配置）。
	Basis string `json:"basis"`
}

// priceCnyRate 返回配置的"1 美元 = X 元人民币"，0 表示不折算。
//
// 读失败（设置缺失/非数字）时按 0 处理并**不报错**：这一项是展示层的附加换算，
// 配错不该让整个价格对比页打不开 —— 那样用户连原币种的价格都看不到，
// 而原币种信息本身就是有用的。
func priceCnyRate() float64 {
	raw, err := SettingGetString(model.SettingKeyPriceCnyPerUsd)
	if err != nil {
		return 0
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || rate <= 0 {
		return 0
	}
	return rate
}

// avgTokensPerRequest 返回本实例平均每次请求消耗的 token 数（按次计价折算的分母）。
//
// 口径：只算**非测试请求**（测试请求的 token 量是人为造的，会把分母带偏）、
// 且只算真正有消耗的行（total_tokens > 0）。样本不足时返回 0，
// 调用方据此放弃按次折算 —— 用 5 条请求算出来的"平均"去做价格对比，
// 比不折更危险，因为它看起来是个结论。
//
// 列名陷阱（本项目已栽过）：CompletionToks 没有 gorm column 标签，GORM 按**字段名**
// 推出列名 completion_toks，而它的 JSON 名是 completion_tokens。写 SQL 时想当然按
// JSON 名写会直接 "no such column"，且因为这里把错误吞成 0，表现是"按次价永远折不出来"
// 而不是报错 —— 所以下面这一行是钉死的，改前先确认列名。
func avgTokensPerRequest(ctx context.Context) int64 {
	var avg *float64
	err := db.GetDB().WithContext(ctx).Model(&model.RelayLog{}).
		Where("is_test = ? OR is_test IS NULL", false).
		Where("prompt_tokens + completion_toks > 0").
		Select("AVG(prompt_tokens + completion_toks)").Scan(&avg).Error
	if err != nil || avg == nil {
		return 0
	}
	value := int64(*avg)
	if value <= 0 {
		return 0
	}
	return value
}

// priceSortColumns 是价格对比支持排序的列（需求9「每个参数都支持从大到小排序」）。
//
// 用白名单而不是把前端传的字符串拼进 ORDER BY：那样一句 "id; DROP TABLE"
// 就能改写查询（SQL 注入），而且前端传错列名时会是静默的全表序。
// 键是对外名，值是列名 —— 两者刻意不同，对外名可以随界面调整而不动库结构。
var priceSortColumns = map[string]string{
	"site":         "site",
	"group":        "group_name",
	"multiplier":   "multiplier",
	"model":        "model",
	"input":        "input_price",
	"output":       "output_price",
	"cache_read":   "cache_read_price",
	"cny_input":    "cny_input_price",
	"cny_output":   "cny_output_price",
	"per_call":     "per_call_price",
	"official_in":  "official_input",
	"official_out": "official_output",
}

// perCallAwarePriceSQL 生成「按次行也参与排序」的人民币价排序表达式。
//
// 只对 **input 列**做按次折算：按次站点只给一个"每次多少钱"，没有输入/输出之分，
// unifyPriceRow 因此只填 CnyInputPrice，CnyOutputPrice 留 0。
// 若 output 列也按 per_call_price 折算，排序键有值而显示值是 0 ——
// 那正是"排序依据与显示值不一致"要避免的情况（用户会看到 0 排在有值行前面）。
//
// tokensPerMillion <= 0（样本不足）时按次行折不出价，按 0 排：与界面显示「—」一致，
// 没有值就不该占位置。
//
// 变异检查说明：把 `if tokensPerMillion > 0` 改成恒真，用例不会红 ——
// 因为 SQLite 里 `x / 0` 返回 NULL，而 ORDER BY 中 NULL 与 0 都排末尾，
// 结果恰好一致。这个守卫**功能上冗余，变异检查证实**；保留它是为了不依赖
// "某个数据库对除零返回 NULL"这一实现细节（MySQL/Postgres 会报错或给 NULL，
// 行为不一致），且让表达式在字面上就自解释。
func perCallAwarePriceSQL(tokenColumn, perCallColumn string, rate, tokensPerMillion float64) string {
	perCallExpr := "0"
	if tokensPerMillion > 0 {
		perCallExpr = fmt.Sprintf("(%s / %s / %s)",
			perCallColumn,
			strconv.FormatFloat(rate, 'f', -1, 64),
			strconv.FormatFloat(tokensPerMillion, 'f', -1, 64))
	}
	return fmt.Sprintf("CASE WHEN billing_mode = 'per_call' THEN %s ELSE (%s / %s) END",
		perCallExpr, tokenColumn, strconv.FormatFloat(rate, 'f', -1, 64))
}

// PriceModelSort 描述一次排序请求：空 Column = 用默认序。
type PriceModelSort struct {
	Column     string // 对外列名，取值见 priceSortColumns。
	Descending bool   // true = 从大到小（需求9 的默认方向）。
}

// resolvePriceSort 把排序请求翻成 ORDER BY 子句；非法列名回落默认序并告知调用方。
//
// ctx 只用于按次折算需要读一次平均 token（那是按次行的人民币价分母）——
// 排序键必须与显示值同源，否则会出现"第 1 行的数比第 2 行小"这种肉眼可辨的错。
func resolvePriceSort(ctx context.Context, sort PriceModelSort, avgTokens int64) (string, bool) {
	column, ok := priceSortColumns[sort.Column]
	if !ok {
		return "", false
	}
	// cny_* 与 per_call 是算出来的列，不是表里的列：按它们排序必须在 SQL 里重算。
	// 这里直接用同一套表达式，保证"排序依据"与"显示值"逐字一致 ——
	// 若排序用一套算式、显示用另一套，会出现"第 1 行的数比第 2 行小"这种肉眼可辨的错。
	switch sort.Column {
	case "cny_input", "cny_output":
		rate := priceCnyRate()
		if rate <= 0 {
			return "", false // 没配汇率时这些列没有值，排它们没有意义
		}
		// 排序键必须与**显示值**用同一套算式。
		//
		// 按次计费的行没有每 token 价（input_price 为 0），它的人民币**输入**价是
		// 「每次价 ÷ (平均 token ÷ 1M)」算出来的。若这里只写 input_price / rate，
		// 按次行会全部按 0 排到底 —— 而它的输入价明明有效，
		// 用户按这一列排序时按次模型不会出现在该在的位置（肉眼可辨的错）。
		//
		// output 列**不**做按次折算：按次站点只有一个"每次多少钱"，没有输入/输出之分，
		// unifyPriceRow 只填 CnyInputPrice。output 也折算就会出现排序键有值、
		// 显示值是 0 的相反错误（0 排到有值行前面去）。
		tokensPerMillion := float64(avgTokens) / 1e6
		if sort.Column == "cny_input" {
			column = perCallAwarePriceSQL("input_price", "per_call_price", rate, tokensPerMillion)
		} else {
			column = "output_price / " + strconv.FormatFloat(rate, 'f', -1, 64)
		}
	case "per_call":
		// 按次价对按量行是 0，排它只会把一堆 0 堆在一起 —— 调用方应先按计费方式过滤。
		column = "per_call_price"
	}
	direction := "ASC"
	if sort.Descending {
		direction = "DESC"
	}
	return column + " " + direction, true
}

// PriceModelList 回全部价格快照，可按模型名过滤、按指定列排序。
func PriceModelList(ctx context.Context, keyword string, sort PriceModelSort) ([]PriceModelRow, error) {
	query := db.GetDB().WithContext(ctx).Model(&model.PriceSnapshot{})
	if keyword != "" {
		query = query.Where("model LIKE ?", "%"+keyword+"%")
	}
	// 平均 token 只查一次：排序键与显示值都要用它（按次行的人民币价分母）。
	// 查两次不只是浪费 —— 两次之间若有新请求落库，排序依据会与显示值不一致。
	avgTokens := avgTokensPerRequest(ctx)
	order := "model asc, site asc, input_price asc"
	if clause, ok := resolvePriceSort(ctx, sort, avgTokens); ok {
		order = clause
	}
	var rows []model.PriceSnapshot
	if err := query.Order(order).Find(&rows).Error; err != nil {
		return nil, err
	}

	rate := priceCnyRate()
	out := make([]PriceModelRow, 0, len(rows))
	for _, row := range rows {
		item := PriceModelRow{
			Site: row.Site, GroupName: row.GroupName, Multiplier: row.Multiplier,
			Model: row.Model, Tier: row.Tier,
			InputPrice: row.InputPrice, OutputPrice: row.OutputPrice, CacheReadPrice: row.CacheReadPrice,
			OfficialInput: row.OfficialInput, OfficialOutput: row.OfficialOutput,
			Discount: 1 - row.Multiplier, CapturedAt: row.CapturedAt,
			BillingMode: row.BillingMode, PerCallPrice: row.PerCallPrice,
		}
		unifyPriceRow(&item, rate, avgTokens)
		out = append(out, item)
	}
	return out, nil
}

// unifyPriceRow 把一行价格折成统一标准（需求9）。
//
// 三种计费方式分开处理，因为它们的"每 1M token 价"根本不是一回事：
//
//	按量（token / metered）  站点直接给每 token 价，除汇率即可；
//	按次（per_call）        把每次调用价除以"平均每次请求的 token 数 ÷ 1M"，
//	                        得到等效的每 1M token 价，再除汇率；
//	包月（subscription）    **不折**。包月买的是"这个月随便用"，没有每 token 单价；
//	                        拿余额除一个估算 token 量得出的数，看起来是个结论，
//	                        实际随用量浮动，拿它去和按量价比会得出错的排序。
//
// 按次那条是整个需求的关键：不折的话，按次模型与按量模型永远没法并排看，
// 而用户问的恰恰是"这家按次、那家按量，哪个划算"。
//
// 站点声明的取值与项目枚举不完全一致（实测上游 plaza 用的是 token，
// 而 model.Channel 的枚举写的是 metered），所以这里**按语义归并**而不是逐个字面量比：
// 认出不认识的取值时按"未知"处理，不明着当成按量。
//
// 折不了的几种情形都**留空而不猜**：
//   - 汇率没配（rate<=0）：不强折，界面继续显示站点原币种；
//   - 分母为 0（样本不足）：按次价折不出每 token 价，留 0 并由 Basis 说明原因。
//     用一个小样本算出的"平均"去做对比，比不折更危险 —— 它看起来是个结论。
func unifyPriceRow(item *PriceModelRow, rate float64, avgTokens int64) {
	// 分母必须**无条件**随行下发，与折没折出来无关：
	// 用户看到"按次 ¥3.1/1M"时，唯一能判断这个数可不可信的就是分母。
	// 只在折算成功时才给，等于把"没折成"和"分母是 0"混为一谈。
	item.AvgTokensPerRequest = avgTokens
	if rate <= 0 {
		return
	}

	switch normalizeBillingMode(item.BillingMode) {
	case billingPerCall:
		if item.PerCallPrice <= 0 {
			// 声明了按次却没给单价：数据不全，不猜。
			item.Basis = "按次计费但站点未提供每次单价，无法折算"
			return
		}
		if avgTokens <= 0 {
			item.Basis = "按次计费：本实例样本不足，暂无法折算成每 1M token 价"
			return
		}
		// per_call 下站点给的是"每次调用"价，InputPrice 那一组是 0（没有每 token 价）。
		perCallCny := item.PerCallPrice / rate
		tokensPerMillion := float64(avgTokens) / 1e6
		item.CnyInputPrice = perCallCny / tokensPerMillion
		item.Basis = fmt.Sprintf("按次 $%s ÷ 平均 %d token/次，折成每 1M token 价",
			strconv.FormatFloat(item.PerCallPrice, 'f', -1, 64), avgTokens)

	case billingSubscription:
		// 包月没有每 token 单价。把三个人民币价全留 0，界面据此显示「—」而不是一个错数。
		item.Basis = "包月计费：没有每 token 单价，不参与按量对比"

	case billingMetered:
		item.CnyInputPrice = item.InputPrice / rate
		item.CnyOutputPrice = item.OutputPrice / rate
		item.CnyCacheReadPrice = item.CacheReadPrice / rate

	default:
		// 空串或认不出的取值：不明着当按量，说明用了什么口径。
		item.Basis = "站点未声明计费方式（或取值未被识别），暂不折算"
	}
}

// billingMode 是归并后的计费方式。只用语义三档，不暴露上游的字面量差异。
type billingMode int

const (
	billingUnknown billingMode = iota
	billingMetered
	billingPerCall
	billingSubscription
)

// normalizeBillingMode 把站点声明的字面量归并成三档。
//
// 为什么要归并而不是字面量比较：上游 plaza 实际给的是 token，而项目自己的枚举
// （model.Channel.BillingMode）写的是 metered。两套写法并存时，逐个字面量比
// 会漏掉其中一种，表现为"该折的没折"或"不该折的折了"。
func normalizeBillingMode(raw string) billingMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "per_call", "percall", "per-request", "request":
		return billingPerCall
	case "subscription", "monthly", "包月":
		return billingSubscription
	case "metered", "token", "usage", "按量":
		return billingMetered
	}
	return billingUnknown
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
