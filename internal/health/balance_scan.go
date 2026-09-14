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

// FetchBalance 对单个渠道做一次余额采集（只读，不停用）。
// baseURL 取渠道 BaseURL；monitorToken 为监控专用凭证，与转发 key 分离存储
// （P2 口径：监控凭证不复用转发凭据）；useProxy 为渠道代理开关。
// 端点失败/解析失败时返回 ok=false，调用方只记事件，不阻断转发。
func FetchBalance(ctx context.Context, baseURL, monitorToken string, useProxy bool) (quota, used, remaining float64, ok bool) {
	endpoint := joinBalanceURL(baseURL, BalanceUserSelfPath)
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, 0, 0, false
	}
	if monitorToken != "" {
		req.Header.Set("Authorization", "Bearer "+monitorToken)
	}

	var client *http.Client
	if useProxy {
		client, err = rhttp.Proxy()
	} else {
		client, err = rhttp.Direct()
	}
	if err != nil || client == nil {
		return 0, 0, 0, false
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, 0, 0, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, 0, 0, false
	}
	q, u, r, ok := ParseBalancePayload(body)
	if !ok {
		return 0, 0, 0, false
	}
	return q, u, r, true
}

// ScanOneChannel 采集单个渠道并做指纹变化判定与阈值判定。
// lastFingerprint 为上次指纹（空表示首次）；threshold<=0 表示未配置阈值。
// 返回快照（Changed 指示指纹是否变化）与可选的阈值事件（nil 表示未触发）。
func ScanOneChannel(ctx context.Context, channelID int, baseURL, monitorToken string, useProxy bool, lastFingerprint string, threshold float64) (BalanceSnapshot, *ThresholdEvent) {
	snap := BalanceSnapshot{ChannelID: channelID}
	q, u, r, ok := FetchBalance(ctx, baseURL, monitorToken, useProxy)
	if !ok {
		return snap, nil
	}
	snap.Quota, snap.Used, snap.Remaining = q, u, r
	fp := FingerprintBalance(q, u, r)
	snap.Changed = fp != lastFingerprint
	if BelowThreshold(r, threshold) {
		return snap, &ThresholdEvent{ChannelID: channelID, Remaining: r, Threshold: threshold}
	}
	return snap, nil
}

// joinBalanceURL 拼接 base 与相对路径，避免双斜杠。
func joinBalanceURL(base, path string) string {
	if base == "" {
		return path
	}
	baseSlash := strings.HasSuffix(base, "/")
	pathSlash := strings.HasPrefix(path, "/")
	switch {
	case baseSlash && pathSlash:
		return base + path[1:]
	case !baseSlash && !pathSlash:
		return base + "/" + path
	default:
		return base + path
	}
}

// balanceProbePayload 仅供单测构造宽容字段样本，保持与上游形状解耦。
func balanceProbePayload(fields map[string]any) []byte {
	b, _ := json.Marshal(fields)
	return b
}
