package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/pipeline"
	"github.com/looplj/axonhub/llm/streams"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// upstreamResponse 是已验证但尚未写给客户端的上游成功响应; events 为 nil 表示非流式响应。
// 透传响应保留上游响应头; 跨协议响应由客户端协议决定响应头。失败一律以 error 返回。
type upstreamResponse struct {
	body   []byte                                  // 非流式响应的完整正文。
	header http.Header                             // 同协议透传时需要原样返回的上游响应头。
	events streams.Stream[*httpclient.StreamEvent] // 流式响应中首个事件之后的剩余事件。
	first  *httpclient.StreamEvent                 // 已预读并验证的首个事件。
	last   bool                                    // 首个事件已经终止整个响应流。
	usage  *llm.Usage                              // 上游本次可确认的用量。
	// reportedModel 是上游响应里回报的模型名（T-verify-001）。
	// 用于校验"上游实际用的模型"是否等于"我们请求的模型"——这是发现上游偷换模型的唯一途径。
	// 空串表示上游没回报（属常见现象，此时不做判定）。
	reportedModel string
	// closeIdle 非 nil 时为渠道专用代理独占客户端的空闲连接归还入口, 消费方读完事件流后必须调用。
	// 仅流式响应会带上它: 非流式响应返回时连接已经用完, 由发起方就地归还。
	closeIdle func()
}

// resolveUpstreamClient 按渠道代理配置取得本轮上游请求使用的客户端。
// 第二个返回值非 nil 时说明客户端是为渠道专用代理新建的独占实例, 不进共享连接池, 用完须调用它归还空闲连接。
//
// 出口优先级（R-proxy-001）: 账号级节点 > 渠道级节点 > 既有的「渠道代理开关 / 渠道专用地址 / 全局代理」。
// 账号级存在的理由: 同一站点多账号必须各自走不同出口 IP，否则上游靠"同 IP 多账号"就能把它们关联起来。
//
// 安全取向: 绑定了节点但节点没就绪（未分配本地端口/已停用/已删除）时**报错**，
// 绝不静默退回直连 —— 静默直连正好泄露了用户想藏起来的真实出口。
func resolveUpstreamClient(channel model.Channel) (*http.Client, func(), error) {
	return resolveUpstreamClientForKey(channel, nil)
}

// resolveUpstreamClientForKey 是带上"本轮用的是哪条账号凭据"的版本；
// key 为 nil（探测/无账号上下文）时只看渠道级绑定。
func resolveUpstreamClientForKey(channel model.Channel, key *model.ChannelKey) (*http.Client, func(), error) {
	boundNodeID := channel.ProxyNodeID
	if key != nil && key.ProxyNodeID > 0 {
		boundNodeID = key.ProxyNodeID
	}
	if boundNodeID > 0 {
		endpoint, err := op.ProxyNodeEndpoint(boundNodeID)
		if err != nil {
			return nil, nil, err
		}
		client, err := rhttp.New(endpoint)
		if err != nil {
			return nil, nil, fmt.Errorf("代理节点出口不可用（%s）: %w", endpoint, err)
		}
		return client, client.CloseIdleConnections, nil
	}
	switch {
	case !channel.Proxy:
		client, err := rhttp.Direct()
		return client, nil, err
	case channel.ChannelProxy == "":
		client, err := rhttp.Proxy()
		return client, nil, err
	default:
		client, err := rhttp.New(channel.ChannelProxy)
		if err != nil {
			return nil, nil, err
		}
		return client, client.CloseIdleConnections, nil
	}
}

// sendPassthrough 以同协议透传方式请求上游, 取得的响应无需转换即可回给客户端。
func sendPassthrough(ctx context.Context, format llm.APIFormat, raw *httpclient.Request, channel model.Channel, channelKey model.ChannelKey, outbound transformer.Outbound, streaming bool, modelName string) (*upstreamResponse, error) {
	request, err := buildPassthroughRequest(format, raw, channel, outbound, modelName)
	if err != nil {
		return nil, err
	}
	httpClient, closeIdle, err := resolveUpstreamClientForKey(channel, &channelKey)
	if err != nil {
		return nil, err
	}
	if streaming {
		// 流式响应返回后仍在读上游连接, 归还入口随响应交给消费方; 取不到响应时就地归还。
		result, err := sendPassthroughStream(ctx, format, request, httpClient)
		if err != nil {
			if closeIdle != nil {
				closeIdle()
			}
			return nil, err
		}
		result.closeIdle = closeIdle
		return result, nil
	}
	// 非流式响应在 Do 返回时正文已读完, 连接可以立即归还。
	if closeIdle != nil {
		defer closeIdle()
	}

	response, err := httpclient.NewHttpClientWithClient(httpClient).Do(ctx, request)
	if err != nil {
		var failure *httpclient.Error
		if errors.As(err, &failure) && len(failure.Body) > 0 {
			return nil, fmt.Errorf("%w: %s", err, failure.Body)
		}
		return nil, err
	}
	// 同协议下响应可原样回给客户端, 仍需解析一次以取得用量并识别以 200 下发的失败终态。
	parsed, err := outbound.TransformResponse(ctx, response)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, response.Body)
	}
	if err := validateResponse(format, parsed); err != nil {
		return nil, fmt.Errorf("%w: %s", err, response.Body)
	}
	return &upstreamResponse{
		body:          slices.Clone(response.Body),
		header:        response.Headers.Clone(),
		usage:         parsed.Usage,
		reportedModel: strings.TrimSpace(parsed.Model),
	}, nil
}

// sendPassthroughStream 发起同协议流式请求并预读首个有效事件, 首个事件通过验证才算本轮取得可提交响应。
func sendPassthroughStream(ctx context.Context, format llm.APIFormat, request *httpclient.Request, client *http.Client) (*upstreamResponse, error) {
	rawRequest, err := httpclient.BuildHttpRequest(ctx, request)
	if err != nil {
		return nil, err
	}
	// 客户端的 Accept 属于库自管头不会透传, 需显式声明才能让上游按 SSE 返回。
	rawRequest.Header.Set("Accept", "text/event-stream")

	response, err := client.Do(rawRequest)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		failure, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		// 带上状态码: 上层据此区分「确定性错误」（不重试, 见 retry.go）与「可恢复错误」。
		// 同时把上游的等待提示（Retry-After 一类, T-allocate-002）带上: 流式路径的响应头只在这里可见,
		// 错过它就只能按分组配置的冷却秒数等（上游说等 3 秒也算 60 秒）。
		return nil, &upstreamStatusError{
			status:    response.StatusCode,
			message:   fmt.Sprintf("upstream responded %s: %s", response.Status, failure),
			retryHint: retryAfterFromHeader(response.Header, time.Now()),
		}
	}

	events := httpclient.NewDefaultSSEDecoder(ctx, response.Body)
	for events.Next() {
		event := events.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		last, err := inspectStreamEvent(format, event)
		if err != nil {
			events.Close()
			return nil, fmt.Errorf("%w: %s", err, event.Data)
		}
		return &upstreamResponse{
			header:        response.Header.Clone(),
			events:        events,
			first:         event,
			last:          last,
			reportedModel: reportedModelFromSSE(event.Data),
		}, nil
	}

	err = events.Err()
	events.Close()
	if err == nil {
		err = errors.New("upstream stream ended before first event")
	}
	return nil, err
}

// conversionMiddleware 保存跨协议 pipeline 单次调用需要应用和取得的状态。
type conversionMiddleware struct {
	pipeline.DummyMiddleware               // 提供本次无需处理的其余 pipeline 中间件方法。
	channel                  model.Channel // 本轮上游请求使用的渠道配置。
	format                   llm.APIFormat // 上游渠道协议, 用于校验统一响应终态。
	clientBody               []byte        // 客户端原始请求正文, 用于补回转成 Responses 时被丢掉的采样参数。
	rawBody                  []byte        // 上游非流式响应或错误的原始正文。
	usage                    *llm.Usage    // 非流式统一响应中确认的用量。
	reportedModel            string        // 上游响应里回报的模型名（T-verify-001）。
}

// OnOutboundRawRequest 在转换后的上游请求上应用渠道参数和自定义 Header。
func (m *conversionMiddleware) OnOutboundRawRequest(_ context.Context, request *httpclient.Request) (*httpclient.Request, error) {
	if err := applyChannelConfig(m.channel, request); err != nil {
		return nil, err
	}
	normalizeDeveloperRole(m.format, request)
	body, err := carryOverSamplingParams(m.clientBody, m.format, request.Body)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(body, request.Body) {
		request.Body = body
		if len(request.JSONBody) > 0 {
			request.JSONBody = slices.Clone(body)
		}
	}
	return request, nil
}

// normalizeDeveloperRole 把发给上游的请求里 developer 角色归一化为等价的 system。
//
// 为什么需要（对齐上游 PR #360 对 #19 回归的修复）：developer 是 OpenAI Responses 协议里 system 的替代角色，
// 客户端（Codex 等）会用它携带指令；而不少 OpenAI 兼容中转站只认 system，收到 developer 直接报错。
// 归一化后语义等价、兼容性更好。
//
// 两种上游形状都要盖（T-devrole-001，分协议位实测发现 Responses 形状曾经漏掉）：
//   - chat completions: 角色在顶层 messages 数组里；
//   - responses:       角色在顶层 input 数组里（input 也可能是字符串，或含无 role 的条目，逐项判角色即可）。
func normalizeDeveloperRole(format llm.APIFormat, request *httpclient.Request) {
	if request == nil {
		return
	}
	var path string
	switch format {
	case llm.APIFormatOpenAIChatCompletion:
		path = "messages"
	case llm.APIFormatOpenAIResponse:
		path = "input"
	default:
		return
	}
	body := request.Body
	if len(body) == 0 {
		body = request.JSONBody
	}
	if len(body) == 0 {
		return
	}
	items := gjson.GetBytes(body, path)
	if !items.IsArray() {
		return
	}
	rewritten := body
	changed := false
	for index, item := range items.Array() {
		if item.Get("role").String() != "developer" {
			continue
		}
		next, err := sjson.SetBytes(rewritten, fmt.Sprintf("%s.%d.role", path, index), "system")
		if err != nil {
			return
		}
		rewritten = next
		changed = true
	}
	if !changed {
		return
	}
	request.Body = rewritten
	if len(request.JSONBody) > 0 {
		request.JSONBody = slices.Clone(rewritten)
	}
}

// responsesCarriedParams 是转换成 Responses 上游时会被转换层丢掉的采样参数（NM-DS-005 实测：
// 客户端 Chat / Anthropic 入、上游 Responses 出时 temperature 不会出现在上游请求体里，
// 而同一份请求直通 Responses 时它是在的）。两个键都是 Responses API 的合法字段，
// 只在"客户端给了、上游没有"时补，不会引入非法参数，也不覆盖转换层或渠道覆盖写下的值。
var responsesCarriedParams = []string{"temperature", "top_p"}

// carryOverSamplingParams 把客户端请求里的采样参数补进上游请求体；目标协议不是 Responses、
// 客户端正文不是 JSON 对象、或上游已有该键时都不改动（返回原正文）。
func carryOverSamplingParams(clientBody []byte, format llm.APIFormat, outboundBody []byte) ([]byte, error) {
	if format != llm.APIFormatOpenAIResponse || len(clientBody) == 0 || len(outboundBody) == 0 {
		return outboundBody, nil
	}
	var client map[string]json.RawMessage
	if err := json.Unmarshal(clientBody, &client); err != nil {
		return outboundBody, nil
	}
	var outbound map[string]json.RawMessage
	if err := json.Unmarshal(outboundBody, &outbound); err != nil {
		return outboundBody, nil
	}
	body := outboundBody
	for _, key := range responsesCarriedParams {
		value, ok := client[key]
		if !ok {
			continue
		}
		if _, exists := outbound[key]; exists {
			continue
		}
		next, err := sjson.SetRawBytes(body, ":"+key, value)
		if err != nil {
			return outboundBody, fmt.Errorf("carry over %s: %w", key, err)
		}
		body = next
	}
	return body, nil
}

// OnOutboundRawError 保留上游错误状态码携带的原始正文。
func (m *conversionMiddleware) OnOutboundRawError(_ context.Context, err error) {
	var failure *httpclient.Error
	if errors.As(err, &failure) {
		m.rawBody = slices.Clone(failure.Body)
	}
}

// OnOutboundRawResponse 保留上游成功响应的原始正文, 供后续转换或终态校验失败时诊断。
func (m *conversionMiddleware) OnOutboundRawResponse(_ context.Context, response *httpclient.Response) (*httpclient.Response, error) {
	m.rawBody = slices.Clone(response.Body)
	return response, nil
}

// OnOutboundLlmResponse 取得非流式用量并在回转客户端协议前校验上游终态。
func (m *conversionMiddleware) OnOutboundLlmResponse(_ context.Context, response *llm.Response) (*llm.Response, error) {
	if err := validateResponse(m.format, response); err != nil {
		return nil, err
	}
	m.usage = response.Usage
	// 记下上游回报的模型名（T-verify-001）。这里是唯一能拿到上游"自称用了什么模型"的位置：
	// 它必须在校验通过之后、响应回转客户端之前取得，早于转换层把 model 字段改写成客户端请求的名字。
	m.reportedModel = strings.TrimSpace(response.Model)
	return response, nil
}

// reportedModelFromSSE 从流式事件正文里取出上游回报的模型名（T-verify-001）。
//
// 取首个事件即可：同一条流的每个 chunk 都带同一个 model，而首事件是**已经预读并校验过**
// 的那一个（调用点就在校验之后），不必额外等待后续事件。
//
// **两种形态都要取**：OpenAI 的 chunk 把 model 放在顶层；Anthropic 的 message_start
// 事件放在 message.model（嵌套一层）。只读顶层会让 Anthropic 流式请求永远拿不到模型名，
// 等于这条协议下完全没有校验能力 —— 实测踩到（单测抓到）。
//
// 解析失败或没有该字段时返回空串 —— 空串在判定处表示"上游没回报"，不参与比对。
func reportedModelFromSSE(data []byte) string {
	if model := strings.TrimSpace(gjson.GetBytes(data, "model").String()); model != "" {
		return model
	}
	return strings.TrimSpace(gjson.GetBytes(data, "message.model").String())
}

// modelMismatch 判断上游回报的模型与请求的模型是否不一致（T-verify-001）。
//
// 只在双方都有值时判定。上游不回报模型名是普遍现象（部分站点省略该字段），
// 把"没回报"当成"不匹配"会让绝大多数正常请求被误标，这个标记就没人看了。
//
// 比较是**大小写不敏感**的：同一个模型名在不同站点的大小写写法不同（gpt-4o / GPT-4O），
// 把它当不一致会产生大量噪音。
func modelMismatch(requested, reported string) bool {
	requested = strings.TrimSpace(requested)
	reported = strings.TrimSpace(reported)
	if requested == "" || reported == "" {
		return false
	}
	return !strings.EqualFold(requested, reported)
}

// sendConverted 经 axonhub pipeline 把客户端请求转换成渠道协议后请求上游, 响应再转换回客户端协议。
func sendConverted(ctx context.Context, format llm.APIFormat, raw *httpclient.Request, channel model.Channel, channelKey model.ChannelKey, outbound transformer.Outbound, streaming bool) (*upstreamResponse, error) {
	var inbound transformer.Inbound
	switch format {
	case llm.APIFormatOpenAIResponse:
		inbound = responses.NewInboundTransformer()
	case llm.APIFormatAnthropicMessage:
		inbound = anthropic.NewInboundTransformer()
	default:
		inbound = openai.NewInboundTransformer()
	}

	httpClient, closeIdle, err := resolveUpstreamClientForKey(channel, &channelKey)
	if err != nil {
		return nil, err
	}
	// 流式响应要等消费方读完才能归还连接, 故只在提交流式结果那一处移交, 其余出口一律就地归还。
	committed := false
	if closeIdle != nil {
		defer func() {
			if !committed {
				closeIdle()
			}
		}()
	}
	middleware := &conversionMiddleware{channel: channel, format: outbound.APIFormat(), clientBody: slices.Clone(raw.Body)}
	processor := pipeline.NewFactory(httpclient.NewHttpClientWithClient(httpClient)).Pipeline(
		inbound,
		outbound,
		pipeline.WithMiddlewares(middleware),
	)
	result, err := processor.Process(ctx, raw)
	if err != nil {
		if len(middleware.rawBody) > 0 {
			return nil, fmt.Errorf("%w: %s", err, middleware.rawBody)
		}
		return nil, err
	}
	if !streaming {
		return &upstreamResponse{
			body:          slices.Clone(result.Response.Body),
			usage:         middleware.usage,
			reportedModel: middleware.reportedModel,
		}, nil
	}

	events := result.EventStream
	for events.Next() {
		event := events.Current()
		if event == nil || len(event.Data) == 0 {
			continue
		}
		last, err := inspectStreamEvent(format, event)
		if err != nil {
			events.Close()
			return nil, fmt.Errorf("%w: %s", err, event.Data)
		}
		committed = true
		return &upstreamResponse{
			events:        events,
			first:         event,
			last:          last,
			closeIdle:     closeIdle,
			reportedModel: reportedModelFromSSE(event.Data),
		}, nil
	}

	err = events.Err()
	events.Close()
	if err == nil {
		err = errors.New("upstream stream ended before first event")
	}
	return nil, err
}
