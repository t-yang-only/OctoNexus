package model

import (
	"fmt"
	"net/url"
	"strconv"

	"github.com/dlclark/regexp2"
)

type SettingKey string

const (
	SettingKeyProxyURL                SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval       SettingKey = "stats_save_interval"        // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval SettingKey = "model_info_update_interval" // 模型信息更新间隔(小时)
	SettingKeyCORSAllowOrigins        SettingKey = "cors_allow_origins"         // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyModelFilter             SettingKey = "model_filter"               // 渠道获取模型时的全局过滤表达式; 留空表示不过滤
	SettingKeyQuotaScanInterval       SettingKey = "quota_scan_interval"        // 余额采集扫描周期(分钟), T-quota-001; 0 表示停用扫描任务
	SettingKeyQuotaAlertThreshold     SettingKey = "quota_alert_threshold"      // 余额告警阈值(额度点), 剩余额度低于该值记告警事件; 留空或<=0 表示不告警 (归零停用不受其影响, 恒按 remaining<=0 判定)
	SettingKeyRouteBalanceEnabled     SettingKey = "route_balance_enabled"      // 故障转移分组是否用加权轮询定序候选 (T-route-002); 默认关闭, 走原有优先级选路
	SettingKeyAlertWebhookURL         SettingKey = "alert_webhook_url"          // 告警事件 webhook 地址, 留空不推送; 余额告警/归零停用等事件 POST JSON 到该地址
)

type Setting struct {
	Key   SettingKey `json:"key" gorm:"primaryKey"`
	Value string     `json:"value" gorm:"not null"`
}

func DefaultSettings() []Setting {
	return []Setting{
		{Key: SettingKeyProxyURL, Value: ""},
		{Key: SettingKeyStatsSaveInterval, Value: "10"},       // 默认10分钟保存一次统计信息
		{Key: SettingKeyCORSAllowOrigins, Value: ""},          // CORS 默认不允许跨域，设置为 "*" 才允许所有来源
		{Key: SettingKeyModelInfoUpdateInterval, Value: "24"}, // 默认24小时更新一次模型信息
		{Key: SettingKeyModelFilter, Value: ""},               // 默认不过滤模型
		{Key: SettingKeyQuotaScanInterval, Value: "5"},        // 余额扫描默认 5 分钟一轮 (P5 低频口径)
		{Key: SettingKeyQuotaAlertThreshold, Value: ""},       // 默认不设告警阈值; 归零停用恒生效, 不经该阈值
		{Key: SettingKeyRouteBalanceEnabled, Value: "false"},  // 加权轮询热路径默认关闭, 行为与既有优先级选路一致
		{Key: SettingKeyAlertWebhookURL, Value: ""},           // 告警 webhook 默认不推送
	}
}

func (s *Setting) Validate() error {
	switch s.Key {
	case SettingKeyModelInfoUpdateInterval:
		_, err := strconv.Atoi(s.Value)
		if err != nil {
			return fmt.Errorf("model info update interval must be an integer")
		}
		return nil
	case SettingKeyModelFilter:
		if s.Value == "" {
			return nil
		}
		// 与渠道侧一致用 ECMAScript 方言校验, 避免设置能存但探测时编译失败。
		if _, err := regexp2.Compile(s.Value, regexp2.ECMAScript); err != nil {
			return fmt.Errorf("model filter regex is invalid: %w", err)
		}
		return nil
	case SettingKeyProxyURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("proxy URL is invalid: %w", err)
		}
		validSchemes := map[string]bool{
			"http":    true,
			"https":   true,
			"socks5":  true,
			"socks5h": true,
		}
		if !validSchemes[parsedURL.Scheme] {
			return fmt.Errorf("proxy URL scheme must be http, https, socks5, or socks5h")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("proxy URL must have a host")
		}
		return nil
	case SettingKeyQuotaScanInterval:
		minutes, err := strconv.Atoi(s.Value)
		if err != nil || minutes < 0 {
			return fmt.Errorf("quota scan interval must be a non-negative integer (minutes)")
		}
		return nil
	case SettingKeyQuotaAlertThreshold:
		if s.Value == "" {
			return nil
		}
		threshold, err := strconv.ParseFloat(s.Value, 64)
		if err != nil || threshold < 0 {
			return fmt.Errorf("quota alert threshold must be empty or a non-negative number")
		}
		return nil
	case SettingKeyAlertWebhookURL:
		if s.Value == "" {
			return nil
		}
		parsedURL, err := url.Parse(s.Value)
		if err != nil {
			return fmt.Errorf("alert webhook URL is invalid: %w", err)
		}
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return fmt.Errorf("alert webhook URL scheme must be http or https")
		}
		if parsedURL.Host == "" {
			return fmt.Errorf("alert webhook URL must include a host")
		}
		return nil
	}

	return nil
}
