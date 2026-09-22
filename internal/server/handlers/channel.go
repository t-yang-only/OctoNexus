package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/price"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/dlclark/regexp2"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/channel").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/detail/:id", http.MethodGet).
				Handle(getChannelDetail),
		).
		AddRoute(
			router.NewRoute("/stats", http.MethodGet).
				Handle(listChannelStats),
		).
		// 可用性诊断（T-usability-001）：把「配了但用不上」的模型与原因列出来。
		AddRoute(
			router.NewRoute("/diagnose", http.MethodGet).
				Handle(diagnoseChannels),
		).
		AddRoute(
			router.NewRoute("/grants", http.MethodGet).
				Handle(listChannelGrant),
		).
		AddRoute(
			router.NewRoute("/create", http.MethodPost).
				Handle(createChannel),
		).
		AddRoute(
			router.NewRoute("/update", http.MethodPost).
				Handle(updateChannel),
		).
		AddRoute(
			router.NewRoute("/enable", http.MethodPost).
				Handle(enableChannel),
		).
		AddRoute(
			router.NewRoute("/delete/:id", http.MethodDelete).
				Handle(deleteChannel),
		).
		AddRoute(
			router.NewRoute("/fetch-model", http.MethodPost).
				Handle(fetchModel),
		)
}

// getChannelDetail 返回单个渠道的完整配置, 供编辑表单打开时读取。
// 与列表分开: 整份配置带着路径, 代理与凭据明文, 只有正在编辑的那一个渠道用得上。
func getChannelDetail(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	detail, err := op.ChannelDetailGet(id)
	if err != nil {
		resp.Error(c, http.StatusNotFound, err.Error())
		return
	}
	resp.Success(c, detail)
}

// listChannelStats 返回全部渠道及其模型的累计统计, 也是渠道列表页的数据来源。
// 不带整份配置: 统计每次转发都在变, 界面按更短的间隔刷新它, 而路径, 代理与凭据明文只在编辑时用得上。
func listChannelStats(c *gin.Context) {
	resp.Success(c, op.ChannelStatsList())
}

// diagnoseChannels 返回各渠道的可用性诊断（T-usability-001）。
//
// 回答的是「我配好的模型为什么用不上」：一个模型要能被客户端调用，
// 必须三层齐全（模型 → 凭据授权 → 分组），任何一层断掉的表现都是
// 客户端报 model not found，而界面上完全看不出是哪一层。
// 这里把断点与可执行的原因一并列出来。
func diagnoseChannels(c *gin.Context) {
	channelID := 0
	if raw := strings.TrimSpace(c.Query("channel_id")); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
			return
		}
		channelID = id
	}
	items, err := op.ChannelDiagnoseAll(c.Request.Context(), channelID)
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, gin.H{
		"summary":  op.SummarizeChannelDiagnose(items),
		"reasons":  op.DiagnoseReasonCounts(items),
		"channels": items,
	})
}

// listChannelGrant 返回全部渠道授权候选, 供分组页选取成员。
func listChannelGrant(c *gin.Context) {
	resp.Success(c, op.ChannelGrantCandidates())
}

func createChannel(c *gin.Context) {
	var req model.ChannelDetail
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	channel, err := op.ChannelCreate(&req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := addChannelModelPrices(channel.Models, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, channel)
}

func updateChannel(c *gin.Context) {
	var req model.ChannelDetail
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if req.ID == 0 {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	channel, err := op.ChannelUpdate(&req, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := addChannelModelPrices(channel.Models, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := op.LLMCleanupGhosts(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, channel)
}

func enableChannel(c *gin.Context) {
	var request struct {
		ID      int  `json:"id"`
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	if err := op.ChannelEnabled(request.ID, request.Enabled, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

func deleteChannel(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidParam)
		return
	}
	if err := op.ChannelDel(id, c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	if err := op.LLMCleanupGhosts(c.Request.Context()); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, nil)
}

// addChannelModelPrices 为渠道模型匹配校准价格，并批量写入尚不存在的价格记录。
func addChannelModelPrices(modelNames []string, ctx context.Context) error {
	seen := make(map[string]struct{}, len(modelNames))
	llmInfos := make([]model.LLMInfo, 0, len(modelNames))
	for _, modelName := range modelNames {
		modelName = strings.ToLower(modelName)
		if _, ok := seen[modelName]; ok {
			continue
		}
		seen[modelName] = struct{}{}
		llmInfo := model.LLMInfo{Name: modelName}
		if modelPrice := price.GetLLMPrice(modelName); modelPrice != nil {
			llmInfo.LLMPrice = *modelPrice
		}
		llmInfos = append(llmInfos, llmInfo)
	}
	return op.LLMBatchCreate(llmInfos, ctx)
}

// fetchModel 按提交的渠道配置与凭据拉取上游模型列表, 并按过滤表达式筛选后返回。
// 同时探测 OpenAI 与 Anthropic 两侧, 谁返回了哪些模型, 就给对应协议位打勾: 协议支持由探测结果决定, 无需用户声明。
// OpenAI 侧记为 Responses 而不是 Chat: Chat Completions 已被官方标记弃用, 新渠道应默认走 Responses,
// 仍需 Chat 的渠道由用户在界面上手动勾选。两侧的 /models 地址与认证形态不同, 故必须分别探测:
// 单协议上游只有一侧会成功, "哪侧成功" 本身就是协议支持的证据。
// 只有两侧都失败才算失败; 一侧失败属正常情况, 单协议上游本就只有一侧讲得通, 按成功那侧的结果返回。
func fetchModel(c *gin.Context) {
	var request model.ChannelFetchModelRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		resp.Error(c, http.StatusBadRequest, resp.ErrInvalidJSON)
		return
	}
	ctx := c.Request.Context()
	// 探测收的是尚未落库的提交配置, 不经 normalizeChannelConfig, 故在此自行去空白;
	// 其中只有地址是硬需求: 渠道尚未命名时也可试拉, 故名称不在此校验。
	target := request.Channel
	target.BaseURL = strings.TrimSpace(target.BaseURL)
	target.ChannelProxy = strings.TrimSpace(target.ChannelProxy)
	target.MatchRegex = strings.TrimSpace(target.MatchRegex)
	if target.BaseURL == "" {
		resp.Error(c, http.StatusBadRequest, "channel base url is required")
		return
	}

	var httpClient *http.Client
	var err error
	switch {
	case !target.Proxy:
		httpClient, err = rhttp.Direct()
	case target.ChannelProxy == "":
		httpClient, err = rhttp.Proxy()
	default:
		httpClient, err = rhttp.New(target.ChannelProxy)
		// 渠道专用代理的客户端不再共享, 探测完就得关掉空闲连接; 探测收的是未落库的输入, 留着也无从复用。
		if httpClient != nil {
			defer httpClient.CloseIdleConnections()
		}
	}
	if err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}

	var openaiModels, anthropicModels []string
	var openaiErr, anthropicErr error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		openaiModels, openaiErr = fetchOpenAIModels(httpClient, ctx, target, request.Key, modelsURL(target.BaseURL, model.EndpointPathOrDefault(target.OpenAIResponsePath, "/v1/responses")))
	}()
	go func() {
		defer wg.Done()
		anthropicModels, anthropicErr = fetchAnthropicModels(httpClient, ctx, target, request.Key, modelsURL(target.BaseURL, model.EndpointPathOrDefault(target.AnthropicMessagePath, "/v1/messages")))
	}()
	wg.Wait()

	if openaiErr != nil && anthropicErr != nil {
		// 上游鉴权失败或地址不通属于调用方配置问题, 按 502 返回并带上上游原文, 便于在界面上直接看到原因。
		resp.Error(c, http.StatusBadGateway, fmt.Sprintf("openai: %v; anthropic: %v", openaiErr, anthropicErr))
		return
	}

	var re, reGlobal *regexp2.Regexp
	if target.MatchRegex != "" {
		if re, err = regexp2.Compile(target.MatchRegex, regexp2.ECMAScript); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}
	// 全局过滤由设置页维护, 与渠道过滤同取 AND: 模型须同时通过两枚正则才保留, 留空的一侧不生效。
	// 设置缺失按不过滤处理: 启动初始化会补齐默认值, 缺行只可能出现在旧库尚未刷新的瞬间。
	globalFilter, _ := op.SettingGetString(model.SettingKeyModelFilter)
	if globalFilter != "" {
		if reGlobal, err = regexp2.Compile(globalFilter, regexp2.ECMAScript); err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
	}

	// 模型名须同时通过渠道与全局两枚过滤正则, 编译与匹配错误统一按请求错误返回。
	matches := func(name string) (bool, error) {
		if re != nil {
			matched, err := re.MatchString(name)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		if reGlobal != nil {
			matched, err := reGlobal.MatchString(name)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	}

	// 两侧结果按名称合并成一份有序集合: 同名模型在两侧都出现时, 协议位取并集。
	// 保持首次出现的顺序, 界面上模型的排列才与上游返回的一致;
	// 先并入 OpenAI 再并入 Anthropic, 顺序写死而不用 map 遍历, 否则界面上的模型排列会随每次刷新变化。
	protocolsByModel := make(map[string]model.Protocol, len(openaiModels)+len(anthropicModels))
	order := make([]string, 0, len(openaiModels)+len(anthropicModels))
	for _, name := range openaiModels {
		matched, err := matches(name)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if !matched {
			continue
		}
		if _, ok := protocolsByModel[name]; !ok {
			order = append(order, name)
		}
		protocolsByModel[name] |= model.ProtocolOpenAIResponse
	}
	for _, name := range anthropicModels {
		matched, err := matches(name)
		if err != nil {
			resp.Error(c, http.StatusBadRequest, err.Error())
			return
		}
		if !matched {
			continue
		}
		if _, ok := protocolsByModel[name]; !ok {
			order = append(order, name)
		}
		protocolsByModel[name] |= model.ProtocolAnthropicMessage
	}

	if !request.Probe {
		models := make([]model.ChannelFetchModel, 0, len(order))
		for _, name := range order {
			models = append(models, model.ChannelFetchModel{Name: name, Protocols: protocolsByModel[name]})
		}
		resp.Success(c, models)
		return
	}

	// 实测模式: /models 只用来圈定候选模型集合, 每个模型对 chat/response/message 三协议
	// 各发一次最小请求, 协议位完全以实测结论为准, 不再采用列表归属推断:
	// 列表侧只能旁证 "该 /models 讲得通", 证明不了单个模型在某个转发端点上可用。
	models := probeModels(httpClient, ctx, target, request.Key, order)
	resp.Success(c, models)
}

// probeModelTimeout 是单个模型单个协议实测的等待上限。
// 拉取 + 实测总时长须可接受: 候选 N 个模型 × 3 协议并发执行, 单发 20 秒封顶。
const probeModelTimeout = 20 * time.Second

// probeModelBodyLimit 截断失败响应正文, 只留诊断所需摘要。
const probeModelBodyLimit = 256

// probeModels 对候选模型逐一实测三协议, 返回带实测明细的模型列表。
// 模型与模型之间并发, 同一模型的三协议也并发: 单凭据一次拉取的总时长约为最慢一组合, 而非逐个累加。
func probeModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key string, order []string) []model.ChannelFetchModel {
	// 上游对并发的容忍远低于拉模型列表, 限在 4: 模型数常达数十个, 不限流会触发上游限频。
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	results := make([]model.ChannelFetchModel, len(order))
	for i, name := range order {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			probes := probeSingleModel(httpClient, ctx, target, key, name)
			protocols := model.Protocol(0)
			for _, probe := range probes {
				if probe.OK {
					protocols |= probe.Protocol
				}
			}
			results[i] = model.ChannelFetchModel{Name: name, Protocols: protocols, Probes: probes}
		}(i, name)
	}
	wg.Wait()
	return results
}

// probeSingleModel 对单个模型实测三协议全部端点, 返回各协议结论, 按位值定序保证明细稳定。
// 不按 /models 归属裁剪候选: 网关常只在一侧列模型却在多侧可转发, 列表旁证不可信, 以实测为准。
func probeSingleModel(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, modelName string) []model.ChannelProtocolProbe {
	definitions := []struct {
		bit      model.Protocol
		sendFunc probeSendFunc
	}{
		{model.ProtocolOpenAIChatCompletion, probeOpenAIChatCompletion},
		{model.ProtocolOpenAIResponse, probeOpenAIResponse},
		{model.ProtocolAnthropicMessage, probeAnthropicMessage},
	}
	probes := make([]model.ChannelProtocolProbe, 0, len(definitions))
	for _, definition := range definitions {
		status, body, err := definition.sendFunc(httpClient, ctx, target, key, modelName)
		probe := model.ChannelProtocolProbe{Protocol: definition.bit, Status: status}
		if err != nil {
			probe.Error = summarizeProbeBody(body, err)
		} else {
			probe.OK = true
		}
		probes = append(probes, probe)
	}
	return probes
}

// probeSendFunc 发送一种协议的最小实测请求, 返回上游状态码, 截断正文与错误。
// err 非 nil 即该协议对模型不可用; 正文仅在 err 非 nil 时有值。
type probeSendFunc func(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, modelName string) (int, []byte, error)

// probeEndpointPath 为探测补协议路径默认值: 探测收的是未落库的表单配置, 路径可能被清空,
// 缺省回退到与保存时一致的默认路径, 无前导斜杠则补齐, 避免拼出错误地址导致协议误判为不支持。
func probeEndpointPath(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return value
}

// probeRequest 构造并执行一次实测请求, 校验状态码并读回正文摘要。
// 判定口径: 非 2xx 一律失败; 2xx 还要求正文以 { 或 [ 开头(取前若干字节判断),
// 部分网关对未知路径也回 200 + HTML, 只看状态码会误判为支持; 最小响应常超 256 字节, 故判首字符而不全量解 JSON。
func probeRequest(httpClient *http.Client, ctx context.Context, request *http.Request) (int, []byte, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeModelTimeout)
	defer cancel()
	response, err := httpClient.Do(request.WithContext(probeCtx))
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, probeModelBodyLimit))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return response.StatusCode, body, fmt.Errorf("upstream %s", response.Status)
	}
	head := strings.TrimLeftFunc(string(body), unicode.IsSpace)
	if !strings.HasPrefix(head, "{") && !strings.HasPrefix(head, "[") {
		return response.StatusCode, body, fmt.Errorf("upstream %s: non-json body", response.Status)
	}
	return response.StatusCode, body, nil
}

// probeOpenAIChatCompletion 按对话补全协议发一次 1 token 的最小请求。
func probeOpenAIChatCompletion(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, modelName string) (int, []byte, error) {
	payload, _ := json.Marshal(map[string]any{
		"model":      modelName,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		model.JoinUpstreamURL(target.BaseURL, probeEndpointPath(target.OpenAIChatCompletionPath, "/v1/chat/completions")), bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	for _, header := range target.CustomHeader {
		if header.HeaderKey != "" {
			request.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}
	return probeRequest(httpClient, ctx, request)
}

// probeOpenAIResponse 按 Responses 协议发一次最小请求。
// max_output_tokens 取 16: OpenAI 规定该值下限为 16, 传更小会被上游以参数错误拒绝而误判为不支持。
func probeOpenAIResponse(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, modelName string) (int, []byte, error) {
	payload, _ := json.Marshal(map[string]any{
		"model":             modelName,
		"input":             "ping",
		"max_output_tokens": 16,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		model.JoinUpstreamURL(target.BaseURL, probeEndpointPath(target.OpenAIResponsePath, "/v1/responses")), bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	for _, header := range target.CustomHeader {
		if header.HeaderKey != "" {
			request.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}
	return probeRequest(httpClient, ctx, request)
}

// probeAnthropicMessage 按 Messages 协议发一次 1 token 的最小请求。
func probeAnthropicMessage(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, modelName string) (int, []byte, error) {
	payload, _ := json.Marshal(map[string]any{
		"model":      modelName,
		"messages":   []map[string]string{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		model.JoinUpstreamURL(target.BaseURL, probeEndpointPath(target.AnthropicMessagePath, "/v1/messages")), bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Api-Key", key)
	request.Header.Set("Anthropic-Version", "2023-06-01")
	for _, header := range target.CustomHeader {
		if header.HeaderKey != "" {
			request.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}
	return probeRequest(httpClient, ctx, request)
}

// summarizeProbeBody 拼一行失败摘要: 错误原因必带, 上游正文截断附后, 两者都方便界面直接定位。
func summarizeProbeBody(body []byte, err error) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return err.Error()
	}
	text = strings.Join(strings.Fields(text), " ")
	if len(text) > probeModelBodyLimit {
		text = text[:probeModelBodyLimit]
	}
	return err.Error() + ": " + text
}

// modelsURL 取协议请求路径的父级目录, 与地址拼成同级的 /models 地址。
// 例如 /v1/chat/completions 与 /v1/messages 都得到 /v1/models, /chat/completions 得到 /models。
func modelsURL(baseURL, protocolPath string) string {
	parent := path.Dir(strings.TrimRight(protocolPath, "/"))
	// Anthropic 的 /v1/messages 只有一层, 父级即 /v1; Chat 的 /v1/chat/completions 需要再上一层。
	if strings.HasSuffix(parent, "/chat") {
		parent = path.Dir(parent)
	}
	if parent == "." || parent == "/" {
		parent = ""
	}
	return model.JoinUpstreamURL(baseURL, parent+"/models")
}

// refer: https://platform.openai.com/docs/api-reference/models/list
func fetchOpenAIModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, url string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	for _, header := range target.CustomHeader {
		if header.HeaderKey != "" {
			req.Header.Set(header.HeaderKey, header.HeaderValue)
		}
	}

	response, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	result, err := decodeModelList[model.OpenAIModelList](response)
	if err != nil {
		return nil, err
	}

	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, m.ID)
	}
	return models, nil
}

// refer: https://platform.claude.com/docs
func fetchAnthropicModels(httpClient *http.Client, ctx context.Context, target model.ChannelConfig, key, url string) ([]string, error) {
	var allModels []string
	var afterID string
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Api-Key", key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
		for _, header := range target.CustomHeader {
			if header.HeaderKey != "" {
				req.Header.Set(header.HeaderKey, header.HeaderValue)
			}
		}
		if afterID != "" {
			q := req.URL.Query()
			q.Set("after_id", afterID)
			req.URL.RawQuery = q.Encode()
		}

		response, err := httpClient.Do(req)
		if err != nil {
			return nil, err
		}
		// 分页时每轮都会新建响应, 必须当轮读完即关; 用 defer 会攒到整个函数返回才释放。
		result, err := decodeModelList[model.AnthropicModelList](response)
		if err != nil {
			return nil, err
		}
		for _, m := range result.Data {
			allModels = append(allModels, m.ID)
		}
		if !result.HasMore {
			break
		}
		afterID = result.LastID
	}
	return allModels, nil
}

// decodeModelList 关闭响应并把响应体解成模型列表; 非 2xx 时按上游错误返回。
// 两侧解析流程一致, 只有目标结构不同, 故用类型参数收敛; 分页调用要求当轮读完即关, 关闭点放在此处最稳。
func decodeModelList[T any](response *http.Response) (T, error) {
	defer response.Body.Close()
	var result T
	// 上游报错时响应体常是能被正常解码的 JSON, 若不先拦下, 模型列表会解成空列表并当作成功;
	// 响应体截断到 512 字节: 部分上游在鉴权失败时返回整页 HTML, 全文带到界面上无用。
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		body, err := io.ReadAll(io.LimitReader(response.Body, 512))
		if err != nil {
			return result, fmt.Errorf("upstream %s", response.Status)
		}
		return result, fmt.Errorf("upstream %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		return result, err
	}
	return result, nil
}
