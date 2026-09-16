package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/bestruirui/octopus/internal/model"
)

// Server酱(Turbo/³)渠道: 报文用表单、站点把错误码藏在 200 的响应体里、SendKey 本身即凭据
//（优先环境变量, 设置表只是回落）, 这三件事都要钉住。

// serverChanSink 记录方法/路径/Content-Type/表单正文, 并按给定状态与响应体作答。
type serverChanSink struct {
	mu      sync.Mutex
	path    string
	forms   []url.Values
	ctype   string
	methods []string
	server  *httptest.Server
}

func newServerChanSink(t *testing.T, status int, body string) *serverChanSink {
	t.Helper()
	sink := &serverChanSink{}
	sink.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
		form, _ := url.ParseQuery(string(raw))
		sink.mu.Lock()
		sink.path = r.URL.Path
		sink.ctype = r.Header.Get("Content-Type")
		sink.methods = append(sink.methods, r.Method)
		sink.forms = append(sink.forms, form)
		sink.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(sink.server.Close)
	return sink
}

func (sink *serverChanSink) snapshot() (string, string, []string, []url.Values) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.path, sink.ctype, append([]string(nil), sink.methods...), append([]url.Values(nil), sink.forms...)
}

// useServerChanBase 把推送站点指到本地桩; 生产不设该变量即为官方地址。
func useServerChanBase(t *testing.T, base string) {
	t.Helper()
	t.Setenv(serverChanBaseURLEnv, base)
}

func findResult(t *testing.T, results []Result, kind Kind) Result {
	t.Helper()
	for _, result := range results {
		if result.Kind == kind {
			return result
		}
	}
	t.Fatalf("结果表里没有 %s: %+v", kind, results)
	return Result{}
}

func TestServerChanDeliveryUsesFormAndSitePath(t *testing.T) {
	sink := newServerChanSink(t, http.StatusOK, `{"code":0,"message":"","data":{"pushid":"1","errno":0,"error":"SUCCESS"}}`)
	useServerChanBase(t, sink.server.URL)
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels:          "serverchan",
		model.SettingKeyAlertServerChanSendKey: "SCT-test-sendkey",
	})

	results := Send(context.Background(), testEvent())
	result := findResult(t, results, KindServerChan)
	if !result.Sent {
		t.Fatalf("serverchan 应投递成功: %+v", result)
	}

	path, ctype, methods, forms := sink.snapshot()
	if path != "/SCT-test-sendkey.send" {
		t.Errorf("推送路径应为 /<SendKey>.send, 实际 %q", path)
	}
	if len(methods) != 1 || methods[0] != http.MethodPost {
		t.Errorf("应只发一次 POST, 实际 %v", methods)
	}
	if !strings.HasPrefix(ctype, "application/x-www-form-urlencoded") {
		t.Errorf("Server酱 收表单, Content-Type 不对: %q", ctype)
	}
	if len(forms) != 1 {
		t.Fatalf("应收到一份表单, 实际 %d 份", len(forms))
	}
	form := forms[0]
	if !strings.HasPrefix(form.Get("title"), "[octopus] ") {
		t.Errorf("标题应带 [octopus] 前缀: %q", form.Get("title"))
	}
	if !strings.Contains(form.Get("title"), "渠道余额告警") {
		t.Errorf("标题应能看出事件类型: %q", form.Get("title"))
	}
	if !strings.Contains(form.Get("desp"), "channel balance below threshold") {
		t.Errorf("正文应带事件内容: %q", form.Get("desp"))
	}
	if form.Get("tags") != serverChanTag {
		t.Errorf("tags 应为 %q, 实际 %q", serverChanTag, form.Get("tags"))
	}
}

// 站点用 HTTP 200 包业务错误: 必须判失败并把错误码带回来, 不能因为状态码是 200 就报成功。
func TestServerChanBusinessErrorFailsTheChannel(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"data.errno 非 0", `{"code":0,"data":{"errno":1001,"error":"bad sendkey"}}`, "errno=1001"},
		{"顶层 code 非 0", `{"code":40001,"message":"invalid sendkey"}`, "code=40001"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := newServerChanSink(t, http.StatusOK, tc.body)
			useServerChanBase(t, sink.server.URL)
			useSettings(t, map[model.SettingKey]string{
				model.SettingKeyAlertChannels:          "serverchan",
				model.SettingKeyAlertServerChanSendKey: "SCT-test-sendkey",
			})

			result := findResult(t, Send(context.Background(), testEvent()), KindServerChan)
			if result.Sent {
				t.Fatalf("业务错误码不该判成功: %+v", result)
			}
			if !strings.Contains(result.Detail, tc.want) {
				t.Errorf("失败原因应带 %q, 实际 %q", tc.want, result.Detail)
			}
		})
	}
}

func TestServerChanNotConfiguredIsAHint(t *testing.T) {
	useSettings(t, map[model.SettingKey]string{model.SettingKeyAlertChannels: "serverchan"})
	result := findResult(t, Send(context.Background(), testEvent()), KindServerChan)
	if result.Sent {
		t.Fatalf("没配 SendKey 不该发包: %+v", result)
	}
	if !strings.Contains(result.Detail, "alert_serverchan_sendkey is empty") {
		t.Errorf("未配置的提示应指出缺哪一项: %q", result.Detail)
	}
}

// 环境变量优先: 它给了就完全不看设置表(SendKey 属于"能放环境就别放库"的那类凭据)。
func TestServerChanSendKeyPrefersTheEnvironment(t *testing.T) {
	sink := newServerChanSink(t, http.StatusOK, `{"code":0,"data":{"errno":0}}`)
	useServerChanBase(t, sink.server.URL)
	t.Setenv(serverChanSendKeyEnv, "SCT-from-env")
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels:          "serverchan",
		model.SettingKeyAlertServerChanSendKey: "SCT-from-settings",
	})

	if result := findResult(t, Send(context.Background(), testEvent()), KindServerChan); !result.Sent {
		t.Fatalf("环境变量给了 SendKey 就该发得出去: %+v", result)
	}
	path, _, _, _ := sink.snapshot()
	if path != "/SCT-from-env.send" {
		t.Errorf("应优先用环境变量里的 SendKey, 实际走了 %q", path)
	}
}

// 状态接口只回"能不能发", 绝不回显带凭据的地址/钥匙本身。
func TestServerChanSendKeyNeverLeaksThroughTargets(t *testing.T) {
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertChannels:          "serverchan",
		model.SettingKeyAlertServerChanSendKey: "SCT-very-secret-sendkey",
	})

	encoded, err := json.Marshal(Targets())
	if err != nil {
		t.Fatalf("marshal targets: %v", err)
	}
	if strings.Contains(string(encoded), "SCT-very-secret-sendkey") {
		t.Fatalf("渠道状态里回显了 SendKey: %s", encoded)
	}

	var found bool
	for _, target := range Targets() {
		if target.Kind != KindServerChan {
			continue
		}
		found = true
		if !target.Configured || !target.Enabled {
			t.Errorf("配好并启用后应报 configured+enabled: %+v", target)
		}
		if target.Hint != "" {
			t.Errorf("配齐了不该有缺项提示: %q", target.Hint)
		}
	}
	if !found {
		t.Fatalf("渠道清单里应有 serverchan: %s", encoded)
	}
}

// TestSend 会遍历全部渠道并真实投递: serverchan 也要在里面, 且测试事件一样能发出去。
func TestTestSendCoversServerChan(t *testing.T) {
	sink := newServerChanSink(t, http.StatusOK, `{"code":0,"data":{"errno":0}}`)
	useServerChanBase(t, sink.server.URL)
	useSettings(t, map[model.SettingKey]string{
		model.SettingKeyAlertServerChanSendKey: "SCT-test-sendkey",
	})

	result := findResult(t, TestSend(context.Background()), KindServerChan)
	if !result.Sent || result.Enabled {
		t.Fatalf("测试投递应成功且标注未启用: %+v", result)
	}
	_, _, _, forms := sink.snapshot()
	if len(forms) != 1 || !strings.Contains(forms[0].Get("desp"), "test notification") {
		t.Fatalf("测试事件没打出去: %v", forms)
	}
}
