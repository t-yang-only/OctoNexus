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
	// 下面这批是 OpenAI 线协议的**厂商特化**转换器（见 vendorOutboundFor 的说明）。
	"github.com/looplj/axonhub/llm/transformer/bailian"
	"github.com/looplj/axonhub/llm/transformer/cerebras"
	"github.com/looplj/axonhub/llm/transformer/cline"
	"github.com/looplj/axonhub/llm/transformer/deepseek"
	"github.com/looplj/axonhub/llm/transformer/doubao"
	"github.com/looplj/axonhub/llm/transformer/fireworks"
	"github.com/looplj/axonhub/llm/transformer/gemini"
	"github.com/looplj/axonhub/llm/transformer/longcat"
	"github.com/looplj/axonhub/llm/transformer/modelscope"
	"github.com/looplj/axonhub/llm/transformer/moonshot"
	"github.com/looplj/axonhub/llm/transformer/nanogpt"
	"github.com/looplj/axonhub/llm/transformer/ollama"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/looplj/axonhub/llm/transformer/opencode"
	"github.com/looplj/axonhub/llm/transformer/openrouter"
	"github.com/looplj/axonhub/llm/transformer/xai"
	"github.com/looplj/axonhub/llm/transformer/zai"
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
		case grant.Protocols&model.ProtocolOllamaChat != 0:
			protocol = model.ProtocolOllamaChat
		}
	}

	key := auth.NewStaticKeyProvider(channelKey.Key)
	switch protocol {
	case model.ProtocolOpenAIChatCompletion:
		// 厂商方言优先：bailian / deepseek / openrouter / xai 等一批服务商
		// 说的就是 OpenAI Chat Completions 线协议（实测它们的出站转换器 APIFormat
		// 全部返回 openai/chat_completions，只是内嵌 OpenAI 转换器并覆写报文归一化），
		// 所以按方言选转换器即可，不需要各占一个协议位。
		// generic 方言返回 nil，继续走下面原有逻辑 —— 行为逐字不变。
		if vendor, err := vendorOutboundFor(channel.Dialect, channel.BaseURL, channelKey.Key); vendor != nil || err != nil {
			return vendor, protocol, passthrough, err
		}
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
	case model.ProtocolOllamaChat:
		// Ollama 是**独立线协议**（APIFormat 实测 ollama/chat），不是 OpenAI 的方言，
		// 所以占独立协议位、走这支。它同样自带规范路径，BaseURL 写到服务根即可。
		outbound, err := ollama.NewOutboundTransformerWithConfig(&ollama.Config{
			BaseURL:        channel.BaseURL,
			APIKeyProvider: key,
		})
		return outbound, protocol, passthrough, err
	default:
		return nil, 0, false, fmt.Errorf("channel grant %d supports no known protocol: %d", grant.ID, grant.Protocols)
	}
}

// vendorOutboundFor 按上游方言构造厂商专用的出站转换器；generic 方言返回 (nil, nil)，
// 调用方随后走标准 OpenAI 转换器（即现有行为）。
//
// 为什么用方言而不是协议位：这批服务商的**线协议与 OpenAI 相同** ——
// 实测它们出站转换器的 APIFormat() 全部返回 `openai/chat_completions`
// （实现方式是内嵌 OpenAI 转换器、只覆写 TransformRequest/TransformResponse），
// 差异只在报文体与请求头，正是项目对 Dialect 的定义。
//
// **base_url 一律原样传**，不与 channel.OpenAIChatCompletionPath 合成：
// 这些转换器自带规范路径（在 BaseURL 后拼 /chat/completions），
// 实测把已含路径的地址传进去会拼成 `.../v1/chat/completions/chat/completions`。
// 所以这类渠道的 base_url 写到 /v1 那一层即可（如 https://api.deepseek.com/v1）。
//
// 参数用原始 key 字符串而不是 auth.APIKeyProvider：这十几家的两参构造器
// 签名统一为 (baseURL, apiKey string)，与既有的 key provider 相比少一层包装。
func vendorOutboundFor(dialect model.Dialect, baseURL, apiKey string) (transformer.Outbound, error) {
	switch dialect {
	case model.DialectBailian:
		return bailian.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectCerebras:
		// 这一家没有两参便捷构造器，只有 WithConfig（形状与其它厂商一致）。
		return cerebras.NewOutboundTransformerWithConfig(&cerebras.Config{
			BaseURL:        baseURL,
			APIKeyProvider: auth.NewStaticKeyProvider(apiKey),
		})
	case model.DialectCline:
		return cline.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectDeepSeek:
		return deepseek.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectDoubao:
		return doubao.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectFireworks:
		return fireworks.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectLongcat:
		return longcat.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectModelScope:
		return modelscope.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectMoonshot:
		return moonshot.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectNanoGPT:
		return nanogpt.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectOpenCode:
		return opencode.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectOpenRouter:
		return openrouter.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectXAI:
		return xai.NewOutboundTransformer(baseURL, apiKey)
	case model.DialectZAI:
		return zai.NewOutboundTransformer(baseURL, apiKey)
	default:
		// generic 以及非 OpenAI 线协议的方言（如 antigravity）都走这里。
		return nil, nil
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
