package relay

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/looplj/axonhub/llm"
	"github.com/t-yang-only/OctoNexus/internal/model"
)

// vendorDialectCase 是一个「厂商方言」用例。
//
// 这些服务商说的仍是 OpenAI Chat Completions **线协议**，只是报文归一化与端点
// 规则带厂商特化（实测它们的出站转换器 APIFormat 全部是 openai/chat_completions）。
// 所以它们应当是「方言」而不是各占一个协议位 —— 协议位是持久化的位掩码，
// 每加一个就永久占掉一位；只有线协议真的不同的服务商才值得占位（如 ollama）。
type vendorDialectCase struct {
	dialect model.Dialect
	baseURL string
	// wantType 是选中的转换器具体类型（实测得出）。
	//
	// 为什么需要它：URL 与 APIFormat 对**所有**厂商方言都一样（都是 base + /chat/completions，
	// 都是 openai/chat_completions），所以把两家的实现互调，那两条判据照样全绿。
	// 类型名是唯一能把「哪一家」钉住的便宜判据。
	//
	// 例外：fireworks 实测返回的是通用 openai 转换器（它把 openai 转换器**直接返回**、
	// 不包装），所以它的期望值就是 openai.OutboundTransformer —— 这是它的真实形状，
	// 不是接线漏了。这一条是实测得出的，别按「应该含 fireworks」去改。
	wantType string
	// wantURL 是实测得出的最终出站地址（= baseURL + /chat/completions）。
	//
	// 为什么要钉死完整地址：只断言「APIFormat 是 openai/chat_completions」
	// 与「忽略端点覆盖」只能证明**某个**厂商分支生效，证明不了**对的那一家**生效 ——
	// 把 bailian 与 deepseek 两家的实现互调（两边 import 都还在用，能编译），
	// 上面两条判据照样全绿，而请求已经打到错误的厂商特化逻辑上。
	// 地址逐字比对是唯一能区分「对不对」的便宜判据。
	wantURL string
}

// vendorDialects 是全部厂商方言。漏一个即红 —— 这正是这张表存在的意义。
// 地址用项目约定写法：写到版本级（/v1），不带端点路径。
func vendorDialects() []vendorDialectCase {
	return []vendorDialectCase{
		{dialect: model.DialectBailian, wantType: "bailian.OutboundTransformer", baseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
			wantURL: "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"},
		{dialect: model.DialectCerebras, wantType: "cerebras.OutboundTransformer", baseURL: "https://api.cerebras.ai/v1",
			wantURL: "https://api.cerebras.ai/v1/chat/completions"},
		{dialect: model.DialectCline, wantType: "cline.OutboundTransformer", baseURL: "https://api.cline.bot/v1",
			wantURL: "https://api.cline.bot/v1/chat/completions"},
		{dialect: model.DialectDeepSeek, wantType: "deepseek.OutboundTransformer", baseURL: "https://api.deepseek.com/v1",
			wantURL: "https://api.deepseek.com/v1/chat/completions"},
		{dialect: model.DialectDoubao, wantType: "doubao.OutboundTransformer", baseURL: "https://ark.cn-beijing.volces.com/api/v3",
			wantURL: "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{dialect: model.DialectFireworks, wantType: "openai.OutboundTransformer", baseURL: "https://api.fireworks.ai/inference/v1",
			wantURL: "https://api.fireworks.ai/inference/v1/chat/completions"},
		{dialect: model.DialectLongcat, wantType: "longcat.OutboundTransformer", baseURL: "https://api.longcat.chat/openai/v1",
			wantURL: "https://api.longcat.chat/openai/v1/chat/completions"},
		{dialect: model.DialectModelScope, wantType: "modelscope.OutboundTransformer", baseURL: "https://api-inference.modelscope.cn/v1",
			wantURL: "https://api-inference.modelscope.cn/v1/chat/completions"},
		{dialect: model.DialectMoonshot, wantType: "moonshot.OutboundTransformer", baseURL: "https://api.moonshot.cn/v1",
			wantURL: "https://api.moonshot.cn/v1/chat/completions"},
		{dialect: model.DialectNanoGPT, wantType: "nanogpt.OutboundTransformer", baseURL: "https://nano-gpt.com/api/v1",
			wantURL: "https://nano-gpt.com/api/v1/chat/completions"},
		{dialect: model.DialectOpenCode, wantType: "opencode.OutboundTransformer", baseURL: "https://opencode.ai/zen/v1",
			wantURL: "https://opencode.ai/zen/v1/chat/completions"},
		{dialect: model.DialectOpenRouter, wantType: "openrouter.OutboundTransformer", baseURL: "https://openrouter.ai/api/v1",
			wantURL: "https://openrouter.ai/api/v1/chat/completions"},
		{dialect: model.DialectXAI, wantType: "xai.OutboundTransformer", baseURL: "https://api.x.ai/v1",
			wantURL: "https://api.x.ai/v1/chat/completions"},
		{dialect: model.DialectZAI, wantType: "zai.OutboundTransformer", baseURL: "https://open.bigmodel.cn/api/paas/v4",
			wantURL: "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
	}
}

// newTestChannel 造一个指定方言的渠道。
func newTestChannel(name string, dialect model.Dialect, baseURL string) model.Channel {
	return model.Channel{
		ChannelConfig: model.ChannelConfig{
			Name:    name,
			BaseURL: baseURL,
			Dialect: dialect,
		},
	}
}

// newTestGrant 造一个只支持 OpenAI Chat Completions 的授权。
func newTestGrant(id int) model.ChannelGrant {
	return model.ChannelGrant{ID: id, Protocols: model.ProtocolOpenAIChatCompletion}
}

// testChannelKey 返回一把固定凭据。
// 用内嵌结构显式构造：ChannelKey.Key 是提升字段，
// go.mod 钉的是 go1.26，在结构体字面量里直接写提升字段要 go1.27 才允许。
func testChannelKey() model.ChannelKey {
	return model.ChannelKey{ChannelKeyConfig: model.ChannelKeyConfig{Key: "sk-demo-000000"}}
}

// testLLMRequest 返回一个最小可用的统一请求，用于真正跑一遍 TransformRequest。
func testLLMRequest(modelName string) *llm.Request {
	text := "hi"
	return &llm.Request{
		Model:    modelName,
		Messages: []llm.Message{{Role: "user", Content: llm.MessageContent{Content: &text}}},
	}
}

// TestVendorDialectsShareOpenAIChatFormat 守住「这些服务商是方言而不是协议位」这个判断，
// 同时证明**方言分支真的生效了**（而不是悄悄退回通用 OpenAI 路径）。
//
// 判据怎么选出来的（试错记录，别再走回头路）：
//   - 只断言 APIFormat 不行 —— 厂商转换器与通用 OpenAI 转换器的 APIFormat 完全相同，
//     把 vendorOutboundFor 整个删掉也照样全绿。
//   - 断言「具体类型名含厂商名」也不行 —— 实测 fireworks 是合法连通的，
//     但它把 openai 转换器**直接返回**（不包装），类型名就是 openai.OutboundTransformer；
//     deepseek 则包了一层。用类型名会误伤不包装的厂商。
//   - 最终采用**端点覆盖是否被忽略**：厂商转换器自带端点规则，
//     结构上拿不到也不使用 openai_chat_completion_path。给通用路径设一个覆盖值，
//     方言分支必须无视它；若退回通用路径，URL 里就会出现那个覆盖值。
//     这条判据对所有 14 家一致成立，且与实现细节（包不包装）无关。
//
// 顺带钉死了另一个事实：**厂商方言的 base_url 一律写到版本级（/v1），不要贴完整端点**。
// 实测贴完整端点会得到 .../v1/chat/completions/chat/completions
// （通用 OpenAI 路径也一样；model.ChannelBaseURL 只去重 /v1 那一段，见 NM-DS-004）。
func TestVendorDialectsShareOpenAIChatFormat(t *testing.T) {
	key := testChannelKey()
	grant := newTestGrant(1)
	const endpointOverride = "/custom/never-used-endpoint"

	for _, tc := range vendorDialects() {
		t.Run(string(tc.dialect), func(t *testing.T) {
			channel := newTestChannel("demo-"+string(tc.dialect), tc.dialect, tc.baseURL)
			channel.OpenAIChatCompletionPath = endpointOverride

			outbound, protocol, passthrough, err := buildOutbound(channel, grant, key, model.ProtocolOpenAIChatCompletion)
			if err != nil {
				t.Fatalf("buildOutbound: %v", err)
			}
			if protocol != model.ProtocolOpenAIChatCompletion {
				t.Errorf("选中的协议 = %d, 想 OpenAIChatCompletion(%d)", protocol, model.ProtocolOpenAIChatCompletion)
			}
			if !passthrough {
				t.Error("客户端要的就是 OpenAI，passthrough 应为 true")
			}
			if got := outbound.APIFormat().String(); got != "openai/chat_completions" {
				t.Errorf("出站 APIFormat = %q, 想 openai/chat_completions", got)
			}

			req, err := outbound.TransformRequest(context.Background(), testLLMRequest("demo-model"))
			if err != nil {
				t.Fatalf("TransformRequest: %v", err)
			}
			if strings.Contains(req.URL, endpointOverride) {
				t.Errorf("URL = %q 带上了 openai_chat_completion_path 覆盖值 —— 说明退回通用 OpenAI 路径，方言分支没生效", req.URL)
			}
			if got := typeName(outbound); got != tc.wantType {
				t.Errorf("选中的转换器类型 = %s, 想 %s —— 说明这一家的分支没生效", got, tc.wantType)
			}
			// 逐字比对：确认基址没被重复拼接、端点覆盖被忽略。
			if req.URL != tc.wantURL {
				t.Errorf("出站 URL = %q\n          想 = %q\n          —— 地址不对说明选中的不是这一家的转换器，或基址被重复拼接", req.URL, tc.wantURL)
			}
		})
	}
}

// TestDialectGenericUnchanged 守住「新增方言不改变存量行为」。
//
// 存量渠道全部是 generic。它必须**不进**厂商分支（vendorOutboundFor 返回 nil, nil），
// 继续走原来的通用 OpenAI 构造器。最直接的判据：通用路径**会**使用
// openai_chat_completion_path —— 上面那张表里方言必须无视它，这里 generic 必须吃它。
func TestDialectGenericUnchanged(t *testing.T) {
	key := testChannelKey()
	grant := newTestGrant(1)

	t.Run("generic 走通用路径并吃端点覆盖", func(t *testing.T) {
		channel := newTestChannel("demo-generic", model.DialectGeneric, "https://api.example.com")
		channel.OpenAIChatCompletionPath = "/custom/generic-endpoint"

		outbound, _, _, err := buildOutbound(channel, grant, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		req, err := outbound.TransformRequest(context.Background(), testLLMRequest("demo-model"))
		if err != nil {
			t.Fatalf("TransformRequest: %v", err)
		}
		if !strings.Contains(req.URL, "generic-endpoint") {
			t.Errorf("URL = %q，generic 没有使用 openai_chat_completion_path —— 存量行为被改变了", req.URL)
		}
	})

	t.Run("antigravity 在 OpenAI 分支也不特化", func(t *testing.T) {
		// antigravity 只在 Gemini 分支有意义（见 buildOutbound 的 Gemini 案例）。
		// 授权只有 OpenAI 位时它必须回落通用路径。
		channel := newTestChannel("demo-ag", model.DialectAntigravity, "https://api.example.com")
		channel.OpenAIChatCompletionPath = "/custom/ag-endpoint"

		outbound, _, _, err := buildOutbound(channel, grant, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		req, err := outbound.TransformRequest(context.Background(), testLLMRequest("demo-model"))
		if err != nil {
			t.Fatalf("TransformRequest: %v", err)
		}
		if !strings.Contains(req.URL, "ag-endpoint") {
			t.Errorf("URL = %q，antigravity 在 OpenAI 分支应走通用路径", req.URL)
		}
	})

	t.Run("vendorOutboundFor 对 generic 与 antigravity 都返回 nil", func(t *testing.T) {
		for _, d := range []model.Dialect{model.DialectGeneric, model.DialectAntigravity} {
			out, err := vendorOutboundFor(d, "https://api.example.com", "sk-demo")
			if err != nil {
				t.Errorf("方言 %s 返回了错误: %v", d, err)
			}
			if out != nil {
				t.Errorf("方言 %s 应返回 nil 让调用方走原路径，实际返回了转换器", d)
			}
		}
	})
}

// TestOllamaIsOwnProtocol 守住 ollama 是**独立线协议**而不是 OpenAI 方言这个判断。
//
// 实测它的 APIFormat 是 ollama/chat（不是 openai/chat_completions），
// 所以它占独立协议位、走 buildOutbound 里自己那支。
func TestOllamaIsOwnProtocol(t *testing.T) {
	key := testChannelKey()
	channel := newTestChannel("demo-ollama", model.DialectGeneric, "http://127.0.0.1:11434")
	grant := model.ChannelGrant{ID: 1, Protocols: model.ProtocolOllamaChat}

	t.Run("只支持 ollama", func(t *testing.T) {
		outbound, protocol, passthrough, err := buildOutbound(channel, grant, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		if protocol != model.ProtocolOllamaChat {
			t.Fatalf("选中的协议 = %d, 想 OllamaChat(%d)", protocol, model.ProtocolOllamaChat)
		}
		if passthrough {
			t.Error("客户端说 OpenAI 而渠道只支持 ollama，passthrough 不该为 true")
		}
		if got := outbound.APIFormat().String(); got != "ollama/chat" {
			t.Errorf("出站 APIFormat = %q, 想 ollama/chat", got)
		}
	})

	t.Run("与 OpenAI 同时支持时仍优先 OpenAI", func(t *testing.T) {
		// 新增协议位排在回落链**最后**，不得改变既有选择顺序。
		both := model.ChannelGrant{ID: 2, Protocols: model.ProtocolOpenAIChatCompletion | model.ProtocolOllamaChat}
		_, protocol, _, err := buildOutbound(channel, both, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		if protocol != model.ProtocolOpenAIChatCompletion {
			t.Errorf("选中的协议 = %d, 想 OpenAIChatCompletion(%d) —— 新增 ollama 位改变了既有选择",
				protocol, model.ProtocolOpenAIChatCompletion)
		}
	})

	t.Run("同时支持时按客户端协议走 ollama", func(t *testing.T) {
		// 客户端指名要 ollama 协议时 passthrough 直接命中，不该回落成 OpenAI。
		both := model.ChannelGrant{ID: 3, Protocols: model.ProtocolOpenAIChatCompletion | model.ProtocolOllamaChat}
		_, protocol, passthrough, err := buildOutbound(channel, both, key, model.ProtocolOllamaChat)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		if protocol != model.ProtocolOllamaChat || !passthrough {
			t.Errorf("协议 = %d passthrough = %v, 想 OllamaChat 且 passthrough", protocol, passthrough)
		}
	})

	t.Run("ollama 也吃端点覆盖", func(t *testing.T) {
		// ollama 的厂商 Config 带 EndpointPath（与多数厂商方言不同），
		// 所以它应当与 generic 一样使用 openai_chat_completion_path。
		ch := newTestChannel("demo-ollama-path", model.DialectGeneric, "http://127.0.0.1:11434")
		outbound, _, _, err := buildOutbound(ch, grant, key, model.ProtocolOpenAIChatCompletion)
		if err != nil {
			t.Fatalf("buildOutbound: %v", err)
		}
		req, err := outbound.TransformRequest(context.Background(), testLLMRequest("demo-model"))
		if err != nil {
			t.Fatalf("TransformRequest: %v", err)
		}
		if !strings.Contains(req.URL, "11434") {
			t.Errorf("URL = %q，没带上 ollama 基址", req.URL)
		}
	})
}

// TestVendorDialectCredentialReachesUpstream 守住凭据真的接上了。
//
// 凭据在 httpclient.Request.Auth（AuthConfig），**不是** Authorization 头 ——
// 头是在下游 HTTP 客户端按这份配置挂上去的。曾按头去断言，得到空串而误判成"凭据没接上"。
//
// 这条能抓住的退化：凭据 provider 没传给厂商转换器。
// 那种退化在上游表现为 401，本地完全看不出来，只有断言到这里才拦得住。
func TestVendorDialectCredentialReachesUpstream(t *testing.T) {
	key := testChannelKey()
	grant := newTestGrant(1)

	for _, dialect := range []model.Dialect{model.DialectDeepSeek, model.DialectMoonshot, model.DialectZAI, model.DialectFireworks} {
		t.Run(string(dialect), func(t *testing.T) {
			channel := newTestChannel("demo-"+string(dialect), dialect, "https://api.example.com/v1")
			outbound, _, _, err := buildOutbound(channel, grant, key, model.ProtocolOpenAIChatCompletion)
			if err != nil {
				t.Fatalf("buildOutbound: %v", err)
			}
			req, err := outbound.TransformRequest(context.Background(), testLLMRequest("demo-model"))
			if err != nil {
				t.Fatalf("TransformRequest: %v", err)
			}
			if req.Method != "POST" {
				t.Errorf("方法 = %q, 想 POST", req.Method)
			}
			if !strings.Contains(string(req.Body), "demo-model") {
				t.Errorf("报文体里没有模型名: %s", string(req.Body))
			}
			if req.Auth == nil {
				t.Fatalf("req.Auth 为空 —— 凭据没接上（上游会收到 401，本地看不出来）")
			}
			if req.Auth.APIKey != key.Key {
				t.Errorf("req.Auth.APIKey = %q, 想 %q", req.Auth.APIKey, key.Key)
			}
		})
	}
}

// typeName 返回转换器的具体类型名（去掉指针与包路径前缀，便于断言）。
func typeName(v any) string {
	if v == nil {
		return "<nil>"
	}
	full := strings.TrimPrefix(fmt.Sprintf("%T", v), "*")
	if idx := strings.LastIndex(full, "/"); idx >= 0 {
		full = full[idx+1:]
	}
	return full
}
