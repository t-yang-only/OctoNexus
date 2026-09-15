package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/model"
)

// R-alert-001 余项的单测: 三个群机器人的请求体形状、业务错误码判定(不信任 HTTP 200)、
// 渠道开关过滤、未配置渠道的提示, 以及 SMTP 真的把邮件投出去。

// useSettings 把设置源换成一张内存表; 未给出的键按"不存在"处理。
func useSettings(t *testing.T, values map[model.SettingKey]string) {
	t.Helper()
	SetSettingSource(func(key model.SettingKey) (string, error) {
		value, ok := values[key]
		if !ok {
			return "", fmt.Errorf("setting not found")
		}
		return value, nil
	})
	t.Cleanup(func() {
		SetSettingSource(func(model.SettingKey) (string, error) { return "", errSettingUnavailable })
	})
}

// captureServer 记录收到的请求体, 并按 handler 给出响应。
type captureServer struct {
	mu       sync.Mutex
	requests [][]byte
	server   *httptest.Server
}

func newCaptureServer(t *testing.T, status int, body string) *captureServer {
	t.Helper()
	capture := &captureServer{}
	capture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := make([]byte, r.ContentLength)
		if r.ContentLength > 0 {
			_, _ = r.Body.Read(raw)
		}
		capture.mu.Lock()
		capture.requests = append(capture.requests, raw)
		capture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(capture.server.Close)
	return capture
}

func (capture *captureServer) count() int {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return len(capture.requests)
}

func (capture *captureServer) last() []byte {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if len(capture.requests) == 0 {
		return nil
	}
	return capture.requests[len(capture.requests)-1]
}

func testEvent() Event {
	return Event{
		Type:      "quota_alert",
		Channel:   "DS-TEST-mock",
		ChannelID: 7,
		Message:   "channel balance below threshold",
		Remaining: 12.5,
		Detail:    map[string]float64{"threshold": 20},
		At:        time.Date(2026, 9, 16, 1, 2, 3, 0, time.Local),
	}
}

// TestRobotPayloadShapes 四个 webhook 类渠道各收到符合自身规范的请求体, 且都判成功。
func TestRobotPayloadShapes(t *testing.T) {
	webhook := newCaptureServer(t, http.StatusOK, `{"ok":true}`)
	feishu := newCaptureServer(t, http.StatusOK, `{"code":0,"msg":"success"}`)
	dingtalk := newCaptureServer(t, http.StatusOK, `{"errcode":0,"errmsg":"ok"}`)
	wecom := newCaptureServer(t, http.StatusOK, `{"errcode":0,"errmsg":"ok"}`)
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels:        "webhook,feishu,dingtalk,wecom",
		model.SettingKeyAlertWebhookURL:      webhook.server.URL,
		model.SettingKeyAlertFeishuWebhook:   feishu.server.URL,
		model.SettingKeyAlertDingTalkWebhook: dingtalk.server.URL,
		model.SettingKeyAlertWeComWebhook:    wecom.server.URL,
	})

	results := Send(context.Background(), testEvent())
	if len(results) != 4 {
		t.Fatalf("results=%d, want 4", len(results))
	}
	for _, result := range results {
		if !result.Sent {
			t.Fatalf("channel %s not sent: %s", result.Kind, result.Detail)
		}
	}

	// 通用 webhook 保持改造前的形状: 请求体就是事件本身。
	var event Event
	if err := json.Unmarshal(webhook.last(), &event); err != nil {
		t.Fatalf("webhook payload is not the event json: %v", err)
	}
	if event.Type != "quota_alert" || event.ChannelID != 7 {
		t.Fatalf("webhook payload = %+v, want the original event", event)
	}

	var feishuPayload struct {
		MsgType string `json:"msg_type"`
		Content struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(feishu.last(), &feishuPayload); err != nil {
		t.Fatalf("feishu payload: %v", err)
	}
	if feishuPayload.MsgType != "text" || !strings.Contains(feishuPayload.Content.Text, "[octopus] 渠道余额告警") {
		t.Fatalf("feishu payload = %s", feishu.last())
	}

	for name, capture := range map[string]*captureServer{"dingtalk": dingtalk, "wecom": wecom} {
		var payload struct {
			MsgType string `json:"msgtype"`
			Text    struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		if err := json.Unmarshal(capture.last(), &payload); err != nil {
			t.Fatalf("%s payload: %v", name, err)
		}
		if payload.MsgType != "text" || !strings.Contains(payload.Text.Content, "DS-TEST-mock") {
			t.Fatalf("%s payload = %s", name, capture.last())
		}
	}
}

// TestBusinessErrorCodeFailsTheChannel 三家机器人习惯用 HTTP 200 包业务错误码:
// 判据必须看响应体里的码, 否则"配置写错/被限流"会被当成发送成功。
func TestBusinessErrorCodeFailsTheChannel(t *testing.T) {
	feishu := newCaptureServer(t, http.StatusOK, `{"code":19024,"msg":"key not found"}`)
	dingtalk := newCaptureServer(t, http.StatusOK, `{"errcode":310000,"errmsg":"keywords not in content"}`)
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels:        "feishu,dingtalk",
		model.SettingKeyAlertFeishuWebhook:   feishu.server.URL,
		model.SettingKeyAlertDingTalkWebhook: dingtalk.server.URL,
	})

	results := Send(context.Background(), testEvent())
	for _, result := range results {
		if result.Sent {
			t.Fatalf("channel %s reported success on a business error", result.Kind)
		}
	}
	if !strings.Contains(results[0].Detail, "code=19024") {
		t.Fatalf("feishu detail=%q, want the provider error code", results[0].Detail)
	}
	if !strings.Contains(results[1].Detail, "errcode=310000") {
		t.Fatalf("dingtalk detail=%q, want the provider error code", results[1].Detail)
	}
}

// TestChannelSelection 只有 alert_channels 里列出的渠道会被投递。
func TestChannelSelection(t *testing.T) {
	webhook := newCaptureServer(t, http.StatusOK, `{}`)
	feishu := newCaptureServer(t, http.StatusOK, `{"code":0}`)
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels:      "feishu",
		model.SettingKeyAlertWebhookURL:    webhook.server.URL,
		model.SettingKeyAlertFeishuWebhook: feishu.server.URL,
	})

	results := Send(context.Background(), testEvent())
	if len(results) != 1 || results[0].Kind != KindFeishu {
		t.Fatalf("results=%+v, want only feishu", results)
	}
	if webhook.count() != 0 {
		t.Fatalf("webhook received %d requests while disabled", webhook.count())
	}
	if feishu.count() != 1 {
		t.Fatalf("feishu received %d requests, want 1", feishu.count())
	}
}

// TestEnabledKindsDefaultsToWebhook 设置缺失/为空时回退 webhook, 与改造前行为一致。
func TestEnabledKindsDefaultsToWebhook(t *testing.T) {
	useSettings(t, map[model.SettingKey]string{})
	kinds := EnabledKinds()
	if len(kinds) != 1 || kinds[0] != KindWebhook {
		t.Fatalf("kinds=%v, want [webhook]", kinds)
	}
	useSettings(t, map[model.SettingKey]string{model.SettingKeyAlertChannels: " feishu , smtp ,bogus"})
	kinds = EnabledKinds()
	if len(kinds) != 2 || kinds[0] != KindFeishu || kinds[1] != KindSMTP {
		t.Fatalf("kinds=%v, want [feishu smtp] (未知渠道被忽略)", kinds)
	}
}

// TestTestSendReportsConfigurationGaps 测试接口对未配置渠道给出缺项提示, 对已配置渠道真投递。
func TestTestSendReportsConfigurationGaps(t *testing.T) {
	feishu := newCaptureServer(t, http.StatusOK, `{"code":0}`)
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels:      "feishu",
		model.SettingKeyAlertFeishuWebhook: feishu.server.URL,
	})
	results := TestSend(context.Background())
	byKind := map[Kind]Result{}
	for _, result := range results {
		byKind[result.Kind] = result
	}
	if !byKind[KindFeishu].Sent || !byKind[KindFeishu].Enabled {
		t.Fatalf("feishu result=%+v, want sent+enabled", byKind[KindFeishu])
	}
	if byKind[KindWebhook].Sent || !strings.Contains(byKind[KindWebhook].Detail, "not configured") {
		t.Fatalf("webhook result=%+v, want an explicit configuration gap", byKind[KindWebhook])
	}
	if byKind[KindSMTP].Sent || !strings.Contains(byKind[KindSMTP].Detail, "alert_smtp_host") {
		t.Fatalf("smtp result=%+v, want the missing keys", byKind[KindSMTP])
	}
	if feishu.count() != 1 {
		t.Fatalf("feishu received %d test messages, want 1", feishu.count())
	}
}

// TestTargetsHideSecrets 状态接口只回"能不能发", 不把带凭据的地址回显出去。
func TestTargetsHideSecrets(t *testing.T) {
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels:      "feishu",
		model.SettingKeyAlertFeishuWebhook: "https://open.feishu.cn/open-apis/bot/v2/hook/super-secret-token",
	})
	raw, err := json.Marshal(Targets())
	if err != nil {
		t.Fatalf("marshal targets: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-token") {
		t.Fatalf("targets leaked the webhook credential: %s", raw)
	}
}

// ---------- SMTP ----------

// smtpSink 是一个够用的 SMTP 服务端: 说够协议让 net/smtp 走完一次投递, 并把邮件正文留下来。
type smtpSink struct {
	listener net.Listener
	mu       sync.Mutex
	messages []string
}

func startSMTPSink(t *testing.T) *smtpSink {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	sink := &smtpSink{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go sink.serve()
	return sink
}

func (sink *smtpSink) serve() {
	for {
		connection, err := sink.listener.Accept()
		if err != nil {
			return
		}
		go sink.handle(connection)
	}
}

func (sink *smtpSink) handle(connection net.Conn) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	write := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}
	write("220 octopus-test ESMTP")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			write("250-octopus-test")
			write("250 SIZE 10485760")
		case strings.HasPrefix(command, "MAIL FROM"):
			write("250 OK")
		case strings.HasPrefix(command, "RCPT TO"):
			write("250 OK")
		case strings.HasPrefix(command, "DATA"):
			write("354 End data with <CR><LF>.<CR><LF>")
			message, err := readSMTPData(reader)
			if err != nil {
				return
			}
			sink.mu.Lock()
			sink.messages = append(sink.messages, message)
			sink.mu.Unlock()
			write("250 OK: queued")
		case strings.HasPrefix(command, "QUIT"):
			write("221 Bye")
			return
		default:
			write("250 OK")
		}
	}
}

// readSMTPData 读到单独的 "." 行为止(点首解码只做最小处理: 我们的正文是 base64, 不会出现点首)。
func readSMTPData(reader *bufio.Reader) (string, error) {
	var builder strings.Builder
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		if strings.TrimRight(line, "\r\n") == "." {
			return builder.String(), nil
		}
		builder.WriteString(line)
	}
}

func (sink *smtpSink) last() string {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.messages) == 0 {
		return ""
	}
	return sink.messages[len(sink.messages)-1]
}

func (sink *smtpSink) port() string {
	return fmt.Sprintf("%d", sink.listener.Addr().(*net.TCPAddr).Port)
}

// TestSMTPDeliversMail SMTP 渠道真的把邮件投出去: 服务端收到 DATA, 主题按 RFC 2047 编码中文, 正文是 base64。
func TestSMTPDeliversMail(t *testing.T) {
	sink := startSMTPSink(t)
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels: "smtp",
		model.SettingKeyAlertSMTPHost: "127.0.0.1",
		model.SettingKeyAlertSMTPPort: sink.port(),
		model.SettingKeyAlertSMTPFrom: "octopus@example.com",
		model.SettingKeyAlertSMTPTo:   "ops@example.com, ops2@example.com",
		model.SettingKeyAlertSMTPUser: "",
	})
	t.Setenv("OCTOPUS_SMTP_PASSWORD", "")

	results := Send(context.Background(), testEvent())
	if len(results) != 1 || !results[0].Sent {
		t.Fatalf("results=%+v, want smtp delivered", results)
	}
	message := sink.last()
	if message == "" {
		t.Fatal("smtp sink received no DATA")
	}
	if !strings.Contains(message, "To: ops@example.com, ops2@example.com") {
		t.Fatalf("message headers = %q", firstLines(message, 5))
	}
	if !strings.Contains(message, "Subject: =?UTF-8?B?") {
		t.Fatalf("chinese subject is not RFC2047 encoded: %q", firstLines(message, 5))
	}
	if !strings.Contains(message, "Content-Transfer-Encoding: base64") || !strings.Contains(message, "charset=UTF-8") {
		t.Fatalf("message is not utf-8 base64 text: %q", firstLines(message, 8))
	}
}

// TestSMTPWithoutPasswordButWithUserIsAConfigurationGap 有用户名却没给密码时提前报缺项, 而不是等认证失败。
func TestSMTPWithoutPasswordButWithUserIsAConfigurationGap(t *testing.T) {
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels: "smtp",
		model.SettingKeyAlertSMTPHost: "smtp.example.com",
		model.SettingKeyAlertSMTPPort: "587",
		model.SettingKeyAlertSMTPFrom: "octopus@example.com",
		model.SettingKeyAlertSMTPTo:   "ops@example.com",
		model.SettingKeyAlertSMTPUser: "octopus@example.com",
	})
	t.Setenv("OCTOPUS_SMTP_PASSWORD", "")
	results := Send(context.Background(), testEvent())
	if results[0].Sent || !strings.Contains(results[0].Detail, "OCTOPUS_SMTP_PASSWORD") {
		t.Fatalf("result=%+v, want a password hint", results[0])
	}
}

func firstLines(text string, count int) string {
	lines := strings.Split(text, "\n")
	if len(lines) > count {
		lines = lines[:count]
	}
	return strings.Join(lines, "\\n")
}
