package model

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/dlclark/regexp2"
)

type SettingKey string

const (
	SettingKeyProxyURL                SettingKey = "proxy_url"
	SettingKeyStatsSaveInterval       SettingKey = "stats_save_interval"          // 将统计信息写入数据库的周期(分钟)
	SettingKeyModelInfoUpdateInterval SettingKey = "model_info_update_interval"   // 模型信息更新间隔(小时)
	SettingKeyCORSAllowOrigins        SettingKey = "cors_allow_origins"           // 跨域白名单(逗号分隔, 如 "example.com,example2.com"). 为空不允许跨域, "*"允许所有
	SettingKeyModelFilter             SettingKey = "model_filter"                 // 渠道获取模型时的全局过滤表达式; 留空表示不过滤
	SettingKeyQuotaScanInterval       SettingKey = "quota_scan_interval"          // 余额采集扫描周期(分钟), T-quota-001; 0 表示停用扫描任务
	SettingKeyQuotaAlertThreshold     SettingKey = "quota_alert_threshold"        // 余额告警阈值(额度点), 剩余额度低于该值记告警事件; 留空或<=0 表示不告警 (归零停用不受其影响, 恒按 remaining<=0 判定)
	SettingKeyRouteBalanceEnabled     SettingKey = "route_balance_enabled"        // 故障转移分组是否用加权轮询定序候选 (T-route-002); 默认关闭, 走原有优先级选路
	SettingKeyAlertWebhookURL         SettingKey = "alert_webhook_url"            // 告警事件 webhook 地址, 留空不推送; 余额告警/归零停用等事件 POST JSON 到该地址
	SettingKeyRouteProbeEnabled       SettingKey = "route_probe_enabled"          // 冷却成员主动探活开关 (R-probe-001); 默认关闭: 每次探测都是一次真实计费请求
	SettingKeyRouteProbeInterval      SettingKey = "route_probe_interval_seconds" // 主动探活周期(秒), 0 表示停用探活任务; 默认 300

	// 通知渠道 (R-alert-001 余项): 一个事件同时投递到全部启用渠道。
	// 密钥口径: 三个群机器人的 webhook 地址本身即凭据(与既有 alert_webhook_url 同性质, 面板可见可编辑);
	// SMTP 密码不进设置表, 只从环境变量 OCTOPUS_SMTP_PASSWORD 读——避免把邮箱密码写进库与备份转储。
	SettingKeyAlertChannels        SettingKey = "alert_channels"         // 启用的通知渠道, 逗号分隔: webhook,feishu,dingtalk,wecom,smtp; 默认 webhook
	SettingKeyAlertFeishuWebhook   SettingKey = "alert_feishu_webhook"   // 飞书群机器人 webhook 地址
	SettingKeyAlertDingTalkWebhook SettingKey = "alert_dingtalk_webhook" // 钉钉群机器人 webhook 地址
	SettingKeyAlertWeComWebhook    SettingKey = "alert_wecom_webhook"    // 企业微信群机器人 webhook 地址
	SettingKeyAlertSMTPHost        SettingKey = "alert_smtp_host"        // SMTP 服务器地址 (不含端口)
	SettingKeyAlertSMTPPort        SettingKey = "alert_smtp_port"        // SMTP 端口: 465 走隐式 TLS, 其余走 STARTTLS/明文
	SettingKeyAlertSMTPUser        SettingKey = "alert_smtp_user"        // SMTP 登录用户; 留空表示不做认证
	SettingKeyAlertSMTPFrom        SettingKey = "alert_smtp_from"        // 发件人地址
	SettingKeyAlertSMTPTo          SettingKey = "alert_smtp_to"          // 收件人地址, 多个用逗号分隔
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
		{Key: SettingKeyRouteProbeEnabled, Value: "false"},    // 主动探活默认关闭: 探测是真实计费请求, 开不开由用户决定
		{Key: SettingKeyRouteProbeInterval, Value: "300"},     // 探活默认 5 分钟一轮 (低频, 冷却期通常远大于它)
		{Key: SettingKeyAlertChannels, Value: "webhook"},      // 默认只发通用 webhook: 与改造前行为一致
		{Key: SettingKeyAlertFeishuWebhook, Value: ""},
		{Key: SettingKeyAlertDingTalkWebhook, Value: ""},
		{Key: SettingKeyAlertWeComWebhook, Value: ""},
		{Key: SettingKeyAlertSMTPHost, Value: ""},
		{Key: SettingKeyAlertSMTPPort, Value: "587"}, // 587 是 STARTTLS 的通行端口; 465 会走隐式 TLS
		{Key: SettingKeyAlertSMTPUser, Value: ""},
		{Key: SettingKeyAlertSMTPFrom, Value: ""},
		{Key: SettingKeyAlertSMTPTo, Value: ""},
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
	case SettingKeyRouteProbeEnabled:
		if _, err := strconv.ParseBool(s.Value); err != nil {
			return fmt.Errorf("route probe enabled must be a boolean")
		}
		return nil
	case SettingKeyRouteProbeInterval:
		seconds, err := strconv.Atoi(s.Value)
		if err != nil || seconds < 0 {
			return fmt.Errorf("route probe interval must be a non-negative integer (seconds)")
		}
		return nil
	case SettingKeyAlertWebhookURL:
		return validateHTTPURL(s.Value, "alert webhook URL")
	case SettingKeyAlertFeishuWebhook:
		return validateHTTPURL(s.Value, "feishu webhook URL")
	case SettingKeyAlertDingTalkWebhook:
		return validateHTTPURL(s.Value, "dingtalk webhook URL")
	case SettingKeyAlertWeComWebhook:
		return validateHTTPURL(s.Value, "wecom webhook URL")
	case SettingKeyAlertChannels:
		for _, kind := range splitNotifyChannels(s.Value) {
			if !isNotifyChannel(kind) {
				return fmt.Errorf("unknown alert channel %q (want webhook, feishu, dingtalk, wecom or smtp)", kind)
			}
		}
		return nil
	case SettingKeyAlertSMTPPort:
		port, err := strconv.Atoi(s.Value)
		if err != nil || port < 0 || port > 65535 {
			return fmt.Errorf("smtp port must be an integer between 0 and 65535")
		}
		return nil
	case SettingKeyAlertSMTPHost:
		if s.Value == "" {
			return nil
		}
		// SMTP 地址只写主机名: 端口是独立设置, 带上 svc:// 或 :port 会构造出无法拨号的地址。
		if strings.ContainsAny(s.Value, ":/ ") {
			return fmt.Errorf("smtp host must be a bare hostname without scheme or port")
		}
		return nil
	case SettingKeyAlertSMTPFrom, SettingKeyAlertSMTPTo:
		return validateMailboxList(s.Value)
	}

	return nil
}

// validateHTTPURL 校验 webhook 类设置: 留空合法(表示未配置), 否则必须是带主机名的 http(s) 地址。
func validateHTTPURL(value, name string) error {
	if value == "" {
		return nil
	}
	parsedURL, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("%s scheme must be http or https", name)
	}
	if parsedURL.Host == "" {
		return fmt.Errorf("%s must include a host", name)
	}
	return nil
}

// splitNotifyChannels 切分渠道设置并去空白; 空串切成空表(表示一个渠道都不发)。
func splitNotifyChannels(value string) []string {
	parts := strings.Split(value, ",")
	kinds := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			kinds = append(kinds, trimmed)
		}
	}
	return kinds
}

func isNotifyChannel(kind string) bool {
	switch kind {
	case "webhook", "feishu", "dingtalk", "wecom", "smtp":
		return true
	}
	return false
}

// validateMailboxList 校验收件人/发件人: 留空合法, 否则每项都必须是形如 a@b 的地址。
func validateMailboxList(value string) error {
	if value == "" {
		return nil
	}
	for _, address := range splitNotifyChannels(value) {
		at := strings.Index(address, "@")
		if at <= 0 || at == len(address)-1 || strings.ContainsAny(address, " \t") {
			return fmt.Errorf("email address %q is invalid", address)
		}
	}
	return nil
}
