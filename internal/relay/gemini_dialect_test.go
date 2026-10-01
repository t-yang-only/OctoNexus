package relay

import (
	"fmt"
	"strings"
	"testing"

	"github.com/t-yang-only/OctoNexus/internal/model"
)

// TestBuildOutboundGemini 覆盖 Gemini 上游接入（T-gemini-001）。
//
// 三条判据，缺一条都说明接线没生效：
//  1. 授权**只**支持 ProtocolGeminiContents 时，buildOutbound 能建出转换器。
//     加这一定位之前，这条路走到 default，报
//     "channel grant N supports no known protocol: 16" —— 即渠道能用 Gemini 但根本发不出请求。
//  2. 方言决定走哪条上游路线：generic → Google 官方 Gemini，antigravity → Cloud Code PA。
//     两者的 APIFormat 都是 gemini/contents（线协议相同），所以**不能用 APIFormat 区分**，
//     必须断言具体类型。
//  3. 授权同时支持 OpenAI 与 Gemini 时，选中的仍是 OpenAI ——
//     保证给已有渠道新增这一位**不改变现有选择**（逐字不变原则）。
func TestBuildOutboundGemini(t *testing.T) {
	base := model.Channel{
		ChannelConfig: model.ChannelConfig{
			Name:    "demo-gemini",
			BaseURL: "https://generativelanguage.googleapis.com",
			Dialect: model.DialectGeneric,
		},
	}
	ag := model.Channel{
		ChannelConfig: model.ChannelConfig{
			Name:          "demo-antigravity",
			BaseURL:       "https://cloudcode-pa.googleapis.com",
			Dialect:       model.DialectAntigravity,
			GeminiProject: "demo-project",
		},
	}
	grantGemini := model.ChannelGrant{ID: 1, Protocols: model.ProtocolGeminiContents}
	grantBoth := model.ChannelGrant{ID: 2, Protocols: model.ProtocolOpenAIChatCompletion | model.ProtocolGeminiContents}
	// 用内嵌结构显式构造：ChannelKey.Key 是提升字段，
	// go.mod 钉的是 go1.26，在结构体字面量里直接写提升字段要 go1.27 才允许。
	key := model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "sk-demo-000000"}}

	t.Run("只支持 gemini", func(t *testing.T) {
		outbound, protocol, passthrough, err := buildOutbound(base, grantGemini, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		if protocol != model.ProtocolGeminiContents {
			t.Fatalf("选中的协议 = %d, 想 GeminiContents(%d)", protocol, model.ProtocolGeminiContents)
		}
		if passthrough {
			t.Error("客户端说 OpenAI 而渠道只支持 Gemini，passthrough 不该为 true")
		}
		if got := outbound.APIFormat().String(); got != "gemini/contents" {
			t.Errorf("出站 APIFormat = %q, 想 gemini/contents", got)
		}
	})

	t.Run("方言 antigravity", func(t *testing.T) {
		outbound, _, _, err := buildOutbound(ag, grantGemini, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		got := fmt.Sprintf("%T", outbound)
		if !strings.Contains(got, "antigravity") {
			t.Errorf("方言 antigravity 选中的是 %s，想 antigravity.Transformer", got)
		}
	})

	t.Run("方言 generic 不落到 antigravity", func(t *testing.T) {
		outbound, _, _, err := buildOutbound(base, grantGemini, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		if got := fmt.Sprintf("%T", outbound); strings.Contains(got, "antigravity") {
			t.Errorf("generic 方言选中了 %s，想 gemini.OutboundTransformer", got)
		}
	})

	t.Run("同时支持时仍选 OpenAI", func(t *testing.T) {
		_, protocol, _, err := buildOutbound(base, grantBoth, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		if protocol != model.ProtocolOpenAIChatCompletion {
			t.Errorf("选中的协议 = %d, 想 OpenAIChatCompletion(%d) —— 新增 Gemini 位改变了既有选择",
				protocol, model.ProtocolOpenAIChatCompletion)
		}
	})

	t.Run("路径缺省填 /v1beta/models", func(t *testing.T) {
		cfg := model.ChannelConfig{}
		if got := model.EndpointPathOrDefault(cfg.GeminiContentsPath, "/v1beta/models"); got != "/v1beta/models" {
			t.Errorf("空路径取缺省 = %q, 想 /v1beta/models", got)
		}
	})
}
