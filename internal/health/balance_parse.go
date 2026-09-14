package health

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

// ParseBalancePayload 宽容解析余额响应体，返回剩余额度三元组。
// new-api 系 /api/user/self 的额度字段别名众多（quota/used/quota_used/
// remain/remaining/balance 等），本函数按"先精确后模糊"匹配，数值可为
// 数字或数字字符串；缺字段时返回 ok=false，调用方只记事件不落库。
// 语义来源：T-research-002 §1 newAPIUserHeaders/inferQuota 的宽容匹配思想，逐行重写。
func ParseBalancePayload(body []byte) (quota, used, remaining float64, ok bool) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return 0, 0, 0, false
	}
	flat := flattenBalanceMap(raw)

	quota, qOK := pickBalanceNumber(flat, []string{"quota", "total_quota", "total"})
	used, uOK := pickBalanceNumber(flat, []string{"used_quota", "quota_used", "used", "total_used"})
	remaining, rOK := pickBalanceNumber(flat, []string{"remain_quota", "quota_remain", "remaining", "remain", "balance", "available"})
	if !qOK && !uOK && !rOK {
		return 0, 0, 0, false
	}
	// 只剩"已用"单字段时无从推知总额/剩余（推导会得到负剩余，下游归零判定会误停用），视为解析失败。
	if !qOK && !rOK {
		return 0, 0, 0, false
	}
	// 剩余额度缺失时由总额推导；总额缺失时由已用+剩余推导。
	if !rOK {
		remaining = quota - used
	}
	if !qOK {
		quota = used + remaining
	}
	return quota, used, remaining, true
}

// FingerprintBalance 对余额三元组做 SHA256 指纹。
// 仅指纹变化时落库/触发事件（P4 口径），避免无意义写入。
func FingerprintBalance(quota, used, remaining float64) string {
	sum := sha256.Sum256([]byte(
		strconv.FormatFloat(quota, 'f', -1, 64) + "|" +
			strconv.FormatFloat(used, 'f', -1, 64) + "|" +
			strconv.FormatFloat(remaining, 'f', -1, 64),
	))
	return hex.EncodeToString(sum[:])
}

// BelowThreshold 判定剩余额度是否跌破阈值。阈值<=0 表示未配置，不触发。
func BelowThreshold(remaining, threshold float64) bool {
	return threshold > 0 && remaining < threshold
}

// flattenBalanceMap 把 data 嵌套层展平一层，供别名匹配。
func flattenBalanceMap(raw map[string]any) map[string]any {
	flat := make(map[string]any, len(raw)+8)
	for k, v := range raw {
		flat[strings.ToLower(k)] = v
	}
	if data, ok := raw["data"]; ok {
		if inner, ok := data.(map[string]any); ok {
			for k, v := range inner {
				flat[strings.ToLower(k)] = v
			}
		}
	}
	return flat
}

// pickBalanceNumber 按别名表取第一个可解析为数字的字段。
func pickBalanceNumber(flat map[string]any, aliases []string) (float64, bool) {
	for _, name := range aliases {
		v, ok := flat[name]
		if !ok {
			continue
		}
		if f, ok := balanceNumber(v); ok {
			return f, true
		}
	}
	return 0, false
}

// balanceNumber 把数字/数字字符串解析为 float64。
func balanceNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		if f, err := n.Float64(); err == nil {
			return f, true
		}
	case string:
		if s := strings.TrimSpace(n); s != "" {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return f, true
			}
		}
	}
	return 0, false
}
