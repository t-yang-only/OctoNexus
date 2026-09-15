package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/rhttp"
)

// 告警通知（对标研究 G1/P1-16 的最小可用形态）:
// 只做一个 webhook 出口, payload 为 JSON, 用户侧 n8n/钉钉/飞书/自建服务随便接;
// 不引 api-monitor 的多渠道通知层与重依赖。发送永不 panic、永不阻塞调用方,
// 失败只返回错误由调用方记日志。

// Event 是推送的告警事件。
type Event struct {
	Type      string    `json:"type"`       // 事件类型: quota_alert | quota_zero_stop | route_probe_recovered | notify_test。
	Title     string    `json:"title"`      // 事件标题; 留空时渲染层按 Type 给中文名 (DisplayTitle)。
	Channel   string    `json:"channel"`    // 渠道名。
	ChannelID int       `json:"channel_id"` // 渠道主键。
	Message   string    `json:"message"`    // 人类可读摘要。
	Remaining float64   `json:"remaining"`  // 事件时的剩余额度（余额类事件）。
	Detail    any       `json:"detail,omitempty"`
	At        time.Time `json:"at"`
}

// WebhookURL 读取告警 webhook 地址, 未配置返回空。
func WebhookURL() string {
	url, err := opSettingGet(model.SettingKeyAlertWebhookURL)
	if err != nil {
		return ""
	}
	return url
}

// PostWebhook 向配置的 webhook 推送事件; 未配置时静默跳过。
// 5 秒超时直连, 不走代理设置, 失败不影响调用方主流程。
func PostWebhook(ctx context.Context, event Event) error {
	url := WebhookURL()
	if url == "" {
		return nil
	}
	event.At = time.Now()
	body, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal alert event: %w", err)
	}
	client, err := rhttp.Direct()
	if err != nil {
		return fmt.Errorf("build http client: %w", err)
	}
	sendCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(sendCtx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send webhook: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("webhook returned %d", response.StatusCode)
	}
	return nil
}
