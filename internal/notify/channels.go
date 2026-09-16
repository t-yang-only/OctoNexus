package notify

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/charmbracelet/log"
)

// R-alert-001 余项: 从"只有一个通用 webhook"扩到"一个事件投递到全部启用渠道"。
//
// 渠道与口径:
//   - webhook : 与改造前完全一致的通用出口 —— POST 原始 Event JSON, 供 n8n/自建服务消费(兼容不动)。
//   - feishu  : 飞书群机器人; 请求体 {"msg_type":"text","content":{"text":...}}, 响应 code/StatusCode 非 0 即失败。
//   - dingtalk: 钉钉群机器人; {"msgtype":"text","text":{"content":...}}, 响应 errcode 非 0 即失败。
//   - wecom   : 企业微信群机器人; 与钉钉同形, 响应 errcode 非 0 即失败。
//   - smtp    : 邮件; 密码只从环境变量 OCTOPUS_SMTP_PASSWORD 读, 不进设置表/数据库/备份。
//   - serverchan: Server酱(Turbo/³)推送; SendKey 拼进推送地址, 故优先从环境变量读, 没给才用设置表。
//
// 三条工程口径:
//  1. **不信任 HTTP 200**: 三家机器人都习惯用 200 包错误码, 故按响应体里的错误码判定成败, 并把原文摘要带回去。
//  2. **投递永不阻断调用方**: 每个渠道失败只记日志并进结果表, 不重试、不阻塞(告警是尽力而为的通知)。
//  3. **可测**: TestSend 用合成事件真实投递一次, 让用户在面板上确认"配好了没有", 而不是等真出事故才发现发不出去。

// Kind 是通知渠道类型, 取值与 alert_channels 设置里的字符串一致。
type Kind string

const (
	KindWebhook    Kind = "webhook"
	KindFeishu     Kind = "feishu"
	KindDingTalk   Kind = "dingtalk"
	KindWeCom      Kind = "wecom"
	KindSMTP       Kind = "smtp"
	KindServerChan Kind = "serverchan"
)

// allKinds 是渠道的固定顺序, 让结果表与界面顺序稳定。
var allKinds = []Kind{KindWebhook, KindFeishu, KindDingTalk, KindWeCom, KindSMTP, KindServerChan}

// smtpPasswordEnv 是唯一接受 SMTP 密码的地方: 库与备份里不放邮箱密码。
const smtpPasswordEnv = "OCTOPUS_SMTP_PASSWORD"

// Server酱的 SendKey 本身就是凭据(它整个就是推送地址的一部分), 所以与 SMTP 密码同口径:
// 优先从环境变量取, 没给才回落到设置表 —— 库与备份里就不会"必然"躺着这把钥匙。
const (
	serverChanSendKeyEnv  = "OCTOPUS_SERVERCHAN_SENDKEY"
	serverChanBaseURLEnv  = "OCTOPUS_SERVERCHAN_BASE_URL"
	serverChanDefaultBase = "https://sctapi.ftqq.com/"
	serverChanTag         = "octopus|告警"
)

// deliveryTimeout 是单个渠道的投递超时。
const deliveryTimeout = 10 * time.Second

// Result 是一个渠道的投递结果, 直接作为测试接口的响应体。
type Result struct {
	Kind    Kind   `json:"kind"`
	Enabled bool   `json:"enabled"` // 是否在 alert_channels 里启用。
	Sent    bool   `json:"sent"`    // 是否真的投递成功(未启用/未配置时为 false)。
	Detail  string `json:"detail"`  // 失败原因或成功摘要(已脱敏, 只带响应码与响应体摘要)。
}

// Target 是渠道的配置状态, 供界面判断"能不能点测试"。
type Target struct {
	Kind       Kind   `json:"kind"`
	Enabled    bool   `json:"enabled"`
	Configured bool   `json:"configured"`
	Hint       string `json:"hint"` // 未配置时给出缺哪一项; 不回显地址本身(里面带凭据)。
}

// EnabledKinds 读 alert_channels; 设置缺失/为空回退 webhook, 与改造前行为一致。
func EnabledKinds() []Kind {
	value, err := opSettingGet(model.SettingKeyAlertChannels)
	if err != nil || strings.TrimSpace(value) == "" {
		return []Kind{KindWebhook}
	}
	kinds := make([]Kind, 0, len(allKinds))
	for _, raw := range strings.Split(value, ",") {
		kind := Kind(strings.TrimSpace(raw))
		for _, known := range allKinds {
			if kind == known {
				kinds = append(kinds, kind)
				break
			}
		}
	}
	return kinds
}

// Targets 列出五个渠道的启用与配置状态。
func Targets() []Target {
	enabled := map[Kind]bool{}
	for _, kind := range EnabledKinds() {
		enabled[kind] = true
	}
	targets := make([]Target, 0, len(allKinds))
	for _, kind := range allKinds {
		configured, hint := channelConfigState(kind)
		targets = append(targets, Target{Kind: kind, Enabled: enabled[kind], Configured: configured, Hint: hint})
	}
	return targets
}

// channelConfigState 判断一个渠道是否配置齐全, 并给出缺项的提示(不回显地址与凭据)。
func channelConfigState(kind Kind) (bool, string) {
	switch kind {
	case KindWebhook:
		if settingValue(model.SettingKeyAlertWebhookURL) == "" {
			return false, "alert_webhook_url is empty"
		}
		return true, ""
	case KindFeishu:
		if settingValue(model.SettingKeyAlertFeishuWebhook) == "" {
			return false, "alert_feishu_webhook is empty"
		}
		return true, ""
	case KindDingTalk:
		if settingValue(model.SettingKeyAlertDingTalkWebhook) == "" {
			return false, "alert_dingtalk_webhook is empty"
		}
		return true, ""
	case KindWeCom:
		if settingValue(model.SettingKeyAlertWeComWebhook) == "" {
			return false, "alert_wecom_webhook is empty"
		}
		return true, ""
	case KindServerChan:
		if serverChanSendKey() == "" {
			return false, "alert_serverchan_sendkey is empty"
		}
		return true, ""
	case KindSMTP:
		missing := make([]string, 0, 3)
		if settingValue(model.SettingKeyAlertSMTPHost) == "" {
			missing = append(missing, "alert_smtp_host")
		}
		if settingValue(model.SettingKeyAlertSMTPFrom) == "" {
			missing = append(missing, "alert_smtp_from")
		}
		if settingValue(model.SettingKeyAlertSMTPTo) == "" {
			missing = append(missing, "alert_smtp_to")
		}
		if len(missing) > 0 {
			return false, "missing " + strings.Join(missing, ", ")
		}
		if user := settingValue(model.SettingKeyAlertSMTPUser); user != "" && os.Getenv(smtpPasswordEnv) == "" {
			// 有用户名却没给密码: 多数服务端会在 AUTH 阶段拒绝, 提前说清楚比看认证失败更好懂。
			return false, smtpPasswordEnv + " is not set while alert_smtp_user is configured"
		}
		return true, ""
	}
	return false, "unknown channel"
}

// Send 把一个事件投递到全部启用的渠道, 返回每个渠道的结果。永不 panic, 永不因单个渠道失败而中断其它渠道。
func Send(ctx context.Context, event Event) []Result {
	kinds := EnabledKinds()
	results := make([]Result, 0, len(kinds))
	for _, kind := range kinds {
		results = append(results, deliver(ctx, kind, event, false))
	}
	return results
}

// TestSend 用合成事件真实投递一次: 未启用的渠道也会尝试(测试的意义就是验证配置), 但未配置的渠道直接报缺项。
func TestSend(ctx context.Context) []Result {
	event := Event{
		Type:    "notify_test",
		Title:   "通知渠道测试",
		Message: "this is a test notification from octopus",
		Detail:  map[string]any{"source": "settings page test button"},
		At:      time.Now(),
	}
	enabled := map[Kind]bool{}
	for _, kind := range EnabledKinds() {
		enabled[kind] = true
	}
	results := make([]Result, 0, len(allKinds))
	for _, kind := range allKinds {
		result := deliver(ctx, kind, event, true)
		result.Enabled = enabled[kind]
		results = append(results, result)
	}
	return results
}

// deliver 投递到单个渠道。forceTest 为真时跳过"是否启用"的门槛(测试接口用), 但仍要求配置齐全。
func deliver(ctx context.Context, kind Kind, event Event, forceTest bool) Result {
	result := Result{Kind: kind, Enabled: true}
	configured, hint := channelConfigState(kind)
	if !configured {
		result.Detail = "not configured: " + hint
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()

	var err error
	switch kind {
	case KindWebhook:
		err = sendWebhook(ctx, event)
	case KindFeishu:
		err = sendFeishu(ctx, event)
	case KindDingTalk:
		err = sendDingTalk(ctx, event)
	case KindWeCom:
		err = sendWeCom(ctx, event)
	case KindSMTP:
		err = sendSMTP(ctx, event)
	case KindServerChan:
		err = sendServerChan(ctx, event)
	default:
		err = fmt.Errorf("unknown channel %q", kind)
	}
	if err != nil {
		result.Detail = err.Error()
		log.Warnf("alert delivery failed: channel=%s type=%s: %v", kind, event.Type, err)
		return result
	}
	result.Sent = true
	result.Detail = "delivered"
	return result
}

// DisplayTitle 返回事件的展示标题: 事件自带标题优先, 否则按类型给中文名, 未知类型原样返回。
func (event Event) DisplayTitle() string {
	if event.Title != "" {
		return event.Title
	}
	switch event.Type {
	case "quota_alert":
		return "渠道余额告警"
	case "quota_zero_stop":
		return "渠道归零停用"
	case "route_probe_recovered":
		return "冷却成员已恢复"
	case "notify_test":
		return "通知渠道测试"
	}
	return event.Type
}

// renderText 把事件渲染成群机器人/邮件用的纯文本(三家机器人的文本消息都是纯文本字段)。
func renderText(event Event) string {
	builder := strings.Builder{}
	builder.WriteString("[octopus] ")
	builder.WriteString(event.DisplayTitle())
	builder.WriteString("\n")
	if event.Channel != "" || event.ChannelID != 0 {
		builder.WriteString(fmt.Sprintf("渠道: %s (id=%d)\n", event.Channel, event.ChannelID))
	}
	if event.Message != "" {
		builder.WriteString("说明: " + event.Message + "\n")
	}
	if event.Remaining != 0 {
		builder.WriteString(fmt.Sprintf("剩余额度: %.4f\n", event.Remaining))
	}
	if event.Detail != nil {
		if raw, err := json.Marshal(event.Detail); err == nil {
			builder.WriteString("详情: " + string(raw) + "\n")
		}
	}
	at := event.At
	if at.IsZero() {
		at = time.Now()
	}
	builder.WriteString("时间: " + at.Format("2006-01-02 15:04:05"))
	return builder.String()
}

// sendWebhook 投递通用 webhook: 请求体就是事件本身(与改造前一致), 非 2xx 即失败。
func sendWebhook(ctx context.Context, event Event) error {
	return postJSON(ctx, settingValue(model.SettingKeyAlertWebhookURL), event, nil)
}

// sendFeishu 投递飞书群机器人。
func sendFeishu(ctx context.Context, event Event) error {
	payload := map[string]any{"msg_type": "text", "content": map[string]string{"text": renderText(event)}}
	return postJSON(ctx, settingValue(model.SettingKeyAlertFeishuWebhook), payload, func(body []byte) error {
		// 飞书历史上有 code 与 StatusCode 两种字段名, 两者都为 0/缺省才算成功。
		var parsed struct {
			Code       *int `json:"code"`
			StatusCode *int `json:"StatusCode"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil // 不以 JSON 应答的网关(如自建反代)按 HTTP 状态判成败。
		}
		if parsed.Code != nil && *parsed.Code != 0 {
			return fmt.Errorf("feishu returned code=%d", *parsed.Code)
		}
		if parsed.StatusCode != nil && *parsed.StatusCode != 0 {
			return fmt.Errorf("feishu returned StatusCode=%d", *parsed.StatusCode)
		}
		return nil
	})
}

// sendDingTalk 投递钉钉群机器人。
func sendDingTalk(ctx context.Context, event Event) error {
	payload := map[string]any{"msgtype": "text", "text": map[string]string{"content": renderText(event)}}
	return postJSON(ctx, settingValue(model.SettingKeyAlertDingTalkWebhook), payload, errCodeValidator("dingtalk"))
}

// sendWeCom 投递企业微信群机器人。
func sendWeCom(ctx context.Context, event Event) error {
	payload := map[string]any{"msgtype": "text", "text": map[string]string{"content": renderText(event)}}
	return postJSON(ctx, settingValue(model.SettingKeyAlertWeComWebhook), payload, errCodeValidator("wecom"))
}

// serverChanSendKey 取 SendKey: 环境变量优先, 没给才读设置表(与 SMTP 密码同一考虑)。
func serverChanSendKey() string {
	if value := strings.TrimSpace(os.Getenv(serverChanSendKeyEnv)); value != "" {
		return value
	}
	return strings.TrimSpace(settingValue(model.SettingKeyAlertServerChanSendKey))
}

// serverChanEndpoint 拼推送地址。base 可由环境变量覆盖, 好让套件把站点指到本地桩上
// (生产不设该变量即为官方地址, 故这条缝只影响测试)。
func serverChanEndpoint(sendKey string) string {
	base := strings.TrimSpace(os.Getenv(serverChanBaseURLEnv))
	if base == "" {
		base = serverChanDefaultBase
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	return base + sendKey + ".send"
}

// sendServerChan 投递 Server酱(Turbo/³): POST <base>/<SendKey>.send, 正文是表单 title/desp/tags。
//
// 它同样属于"HTTP 200 包错误码"那一类: 顶层 code 与 data.errno 都为 0 才算成功,
// 否则把错误码与原文摘要带回去(不重试、不阻断调用方)。
func sendServerChan(ctx context.Context, event Event) error {
	sendKey := serverChanSendKey()
	if sendKey == "" {
		return fmt.Errorf("serverchan sendkey is empty")
	}
	form := url.Values{}
	form.Set("title", "[octopus] "+event.DisplayTitle())
	form.Set("desp", renderText(event))
	form.Set("tags", serverChanTag)
	return postForm(ctx, serverChanEndpoint(sendKey), form, func(body []byte) error {
		var parsed struct {
			Code *int `json:"code"`
			Data *struct {
				ErrNo *int   `json:"errno"`
				Error string `json:"error"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil // 不以 JSON 应答的网关(如自建反代)按 HTTP 状态判成败。
		}
		if parsed.Code != nil && *parsed.Code != 0 {
			return fmt.Errorf("serverchan returned code=%d", *parsed.Code)
		}
		if parsed.Data != nil && parsed.Data.ErrNo != nil && *parsed.Data.ErrNo != 0 {
			return fmt.Errorf("serverchan returned errno=%d error=%s", *parsed.Data.ErrNo, parsed.Data.Error)
		}
		return nil
	})
}

// errCodeValidator 校验钉钉/企微同形的 {"errcode":0,"errmsg":"ok"} 应答。
func errCodeValidator(name string) func([]byte) error {
	return func(body []byte) error {
		var parsed struct {
			ErrCode *int   `json:"errcode"`
			ErrMsg  string `json:"errmsg"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil
		}
		if parsed.ErrCode != nil && *parsed.ErrCode != 0 {
			return fmt.Errorf("%s returned errcode=%d errmsg=%s", name, *parsed.ErrCode, parsed.ErrMsg)
		}
		return nil
	}
}

// post 向 url POST 一段正文: 不走代理设置(与既有 webhook 同口径), 非 2xx 直接失败,
// 2xx 时再交给 validate 检查响应体里的业务错误码。
func post(ctx context.Context, url, contentType string, body []byte, validate func([]byte) error) error {
	if url == "" {
		return fmt.Errorf("endpoint url is empty")
	}
	client, err := rhttp.Direct()
	if err != nil {
		return fmt.Errorf("build http client: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", contentType)
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("endpoint returned %d: %s", response.StatusCode, summarize(raw))
	}
	if validate != nil {
		if err := validate(raw); err != nil {
			return err
		}
	}
	return nil
}

// postJSON 向 url POST 一个 JSON 正文(各家机器人走这条)。
func postJSON(ctx context.Context, url string, payload any, validate func([]byte) error) error {
	if url == "" {
		return fmt.Errorf("webhook url is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}
	return post(ctx, url, "application/json", body, validate)
}

// postForm 向 url POST 一个表单正文(Server酱的推送接口收的是表单)。
func postForm(ctx context.Context, url string, form url.Values, validate func([]byte) error) error {
	if url == "" {
		return fmt.Errorf("endpoint url is empty")
	}
	return post(ctx, url, "application/x-www-form-urlencoded", []byte(form.Encode()), validate)
}

// sendSMTP 投递邮件: 465 走隐式 TLS, 其余端口走 net/smtp 的 STARTTLS(失败则明文, 与标准库语义一致)。
func sendSMTP(ctx context.Context, event Event) error {
	host := settingValue(model.SettingKeyAlertSMTPHost)
	from := settingValue(model.SettingKeyAlertSMTPFrom)
	recipients := splitAddresses(settingValue(model.SettingKeyAlertSMTPTo))
	if host == "" || from == "" || len(recipients) == 0 {
		return fmt.Errorf("smtp is not configured")
	}
	port, err := strconv.Atoi(settingValue(model.SettingKeyAlertSMTPPort))
	if err != nil || port <= 0 {
		port = 587
	}
	user := settingValue(model.SettingKeyAlertSMTPUser)
	password := os.Getenv(smtpPasswordEnv)
	message := buildMail(from, recipients, "[octopus] "+event.DisplayTitle(), renderText(event))

	address := net.JoinHostPort(host, strconv.Itoa(port))
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(deliveryTimeout)
	}
	dialer := &net.Dialer{Timeout: time.Until(deadline)}

	var connection net.Conn
	if port == 465 {
		// 465 是隐式 TLS: 连接建立即握手, 没有 STARTTLS 协商。
		connection, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{ServerName: host})
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return fmt.Errorf("dial smtp %s: %w", address, err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(deadline)

	client, err := smtp.NewClient(connection, host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer client.Close()
	if err := client.Hello("octopus"); err != nil {
		return fmt.Errorf("smtp hello: %w", err)
	}
	if port != 465 {
		// 端口 465 已经 TLS, 其余端口尝试 STARTTLS; 服务端不支持时保持明文继续(与 net/smtp 口径一致)。
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return fmt.Errorf("smtp starttls: %w", err)
			}
		}
	}
	if user != "" || password != "" {
		if ok, _ := client.Extension("AUTH"); !ok && user != "" {
			return fmt.Errorf("smtp server does not support AUTH but alert_smtp_user is set")
		}
		auth := smtp.PlainAuth("", user, password, host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", recipient, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := writer.Write(message); err != nil {
		return fmt.Errorf("smtp write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("smtp close body: %w", err)
	}
	// Quit 失败不影响"邮件已投递"的结论, 故忽略它的错误。
	_ = client.Quit()
	return nil
}

// buildMail 组装一封纯文本邮件: 正文用 base64 编码, 中文标题与正文都不会被中间 MTA 破坏。
func buildMail(from string, recipients []string, subject, text string) []byte {
	var builder bytes.Buffer
	builder.WriteString("From: " + from + "\r\n")
	builder.WriteString("To: " + strings.Join(recipients, ", ") + "\r\n")
	builder.WriteString("Subject: " + encodeHeader(subject) + "\r\n")
	builder.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	builder.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	builder.WriteString(base64.StdEncoding.EncodeToString([]byte(text)))
	builder.WriteString("\r\n")
	return builder.Bytes()
}

// encodeHeader 按 RFC 2047 编码可能含中文的邮件头。
func encodeHeader(value string) string {
	for _, r := range value {
		if r > 127 {
			return "=?UTF-8?B?" + base64.StdEncoding.EncodeToString([]byte(value)) + "?="
		}
	}
	return value
}

func splitAddresses(value string) []string {
	parts := strings.Split(value, ",")
	addresses := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			addresses = append(addresses, trimmed)
		}
	}
	return addresses
}

// settingValue 读设置; 缺失/读不到一律按空串处理(通知是旁路, 不因设置源缺失而改变主流程)。
func settingValue(key model.SettingKey) string {
	value, err := opSettingGet(key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// summarize 截断响应正文, 避免把整段上游响应写进告警详情与日志。
func summarize(body []byte) string {
	const limit = 200
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > limit {
		return trimmed[:limit] + "..."
	}
	return trimmed
}
