package relay

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/looplj/axonhub/llm/auth"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/antigravity"
	"github.com/looplj/axonhub/llm/transformer/gemini"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/t-yang-only/OctoNexus/internal/model"
	"github.com/tidwall/sjson"
)

// buildOutbound 在渠道授权支持的协议内选出本轮上游协议, 构造对应的出站转换器, 并返回选中的协议和能否同协议透传。
// 地址由渠道的协议路径字段与地址拼接, 凭据取自目标绑定的渠道凭据。
// want 是客户端请求使用的协议, 由调用方按入站格式定出; 选中的协议随请求状态推给界面, 故一并返回。
func buildOutbound(channel model.Channel, grant model.ChannelGrant, channelKey model.ChannelKey, want model.Protocol) (transformer.Outbound, model.Protocol, bool, error) {
	protocol, passthrough := want, grant.Protocols&want != 0
	if !passthrough {
		protocol = 0
		switch {
		case grant.Protocols&model.ProtocolAnthropicMessage != 0:
			protocol = model.ProtocolAnthropicMessage
		case grant.Protocols&model.ProtocolOpenAIResponse != 0:
			protocol = model.ProtocolOpenAIResponse
		case grant.Protocols&model.ProtocolOpenAIChatCompletion != 0:
			protocol = model.ProtocolOpenAIChatCompletion
		// Gemini 排在**最后**：它只影响"渠道只支持 Gemini"这一种新情况，
		// 渠道同时支持 OpenAI 系与 Gemini 时的选择与加它之前逐字一致。
		case grant.Protocols&model.ProtocolGeminiContents != 0:
			protocol = model.ProtocolGeminiContents
		}
	}

	key := auth.NewStaticKeyProvider(channelKey.Key)
	switch protocol {
	case model.ProtocolOpenAIChatCompletion:
		outbound, err := openai.NewOutboundTransformerWithConfig(&openai.Config{PlatformType: openai.PlatformOpenAI,
			// 基地址按端点路径去重版本段: 用户把 /v1 写进 BaseURL 时不再拼出 /v1/v1/...（NM-DS-004）。
			BaseURL:      model.ChannelBaseURL(channel.BaseURL, model.EndpointPathOrDefault(channel.OpenAIChatCompletionPath, "/v1/chat/completions")),
			EndpointPath: channel.OpenAIChatCompletionPath, APIKeyProvider: key})
		return outbound, protocol, passthrough, err
	case model.ProtocolOpenAIResponse:
		outbound, err := responses.NewOutboundTransformerWithConfig(&responses.Config{
			BaseURL:      model.ChannelBaseURL(channel.BaseURL, model.EndpointPathOrDefault(channel.OpenAIResponsePath, "/v1/responses")),
			EndpointPath: channel.OpenAIResponsePath, APIKeyProvider: key})
		return outbound, protocol, passthrough, err
	case model.ProtocolAnthropicMessage:
		outbound, err := anthropic.NewOutboundTransformerWithConfig(&anthropic.Config{Type: anthropic.PlatformDirect,
			BaseURL:      model.ChannelBaseURL(channel.BaseURL, model.EndpointPathOrDefault(channel.AnthropicMessagePath, "/v1/messages")),
			EndpointPath: channel.AnthropicMessagePath, APIKeyProvider: key})
		return outbound, protocol, passthrough, err
	case model.ProtocolGeminiContents:
		// Gemini 有两条上游路线，由**方言**区分（这正是 Dialect 存在的理由：
		// 同一线协议下不同服务商在报文与端点上的差异，地址与路径表达不了）。
		//   generic     → Google 官方 Gemini API（BaseURL 缺省即 generativelanguage）；
		//   antigravity → Antigravity / Cloud Code PA（自家选端点、套信封、过 sanitizer）。
		if channel.Dialect == model.DialectAntigravity {
			outbound, err := antigravity.NewTransformer(antigravity.Config{
				BaseURL: channel.BaseURL,
				APIKey:  channelKey.Key,
				Project: channel.GeminiProject,
			})
			return outbound, protocol, passthrough, err
		}
		// **不设 EndpointPath**：它的语义是"完整路径覆盖"，一设就把转换器自己按模型名
		// 拼好的 /v1beta/models/{model}:generateContent 顶掉，请求打到 /v1beta/models 上
		//（实测踩过：mock 上游收到 path=/v1beta/models，报文对、路径不对）。
		// Gemini 的完整路径含模型名，单个固定路径字段表达不了，交给转换器按 BaseURL 拼。
		outbound, err := gemini.NewOutboundTransformerWithConfig(gemini.Config{
			BaseURL:        channel.BaseURL,
			APIKeyProvider: key,
		})
		return outbound, protocol, passthrough, err
	default:
		return nil, 0, false, fmt.Errorf("channel grant %d supports no known protocol: %d", grant.ID, grant.Protocols)
	}
}

// clientHeaderPlaceholder 匹配自定义 Header 值中引用的客户端请求头
var clientHeaderPlaceholder = regexp.MustCompile(`\{client_header:[^}]+\}`)

// applyChannelConfig 按渠道配置覆盖上游请求的参数并追加自定义 Header; model 与 stream 由转发流程决定, 不允许覆盖。
func applyChannelConfig(channel model.Channel, request *httpclient.Request) error {
	if channel.ParamOverride != "" {
		var overrides map[string]json.RawMessage
		if err := json.Unmarshal([]byte(channel.ParamOverride), &overrides); err != nil {
			return fmt.Errorf("invalid channel parameter override: %w", err)
		}
		body := request.Body
		// 覆盖键可能自带点号或冒号, 转义后再作为 sjson 路径使用, 避免被解析成嵌套路径。
		escape := strings.NewReplacer("\\", "\\\\", ".", "\\.", ":", "\\:")
		for key, value := range overrides {
			if key == "model" || key == "stream" {
				continue
			}
			next, err := sjson.SetRawBytes(body, ":"+escape.Replace(key), value)
			if err != nil {
				return fmt.Errorf("apply channel parameter %q: %w", key, err)
			}
			body = next
		}
		request.Body = body
		if len(request.JSONBody) > 0 {
			request.JSONBody = slices.Clone(body)
		}
	}

	// 转换器已经写入的认证等敏感 Header 不允许被自定义配置覆盖。
	for _, header := range channel.CustomHeader {
		if request.Headers.Get(header.HeaderKey) != "" && httpclient.IsSensitiveHeader(header.HeaderKey) {
			continue
		}
		// 值中的 {client_header:xxx} 片段替换为客户端请求头 xxx 的实际值。
		value := clientHeaderPlaceholder.ReplaceAllStringFunc(header.HeaderValue, func(placeholder string) string {
			return request.Headers.Get(placeholder[len("{client_header:") : len(placeholder)-1])
		})
		request.Headers.Set(header.HeaderKey, value)
	}
	return nil
}
