package handlers

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// T-verify-004 渠道模型的批量可用性实测。
//
// ## 为什么需要"实测"而不是"看清单"
//
// T-usability-006 的教训：上游 /v1/models 的清单**不完整** ——
// 实测发现「可茶/MiniMax-M3」不在清单里但实际调用返回 200。
// 所以清单只能给出下界，回答不了「这个模型到底能不能用」。
//
// 唯一可靠的判据是**发一次真实请求**。
//
// ## 成本与克制的设计
//
// 这是个会真打上游的操作（每个模型一次请求），所以：
//   - **手动触发**，不挂任何自动流程；
//   - 并发固定 3：再高容易被风控，而这是诊断工具，慢一点没关系；
//   - 每个请求 max_tokens=1：只为验证"上游收不收"，不需要真吐内容；
//   - 单请求超时 30 秒，整体上限由模型数 × 并发决定；
//   - 只报事实（可用/不可用/错误原因），**不自动改任何配置**。
type modelVerifyResult struct {
	Model string `json:"model"`
	// Usable 为真表示上游接受了这次请求（HTTP 2xx）。
	Usable bool `json:"usable"`
	// Status 是上游 HTTP 状态码（0 表示请求没发出去/网络失败）。
	Status int `json:"status"`
	// Error 是失败原因原文（成功时为空）。
	Error string `json:"error,omitempty"`
	// LatencyMs 是这次实测的耗时，供用户判断"能用但很慢"这类情况。
	LatencyMs int64 `json:"latency_ms"`
}

func verifyChannelModels(c *gin.Context) {
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

	// 只实测**有凭据授权**的模型：没有授权就没有可用的凭据，
	// 实测只会得到 401，那是配置问题不是模型问题 —— 报出来会误导。
	authorized := make(map[string]string, len(detail.Grants))
	for _, g := range detail.Grants {
		authorized[g.ModelName] = g.KeyName
	}
	keyByName := make(map[string]string, len(detail.Keys))
	for _, k := range detail.Keys {
		if k.Enabled == nil || *k.Enabled {
			keyByName[k.Name] = k.Key
		}
	}

	targets := make([]string, 0, len(authorized))
	for name := range authorized {
		if _, ok := keyByName[authorized[name]]; ok {
			targets = append(targets, name)
		}
	}
	sort.Strings(targets)

	// 上限保护：模型多时不该让一次请求打上游几百下。
	const maxTargets = 60
	truncated := false
	if len(targets) > maxTargets {
		targets = targets[:maxTargets]
		truncated = true
	}

	if len(targets) == 0 {
		resp.Success(c, gin.H{
			"channel_id":   id,
			"channel_name": detail.Name,
			"results":      []modelVerifyResult{},
			"note":         "该渠道没有「已启用凭据 + 已授权」的模型组合，无可实测对象",
		})
		return
	}

	target := model.ChannelConfig{
		Name:               detail.Name,
		BaseURL:            detail.BaseURL,
		Proxy:              detail.Proxy,
		ChannelProxy:       detail.ChannelProxy,
		OpenAIResponsePath: detail.OpenAIResponsePath,
	}
	var httpClient *http.Client
	switch {
	case !target.Proxy:
		httpClient, err = rhttp.Direct()
	case target.ChannelProxy == "":
		httpClient, err = rhttp.Proxy()
	default:
		httpClient, err = rhttp.New(target.ChannelProxy)
		if httpClient != nil {
			defer httpClient.CloseIdleConnections()
		}
	}
	if err != nil {
		resp.Error(c, http.StatusBadGateway, err.Error())
		return
	}

	// 并发固定 3：再高容易被风控，而这是诊断工具，慢一点可以接受。
	const concurrency = 3
	results := make([]modelVerifyResult, len(targets))
	var wg sync.WaitGroup
	slots := make(chan struct{}, concurrency)
	ctx := c.Request.Context()

	for i, name := range targets {
		wg.Add(1)
		go func(idx int, modelName string) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[idx] = verifyOneModel(ctx, httpClient, target, keyByName[authorized[modelName]], modelName)
		}(i, name)
	}
	wg.Wait()

	var usable int
	for _, r := range results {
		if r.Usable {
			usable++
		}
	}

	resp.Success(c, gin.H{
		"channel_id":   id,
		"channel_name": detail.Name,
		"total":        len(results),
		"usable":       usable,
		"unusable":     len(results) - usable,
		"truncated":    truncated,
		"max_targets":  maxTargets,
		"results":      results,
		"concurrency":  concurrency,
	})
}

// verifyOneModel 发一次最小请求，判断上游收不收这个模型。
//
// max_tokens=1 而不是真跑一轮对话：这里只验证「上游认不认这个名字」，
// 不需要它产出内容 —— 既省上游的额度，也让实测快得多。
func verifyOneModel(
	ctx context.Context,
	httpClient *http.Client,
	target model.ChannelConfig,
	key string,
	modelName string,
) modelVerifyResult {
	if key == "" {
		return modelVerifyResult{Model: modelName, Error: "没有可用的凭据"}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	body := fmt.Sprintf(
		`{"model":%q,"messages":[{"role":"user","content":"hi"}],"max_tokens":1}`,
		modelName,
	)
	url := model.JoinUpstreamURL(target.BaseURL, "/v1/chat/completions")
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return modelVerifyResult{Model: modelName, Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	start := time.Now()
	res, err := httpClient.Do(req)
	elapsed := time.Since(start).Milliseconds()
	if err != nil {
		return modelVerifyResult{Model: modelName, Error: err.Error(), LatencyMs: elapsed}
	}
	defer res.Body.Close()
	// 读掉响应体以复用连接；只需头部信息做判断。
	_, _ = res.Body.Read(make([]byte, 4096))

	result := modelVerifyResult{
		Model:     modelName,
		Status:    res.StatusCode,
		Usable:    res.StatusCode >= 200 && res.StatusCode < 300,
		LatencyMs: elapsed,
	}
	if !result.Usable {
		// 只给状态码与简短分类，不回显上游原文 —— 它可能很长，
		// 而用户需要的是"能不能用"，细节可以去日志页看。
		result.Error = fmt.Sprintf("上游返回 HTTP %d", res.StatusCode)
	}
	return result
}

func init() {
	router.NewGroupRouter("/api/v1/channel").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/:id/verify-models", http.MethodPost).
				Handle(verifyChannelModels),
		)
}
