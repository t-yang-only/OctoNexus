package health

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/rhttp"
)

// ProbeModelsPath 是 S2/OpenAI/Anthropic 三类共用的模型列表探测端点。
const ProbeModelsPath = "/v1/models"

// ProbeToken 按类别对单个站点做一次只读健康探测。
// token 为探测专用凭据（与转发 key 分离存储，调用方保管）；useProxy 为代理开关。
// Admin-costs 在权限门关闭时直接返回 Healthy=false + 说明，不发起请求。
// 失败只记结果，不阻断转发热路径。
func ProbeToken(ctx context.Context, kind ProbeKind, baseURL, token string, useProxy bool) ProbeResult {
	result := ProbeResult{Kind: kind, BaseURL: baseURL}
	if !ValidProbeKind(kind) {
		result.Error = "unknown probe kind"
		return result
	}
	if kind == ProbeKindAdminCosts {
		if !AdminCostsProbeEnabled {
			result.Error = "admin costs probe disabled: requires official admin permission grant"
			return result
		}
		// 权限门开启后的实现待官方管理权限接入（登记台备注口径），当前仍不可达。
		result.Error = "admin costs probe not implemented"
		return result
	}
	if strings.TrimSpace(token) == "" {
		result.Error = "probe token is empty"
		return result
	}

	var path string
	switch kind {
	case ProbeKindNAToken:
		// New API token 沿用其用户侧自查端点：带 Authorization 的 /api/user/self
		// 可验证 token 有效性与额度可见性（读侧，不写）。
		path = NAUserSelfPath
	case ProbeKindS2Token, ProbeKindOpenAIKey, ProbeKindAnthropicKey:
		path = ProbeModelsPath
	}

	callCtx, cancel := context.WithTimeout(ctx, RelayLoginNewAPITimeoutSeconds*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, joinBalanceURL(baseURL, path), nil)
	if err != nil {
		result.Error = "build request: " + err.Error()
		return result
	}
	switch kind {
	case ProbeKindNAToken, ProbeKindS2Token, ProbeKindOpenAIKey:
		req.Header.Set("Authorization", "Bearer "+token)
	case ProbeKindAnthropicKey:
		req.Header.Set("x-api-key", token)
		req.Header.Set(AnthropicVersionHeader, AnthropicAPIVersion)
	}

	var client *http.Client
	if useProxy {
		client, err = rhttp.Proxy()
	} else {
		client, err = rhttp.Direct()
	}
	if err != nil || client == nil {
		result.Error = "build http client"
		return result
	}

	start := time.Now()
	resp, err := client.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = "request: " + err.Error()
		return result
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	// 2xx 即视为健康：/v1/models 与 /api/user/self 均为只读端点，
	// 能返回 2xx 说明凭据有效且端点可达；响应体只做丢弃前限读。
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Error = "upstream status " + http.StatusText(resp.StatusCode)
		return result
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		result.Error = "read response: " + err.Error()
		return result
	}
	if !probeResponseLooksValid(kind, body) {
		result.Error = "upstream response is not valid JSON"
		return result
	}
	result.Healthy = true
	return result
}

// probeResponseLooksValid 过滤 2xx HTML/空响应，避免错误页面被误判为健康。
// /v1/models 需要 JSON，NA 自查同样需要 JSON；不校验业务字段，兼容不同上游 schema。
func probeResponseLooksValid(kind ProbeKind, body []byte) bool {
	if kind != ProbeKindNAToken && kind != ProbeKindS2Token && kind != ProbeKindOpenAIKey && kind != ProbeKindAnthropicKey {
		return false
	}
	var payload any
	return len(body) > 0 && json.Unmarshal(body, &payload) == nil
}
