package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// 本轮（R-balance-002）按实测报告补上的协议族：上游站点里**真的能读到余额**的接口不止
// new-api 的 /api/user/self。实测覆盖 15 家 × 46 把 Key 后确认的三类：
//
//	① GET /user/balance            → {"balance":32.53,"remaining":32.53,"isValid":true,"unit":"USD"}（apikey.fan 自定义；
//	                                  DeepSeek 官方也是这个路径，返回 balance_infos[]）
//	② GET /usage                   → {"cost_usd_used":23.24,"cost_limit_usd":100,"cost_usd_remaining":76.76}（openagents，FastAPI 风格）
//	③ GET /v1/dashboard/billing/{subscription,usage}（new-api/one-api 系 OpenAI Billing 兼容）：
//	      subscription 的 hard_limit_usd 常见占位值（实测 100000000），但 usage 的 total_usage 是**真实用量**（单位美分）。
//
// 口径（沿用 T-balance-001 铁律）：
//   - 只把**可信**的数当余额；占位值绝不进总额（宁可显示"未知"，也不能显示"你有 1 亿美元"）。
//   - 读到用量也算收获：面板要能说清"这家有接口、但额度不可信，已用 $33.97"，而不是笼统的"未读到"。
//   - 依次探测的失败原因要合并成一句话（试过哪些路径、各自什么结果），否则用户无从下手。
const (
	SourceUserBalance   = "user_balance"   // GET /user/balance
	SourceUsage         = "usage"          // GET /usage
	SourceNewAPISelf    = "new_api_self"   // GET /api/user/self
	SourceOpenAIBilling = "openai_billing" // GET /v1/dashboard/billing/*
)

// ReasonQuotaPlaceholder：有接口、有响应，但额度字段是占位值（额度不可信，用量可能可信）。
const ReasonQuotaPlaceholder = "quota_placeholder"

// 余额接口候选路径。顺序即优先级：先试"专门报余额"的接口，再试"顺带报余额"的用量接口，
// 最后才是需要站点 access token 的 /api/user/self 与 OpenAI Billing。
const (
	PathUserBalance     = "/user/balance"
	PathUsage           = "/usage"
	PathNewAPISelf      = "/api/user/self"
	PathBillingSubscrip = "/v1/dashboard/billing/subscription"
	PathBillingUsage    = "/v1/dashboard/billing/usage"
)

// ReadBalanceAuto 依次探测各协议族，返回**最可信**的一份读数。
//
// customPath 是用户在设置里指定的路径（默认 BalanceUserSelfPath）：一旦被显式改过，
// 就先按它读 —— 用户明确知道自己在用什么接口时，不该被自动探测的结果覆盖。
func ReadBalanceAuto(ctx context.Context, baseURL, token string, client *http.Client, customPath string) BalanceRead {
	if client == nil {
		return BalanceRead{ReasonText: "没有可用的出网客户端", ReasonKey: ReasonUnreachable}
	}
	attempts := make([]string, 0, 4)

	if custom := strings.TrimSpace(customPath); custom != "" && custom != BalanceUserSelfPath {
		body, status, err := getJSON(ctx, baseURL, custom, "", token, client)
		if err == nil && status == http.StatusOK {
			if read, ok := parseGenericBalance(body); ok {
				read.Source = SourceNewAPISelf
				return read
			}
		}
		attempts = append(attempts, custom+"="+describeAttempt(status, err))
	}

	// ① /user/balance：专门报余额的接口（DeepSeek 官方口径 / apikey.fan 自定义口径）。
	if body, status, err := getJSON(ctx, baseURL, PathUserBalance, "", token, client); err == nil && status == http.StatusOK {
		if read, ok := parseGenericBalance(body); ok {
			read.Source = SourceUserBalance
			return read
		}
		attempts = append(attempts, PathUserBalance+"=字段不可解析")
	} else {
		attempts = append(attempts, PathUserBalance+"="+describeAttempt(status, err))
	}

	// ② /usage：用量与余额同一接口（openagents 风格）。
	if body, status, err := getJSON(ctx, baseURL, PathUsage, "", token, client); err == nil && status == http.StatusOK {
		if read, ok := parseUsageBalance(body); ok {
			read.Source = SourceUsage
			return read
		}
		attempts = append(attempts, PathUsage+"=字段不可解析")
	} else {
		attempts = append(attempts, PathUsage+"="+describeAttempt(status, err))
	}

	// ③ /api/user/self：new-api 站点管理协议（要控制台 access token，sk- 转发 Key 无权）。
	if body, status, err := getJSON(ctx, baseURL, PathNewAPISelf, "", token, client); err == nil && status == http.StatusOK {
		if read, ok := parseGenericBalance(body); ok {
			read.Source = SourceNewAPISelf
			return read
		}
		attempts = append(attempts, PathNewAPISelf+"=字段不可解析")
	} else {
		attempts = append(attempts, PathNewAPISelf+"="+describeAttempt(status, err))
	}

	// ④ OpenAI Billing 兼容：额度常见占位值，用量可信。
	billing := readOpenAIBilling(ctx, baseURL, token, client, &attempts)
	if billing.OK || billing.Spent > 0 {
		return billing
	}

	return BalanceRead{
		ReasonKey:  worstReason(attempts),
		ReasonText: trimReason("各余额接口都没读到：" + strings.Join(attempts, "；")),
	}
}

// readOpenAIBilling 读 OpenAI Billing 兼容协议：先 subscription 取额度，再 usage 取已用（美分→美元）。
//
// 占位值判定（实测口径）：额度字段落在 1e6 / 1e7 / 1e8 这些整数位、或 >= 1e8 时视为占位 ——
// 中转站普遍把"不限量"写成这类数字。此时**余额不进总额**，但把用量如实带回去。
func readOpenAIBilling(ctx context.Context, baseURL, token string, client *http.Client, attempts *[]string) BalanceRead {
	body, status, err := getJSON(ctx, baseURL, PathBillingSubscrip, "", token, client)
	if err != nil || status != http.StatusOK {
		*attempts = append(*attempts, PathBillingSubscrip+"="+describeAttempt(status, err))
		return BalanceRead{}
	}
	limit, ok := parseBillingLimit(body)
	if !ok {
		*attempts = append(*attempts, PathBillingSubscrip+"=字段不可解析")
		return BalanceRead{}
	}
	spent, spentKnown := readBillingUsage(ctx, baseURL, token, client)

	if isPlaceholderLimit(limit) {
		text := fmt.Sprintf("该站点把额度写成占位值（%g），额度不可信", limit)
		if spentKnown {
			text = fmt.Sprintf("%s；本账号已用 $%.2f（用量可信）", text, spent)
		}
		return BalanceRead{
			Spent: spent, QuotaKnown: false, OK: false, Source: SourceOpenAIBilling,
			ReasonKey: ReasonQuotaPlaceholder, ReasonText: trimReason(text),
		}
	}
	remaining := limit - spent
	if remaining < 0 {
		remaining = 0
	}
	return BalanceRead{
		Quota: limit, Used: spent, Remaining: remaining, OK: true, QuotaKnown: true,
		InCurrency: true, Source: SourceOpenAIBilling,
	}
}

func readBillingUsage(ctx context.Context, baseURL, token string, client *http.Client) (float64, bool) {
	query := "?start_date=" + time.Now().AddDate(0, 0, -30).Format("2006-01-02") + "&end_date=" + time.Now().Format("2006-01-02")
	body, status, err := getJSON(ctx, baseURL, PathBillingUsage, query, token, client)
	if err != nil || status != http.StatusOK {
		return 0, false
	}
	return parseBillingUsage(body)
}

// isPlaceholderLimit 判定额度是否像占位值。
func isPlaceholderLimit(limit float64) bool {
	switch limit {
	case 1e6, 1e7, 1e8, 1e9:
		return true
	}
	return limit >= 1e8
}

// parseGenericBalance 解析"直接报余额"的接口，兼容实测到的两种命名：
//
//	{"balance":32.53,"remaining":32.53,"unit":"USD"}          （apikey.fan）
//	{"balance_infos":[{"total_balance":"12.34",...}]}          （DeepSeek 官方）
//
// 同时兼容 new-api 的 quota/used_quota（点）口径 —— 走既有的 ParseBalancePayload。
func parseGenericBalance(body []byte) (BalanceRead, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return BalanceRead{}, false
	}
	// 先认"钱"的字段名，再退回 new-api 的点口径。
	//
	// 顺序不能反：apikey.fan 返回的 `{"balance":32.53,"remaining":32.53,"unit":"USD"}` 里
	// `balance` 会被点口径的宽容解析也认下来，于是 32.53 美元被当成 32.53 点再除 500000，
	// 面板显示 6.5e-05（实测就是这么错的）。钱的名字优先，歧义才按点算。
	for _, key := range []string{"remaining", "balance", "total_available", "total_balance"} {
		if value, ok := numberField(payload, key); ok {
			return BalanceRead{Quota: value, Remaining: value, OK: true, QuotaKnown: true, InCurrency: true}, true
		}
	}
	if infos, ok := payload["balance_infos"].([]any); ok {
		for _, item := range infos {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if value, ok := numberField(entry, "total_balance"); ok {
				return BalanceRead{Quota: value, Remaining: value, OK: true, QuotaKnown: true, InCurrency: true}, true
			}
			if value, ok := numberField(entry, "balance"); ok {
				return BalanceRead{Quota: value, Remaining: value, OK: true, QuotaKnown: true, InCurrency: true}, true
			}
		}
	}
	// new-api 的 quota/used_quota 是"点"，按 balance_points_per_unit 折算，因此 InCurrency=false。
	if quota, used, remaining, ok := ParseBalancePayload(body); ok {
		return BalanceRead{Quota: quota, Used: used, Remaining: remaining, OK: true, QuotaKnown: true}, true
	}
	return BalanceRead{}, false
}

// parseUsageBalance 解析"用量+余额同一接口"（openagents 风格）：
//
//	{"cost_usd_used":23.24,"cost_limit_usd":100.0,"cost_usd_remaining":76.755}
//
// 只要拿到 remaining 就算成功（它才是"还剩多少"）；只有 used+limit 时也能算出剩余。
func parseUsageBalance(body []byte) (BalanceRead, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return BalanceRead{}, false
	}
	used, usedOK := numberField(payload, "cost_usd_used")
	limit, limitOK := numberField(payload, "cost_limit_usd")
	remaining, remainingOK := numberField(payload, "cost_usd_remaining")
	if !remainingOK {
		remaining, remainingOK = numberField(payload, "remaining")
	}
	switch {
	case remainingOK:
		if !limitOK && usedOK {
			limit = remaining + used
		}
		return BalanceRead{Quota: limit, Used: used, Remaining: remaining, OK: true, QuotaKnown: true, InCurrency: true}, true
	case limitOK && usedOK:
		return BalanceRead{Quota: limit, Used: used, Remaining: limit - used, OK: true, QuotaKnown: true, InCurrency: true}, true
	default:
		return BalanceRead{}, false
	}
}

// parseBillingLimit 取 OpenAI Billing subscription 的额度（hard_limit_usd / soft_limit_usd）。
func parseBillingLimit(body []byte) (float64, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, false
	}
	if value, ok := numberField(payload, "hard_limit_usd"); ok {
		return value, true
	}
	if value, ok := numberField(payload, "soft_limit_usd"); ok {
		return value, true
	}
	return 0, false
}

// parseBillingUsage 取 OpenAI Billing usage 的已用金额：new-api 系的 total_usage 单位是**美分**。
func parseBillingUsage(body []byte) (float64, bool) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return 0, false
	}
	if value, ok := numberField(payload, "total_usage_usd"); ok {
		return value, true
	}
	if value, ok := numberField(payload, "total_usage"); ok {
		return value / 100, true
	}
	return 0, false
}

// numberField 宽容地取一个数值字段：上游有的用数字、有的用字符串（DeepSeek 的 total_balance 就是字符串）。
func numberField(payload map[string]any, key string) (float64, bool) {
	value, ok := payload[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		var parsed float64
		if _, err := fmt.Sscanf(trimmed, "%f", &parsed); err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

// getJSON 发一次带 Bearer 的 GET，返回正文与状态码（状态码 0 表示请求就没发出去）。
func getJSON(ctx context.Context, baseURL, path, query, token string, client *http.Client) ([]byte, int, error) {
	if client == nil {
		return nil, 0, fmt.Errorf("没有可用的出网客户端")
	}
	endpoint := joinBalanceURL(baseURL, path) + query
	callCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	return body, resp.StatusCode, nil
}

// describeAttempt 用最短的形式描述一次探测结果（`路径=结果`）：
// 失败文案要在一行里说清"试过哪些路径、各自什么结果"，写长了会被 trimReason 截断，
// 把最后那条（往往正是最关键的 OpenAI Billing 结果）切掉。
func describeAttempt(status int, err error) string {
	switch {
	case err != nil:
		return "网络不可达"
	case status == http.StatusOK:
		return "200"
	default:
		return fmt.Sprintf("%d", status)
	}
}

// worstReason 把多次探测合并成一个原因码：凭据问题优先于"接口不存在"（前者用户能改，后者只能换路子）。
func worstReason(attempts []string) string {
	joined := strings.Join(attempts, " ")
	switch {
	case strings.Contains(joined, "401") || strings.Contains(joined, "403"):
		return ReasonUnauthorized
	case strings.Contains(joined, "网络不可达"):
		return ReasonUnreachable
	case strings.Contains(joined, "404") || strings.Contains(joined, "405"):
		return ReasonEndpointGone
	default:
		return ReasonUnparsable
	}
}
