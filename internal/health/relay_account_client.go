package health

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"
)

// naLoginPayload 是 New API 登录请求体。
type naLoginPayload struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// NewAPILogin 用账号密码登录 New API 系中转站，返回会话 cookie（client 内含 jar）。
// base 为站点根地址；返回的 client 已带登录态，供后续读侧请求复用。
// 登录失败（非 2xx / body.success != true）返回 ok=false。
func NewAPILogin(ctx context.Context, client *http.Client, base, username, password string) (*http.Client, bool) {
	callCtx, cancel := context.WithTimeout(ctx, RelayLoginNewAPITimeoutSeconds*time.Second)
	defer cancel()
	body, _ := json.Marshal(naLoginPayload{Username: username, Password: password})
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost,
		joinBalanceURL(base, NAUserLoginPath), bytes.NewReader(body))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")

	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, false
	}
	httpClient := client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: RelayLoginNewAPITimeoutSeconds * time.Second}
	}
	httpClient.Jar = jar

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	var ack struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(payload, &ack); err != nil || !ack.Success {
		return nil, false
	}
	return httpClient, true
}

// NewAPISelf 用已登录会话自查用户额度，宽容字段匹配复用 ParseBalancePayload。
// 未登录/会话失效（非 2xx 或缺字段）返回 ok=false。
func NewAPISelf(ctx context.Context, client *http.Client, base string) (RelaySelfSnapshot, bool) {
	var snap RelaySelfSnapshot
	callCtx, cancel := context.WithTimeout(ctx, RelayLoginNewAPITimeoutSeconds*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet,
		joinBalanceURL(base, NAUserSelfPath), nil)
	if err != nil {
		return snap, false
	}
	httpClient := client
	if httpClient == nil {
		return snap, false
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return snap, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return snap, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return snap, false
	}
	quota, used, remaining, ok := ParseBalancePayload(body)
	if !ok {
		return snap, false
	}
	return RelaySelfSnapshot{Quota: quota, Used: used, Remaining: remaining}, true
}

// s2LoginPayload 是 Sub2Api 登录请求体（邮箱密码）。
type s2LoginPayload struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// s2LoginAck 是 Sub2Api 登录响应中 token 的宽容提取形状。
type s2LoginAck struct {
	Token string `json:"token"`
	Data  struct {
		Token string `json:"token"`
	} `json:"data"`
}

// Sub2APILogin 用邮箱密码登录 Sub2Api，返回后续请求用的 bearer token。
// token 别名宽容匹配（顶层 token / data.token），均缺失返回 ok=false。
func Sub2APILogin(ctx context.Context, client *http.Client, base, email, password string) (string, bool) {
	callCtx, cancel := context.WithTimeout(ctx, RelayLoginNewAPITimeoutSeconds*time.Second)
	defer cancel()
	body, _ := json.Marshal(s2LoginPayload{Email: email, Password: password})
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost,
		joinBalanceURL(base, S2LoginPath), bytes.NewReader(body))
	if err != nil {
		return "", false
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: RelayLoginNewAPITimeoutSeconds * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", false
	}
	var ack s2LoginAck
	if err := json.Unmarshal(payload, &ack); err != nil {
		return "", false
	}
	if token := strings.TrimSpace(ack.Token); token != "" {
		return token, true
	}
	if token := strings.TrimSpace(ack.Data.Token); token != "" {
		return token, true
	}
	return "", false
}

// s2JSONBody 收敛 S2 读侧端点的 JSON 有效性：空体/非 JSON（含 2xx 网关 HTML 页）判失败。
// Keys/Subscriptions 的下游解析本就以 JSON 为前提（非 JSON 必报错），此处只是把
// 判定时机从解析阶段提前到传输层；quotas 走 RawMessage 透传，无下游校验，最需要此判定。
// 与 probe_token_client.go 的 anti-200-HTML 同口径（NM-CUR-146）。
func s2JSONBody(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	var payload any
	return json.Unmarshal(trimmed, &payload) == nil
}

// sub2APIKeysAck 是 Keys 列表响应的宽容形状（顶层数组或 data 数组）。
type sub2APIKeysAck struct {
	Items []struct {
		ID     any    `json:"id"`
		Name   string `json:"name"`
		Sk     string `json:"sk"`
		Key    string `json:"key"`
		Status string `json:"status"`
	} `json:"data"`
}

// Sub2APIKeys 用 bearer token 读取用户名下 API Keys 摘要。
// sk 别名宽容匹配（sk/key），ID 兼容数字与字符串。
func Sub2APIKeys(ctx context.Context, client *http.Client, base, token string) ([]RelayAPIKeySummary, bool) {
	body, ok := sub2APIGet(ctx, client, base, S2APIKeysPath, token)
	if !ok {
		return nil, false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		return decodeSub2APIKeyRows(trimmed)
	}
	var ack sub2APIKeysAck
	if err := json.Unmarshal(trimmed, &ack); err != nil {
		return nil, false
	}
	return collectSub2APIKeys(ack.Items), true
}

// decodeSub2APIKeyRows 解析顶层数组形状的 Keys 列表。
func decodeSub2APIKeyRows(body []byte) ([]RelayAPIKeySummary, bool) {
	var list []struct {
		ID     any    `json:"id"`
		Name   string `json:"name"`
		Sk     string `json:"sk"`
		Key    string `json:"key"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, false
	}
	return collectSub2APIKeys(list), true
}

// collectSub2APIKeys 把宽容行形状收敛为读侧摘要。
func collectSub2APIKeys(rows []struct {
	ID     any    `json:"id"`
	Name   string `json:"name"`
	Sk     string `json:"sk"`
	Key    string `json:"key"`
	Status string `json:"status"`
}) []RelayAPIKeySummary {
	keys := make([]RelayAPIKeySummary, 0, len(rows))
	for _, row := range rows {
		sk := row.Sk
		if sk == "" {
			sk = row.Key
		}
		var id int64
		switch v := row.ID.(type) {
		case float64:
			id = int64(v)
		case string:
			fmt.Sscanf(v, "%d", &id)
		}
		keys = append(keys, RelayAPIKeySummary{ID: id, Name: row.Name, Sk: sk, Status: row.Status})
	}
	return keys
}

// Sub2APISubscriptions 用 bearer token 读取用户订阅摘要。
func Sub2APISubscriptions(ctx context.Context, client *http.Client, base, token string) ([]RelaySubscriptionSummary, bool) {
	body, ok := sub2APIGet(ctx, client, base, S2SubscriptionsPath, token)
	if !ok {
		return nil, false
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var list []RelaySubscriptionSummary
		if err := json.Unmarshal(trimmed, &list); err != nil {
			return nil, false
		}
		return list, true
	}
	var ack struct {
		Items []RelaySubscriptionSummary `json:"data"`
	}
	if err := json.Unmarshal(trimmed, &ack); err != nil {
		return nil, false
	}
	return ack.Items, true
}

// Sub2APIPlatformQuotas 用 bearer token 读取 platform quotas 原始 JSON。
// quotas 形状上游未见稳定 schema，原样透传给调用方，由其按需提取。
func Sub2APIPlatformQuotas(ctx context.Context, client *http.Client, base, token string) (json.RawMessage, bool) {
	return sub2APIGet(ctx, client, base, S2PlatformQuotasPath, token)
}

// sub2APIGet 是 S2 读侧 GET 公共路径：bearer 注入 + 2xx + 1MiB 限读 + JSON 有效性。
// 2xx 但响应体为 HTML/空体（网关错误页/登录重定向页）一律判失败，不把垃圾体交给下游。
func sub2APIGet(ctx context.Context, client *http.Client, base, path, token string) ([]byte, bool) {
	callCtx, cancel := context.WithTimeout(ctx, RelayLoginNewAPITimeoutSeconds*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, joinBalanceURL(base, path), nil)
	if err != nil {
		return nil, false
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	httpClient := client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: RelayLoginNewAPITimeoutSeconds * time.Second}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, false
	}
	if !s2JSONBody(body) {
		return nil, false
	}
	return body, true
}
