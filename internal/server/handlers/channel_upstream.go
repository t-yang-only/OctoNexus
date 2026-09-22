package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/bestruirui/octopus/internal/server/middleware"
	"github.com/bestruirui/octopus/internal/server/resp"
	"github.com/bestruirui/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

// T-usability-006 渠道「配置 vs 上游」对比。
//
// ## 解决什么问题
//
// 渠道可用性诊断（T-usability-001）看的是**配置层**：模型有没有、有没有凭据授权、
// 有没有生成分组。但这些都是"我们自己这边的账"，它回答不了另一个问题：
//
//	**客户端列表里那些模型，上游到底有没有？**
//
// 实测（2026-09-23）：pipixia 渠道配了 33 个模型，而上游 /v1/models 只列出 4 个 ——
// 那 29 个是**永远不可能调用成功**的配置。它们不会报错、不会告警，
// 只是安静地待在模型列表里，等着用户选中然后失败。
//
// 更麻烦的是它会让统计数字说谎：诊断说"这个渠道可用 6/33"，
// 用户以为补上授权就能用 33 个，实际补了也只有 4 个能用。
//
// ## 与 fetchModel 的区别
//
// `fetchModel` 探测的是**表单里尚未落库的配置**（渠道编辑时点「获取模型」），
// 结果只用于回填表单。本接口探测的是**已落库的渠道**，并把结果与配置对比 ——
// 目的是发现"配置里写了但上游没有"的无效条目。
//
// 复用同一套探测原语（fetchOpenAIModels / rhttp），不另造一套。
func checkChannelUpstream(c *gin.Context) {
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
	configured := make([]string, 0, len(detail.Models))
	for _, name := range detail.Models {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			configured = append(configured, trimmed)
		}
	}
	if len(configured) == 0 {
		resp.Success(c, gin.H{
			"channel_id":       id,
			"channel_name":     detail.Name,
			"configured_count": 0,
			"note":             "该渠道没有配置任何模型，无需对比",
		})
		return
	}

	// 取一把启用的凭据：上游清单通常与用哪把钥匙无关，但鉴权失败会直接暴露成 403，
	// 所以必须有钥匙才能探。
	key := ""
	for _, k := range detail.Keys {
		// Enabled 是 *bool：nil 表示提交方没带这个字段，按项目口径视为启用。
		if k.Enabled == nil || *k.Enabled {
			key = k.Key
			break
		}
	}
	if key == "" {
		resp.Success(c, gin.H{
			"channel_id":       id,
			"channel_name":     detail.Name,
			"configured_count": len(configured),
			"note":             "该渠道没有启用的凭据，无法探测上游（请先启用或补一把凭据）",
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

	accept := model.EndpointPathOrDefault(target.OpenAIResponsePath, "/v1/responses")
	upstream, probeErr := fetchOpenAIModels(
		httpClient, c.Request.Context(), target, key, modelsURL(target.BaseURL, accept))

	// 探测失败时如实返回原因，而不是把「探不到」当成「上游没有」——
	// 后者会让用户误以为配置全是错的，进而删掉本来有效的模型。
	// 实测：openagents 的 key 返回 403，属于凭据问题而非配置问题。
	if probeErr != nil {
		resp.Success(c, gin.H{
			"channel_id":       id,
			"channel_name":     detail.Name,
			"configured_count": len(configured),
			"probe_ok":         false,
			"probe_error":      probeErr.Error(),
			"note": "探测上游失败，无法判断配置是否有效。这通常是凭据或地址问题，" +
				"不是配置写错了 —— 请先解决探测失败的原因。",
		})
		return
	}

	// 对比逻辑走纯函数（可见 diffUpstreamModels 的注释：它的失败模式是误报）。
	diff := diffUpstreamModels(configured, upstream)
	sort.Strings(upstream)

	resp.Success(c, gin.H{
		"channel_id":       id,
		"channel_name":     detail.Name,
		"probe_ok":         true,
		"configured_count": countNonBlank(configured),
		"upstream_count":   len(upstream),
		"effective_count":  diff.EffectiveCount,
		"upstream_models":  upstream,
		"missing_upstream": diff.MissingUpstream,
		"not_configured":   diff.NotConfigured,
	})
}

// upstreamDiff 是「配置 vs 上游」的对比结果。
type upstreamDiff struct {
	// MissingUpstream 是配置里写了但上游没有的模型 —— 这些永远不可能调用成功。
	MissingUpstream []string
	// NotConfigured 是上游有但配置里没写的模型 —— 可以补进来。
	NotConfigured []string
	// EffectiveCount 是配置里真正可能可用的数量（总配置数 − 上游没有的）。
	EffectiveCount int
}

// diffUpstreamModels 对比「配置里写的模型」与「上游实际有的模型」。
//
// 单独抽成纯函数是为了它能被直接断言：这段逻辑的失败模式是**误报**
// （把有效的模型报成"上游没有"），而误报会让用户删掉本来能用的配置 ——
// 比漏报危险得多。端到端测试要真打上游，覆盖不到大小写、空白这些边界。
//
// 比对口径：忽略大小写与首尾空白。同一个模型在不同站点的写法常有出入
// （`GLM-5` vs `glm-5`），按字面比对会把它们全报成缺失。
func diffUpstreamModels(configured, upstream []string) upstreamDiff {
	normalize := func(s string) string {
		return strings.ToLower(strings.TrimSpace(s))
	}

	upstreamSet := make(map[string]struct{}, len(upstream))
	for _, name := range upstream {
		if key := normalize(name); key != "" {
			upstreamSet[key] = struct{}{}
		}
	}
	configuredSet := make(map[string]struct{}, len(configured))
	for _, name := range configured {
		if key := normalize(name); key != "" {
			configuredSet[key] = struct{}{}
		}
	}

	result := upstreamDiff{
		MissingUpstream: make([]string, 0),
		NotConfigured:   make([]string, 0),
	}
	for _, name := range configured {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if _, ok := upstreamSet[normalize(name)]; !ok {
			result.MissingUpstream = append(result.MissingUpstream, name)
		}
	}
	for _, name := range upstream {
		if strings.TrimSpace(name) == "" {
			continue
		}
		if _, ok := configuredSet[normalize(name)]; !ok {
			result.NotConfigured = append(result.NotConfigured, name)
		}
	}
	sort.Strings(result.MissingUpstream)
	sort.Strings(result.NotConfigured)

	result.EffectiveCount = countNonBlank(configured) - len(result.MissingUpstream)
	if result.EffectiveCount < 0 {
		result.EffectiveCount = 0
	}
	return result
}

func countNonBlank(names []string) int {
	count := 0
	for _, name := range names {
		if strings.TrimSpace(name) != "" {
			count++
		}
	}
	return count
}

func init() {
	// 探测是只读动作但会打向上游，按 GET 暴露便于面板直接触发；
	// 它不修改任何状态，故不需要 RequireJSON。
	router.NewGroupRouter("/api/v1/channel").
		ServeOn(router.ServerAdmin).
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/:id/upstream-check", http.MethodGet).
				Handle(checkChannelUpstream),
		)
}
