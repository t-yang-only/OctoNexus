package relay

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// T-verify-002 TypeSafe AI「System One」评估接口的兼容转发。
//
// ## 为什么需要一个专门的转发器
//
// TypeSafe 的 Jev 不是聊天模型，它的接口形态与三家标准协议都不同：
//
//	POST /v1/systemone
//	{"model":"jev-latest","state":<要评估的内容>,"questions":{"q1":{"type":"noul","instructions":"..."}}}
//	→ {"model":"jev-1.13.0","answers":{"q1":{"type":"noul","noul":0.99}},"usage":{...}}
//
// 请求体是 state + typed questions，响应是结构化答案（choice/score/noul），
// 全程没有 messages/choices 这些概念。因此它**过不了协议转换层**：
// 塞进 OpenAI 协议会被当成非法请求，反过来也一样。
//
// 做法是"复用选路、绕开转换"：分组、成员、冷却、重试这些决定"走哪个渠道"的能力
// 与协议无关，照常复用；只有 body 的编解码不走 transformer，原样透传。
//
// ## 请求里的 model 填什么
//
// 填**分组名**（与其他协议一致，如 `TypeSafe/jev-latest`）。
// 转发时把它改写成渠道下的真实模型名（`jev-latest`）—— 与既有链路同口径：
// 客户端只认分组名，上游只认模型名，两者之间的翻译由网关完成。
func ForwardSystemOne() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, err := readInboundRequest(c.Request)
		if err != nil {
			rejectJSON(c, http.StatusBadRequest, err.Error())
			return
		}

		requested := strings.TrimSpace(gjson.GetBytes(raw.Body, "model").String())
		if requested == "" {
			rejectJSON(c, http.StatusBadRequest, "model is required")
			return
		}

		// API Key 限定模型范围时同样要放行检查（与其它协议一致）。
		if allowed, ok := c.Get("supported_models"); ok {
			if names, _ := allowed.([]string); len(names) > 0 && !containsString(names, requested) {
				rejectJSON(c, http.StatusBadRequest, "model not supported by this api key")
				return
			}
		}

		group, err := op.GroupGetByName(requested)
		if err != nil {
			// 与其它协议同款：先按原名找，找不到再过一层重写规则。
			rewritten, matched := op.ModelMappingResolveByName(requested)
			if !matched {
				rejectJSON(c, http.StatusBadRequest, modelNotFoundError(requested).Error())
				return
			}
			group, err = op.GroupGetByName(rewritten)
			if err != nil {
				rejectJSON(c, http.StatusBadRequest, modelNotFoundError(requested).Error())
				return
			}
		}

		flat := op.FlattenGroupItems(group)
		if len(flat) == 0 {
			rejectJSON(c, http.StatusBadRequest, "group has no member")
			return
		}
		item := PickGroupItem(group, flat)
		if item.ID == 0 {
			rejectJSON(c, http.StatusServiceUnavailable, "no available member (all cooling or disabled)")
			return
		}

		grant, err := op.ChannelGrantGet(item.GrantRef())
		if err != nil {
			rejectJSON(c, http.StatusBadGateway, "grant unavailable: "+err.Error())
			return
		}
		if grant.ChannelKey == nil {
			rejectJSON(c, http.StatusBadGateway, "member has no credential")
			return
		}
		channel, err := op.ChannelGet(grant.ChannelModel.ChannelID)
		if err != nil {
			rejectJSON(c, http.StatusBadGateway, "channel unavailable: "+err.Error())
			return
		}

		// 把 model 改写成渠道下的真实模型名；其余字段原样保留（state/questions 是上游的契约）。
		body, err := sjson.SetBytes(raw.Body, "model", grant.ChannelModel.Name)
		if err != nil {
			rejectJSON(c, http.StatusBadRequest, "rewrite model: "+err.Error())
			return
		}

		target := systemOneURL(channel.BaseURL)
		started := time.Now()
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			rejectJSON(c, http.StatusBadGateway, err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+grant.ChannelKey.Key)
		for _, h := range channel.CustomHeader {
			req.Header.Set(h.HeaderKey, h.HeaderValue)
		}

		client := &http.Client{Timeout: 5 * time.Minute}
		resp, err := client.Do(req)
		if err != nil {
			recordSystemOneLog(c, requested, channel.Name, grant.ChannelModel.Name, started, 0, 0, err.Error())
			rejectJSON(c, http.StatusBadGateway, "upstream request failed: "+err.Error())
			return
		}
		defer resp.Body.Close()
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 64*1024*1024))
		if readErr != nil {
			recordSystemOneLog(c, requested, channel.Name, grant.ChannelModel.Name, started, 0, 0, readErr.Error())
			rejectJSON(c, http.StatusBadGateway, "read upstream response: "+readErr.Error())
			return
		}

		// 用量口径与上游一致（input_tokens/output_tokens），与 OpenAI 的 prompt/completion 不同名。
		inTokens := gjson.GetBytes(respBody, "usage.input_tokens").Int()
		outTokens := gjson.GetBytes(respBody, "usage.output_tokens").Int()

		if resp.StatusCode >= 400 {
			// 上游拒绝：把原文透传给客户端（它带明确的错误类型，比网关改写过的更有用），
			// 同时记进日志 —— 否则"为什么失败"只能靠抓包。
			recordSystemOneLog(c, requested, channel.Name, grant.ChannelModel.Name, started,
				inTokens, outTokens, string(respBody))
			c.Data(resp.StatusCode, "application/json", respBody)
			return
		}

		recordSystemOneLog(c, requested, channel.Name, grant.ChannelModel.Name, started, inTokens, outTokens, "")
		c.Data(resp.StatusCode, "application/json", respBody)
	}
}

// systemOneURL 拼上游的 systemone 端点。
//
// base_url 归一化：用户可能填 https://api.typesafe.ai 或 https://api.typesafe.ai/v1，
// 两种都要拼成同一个地址。少一次归一化就会出现 /v1/v1/systemone 这种 404，
// 而错误信息只会说 "Not Found"，排查起来要绕一圈。
func systemOneURL(base string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/systemone"
	}
	return base + "/v1/systemone"
}

// recordSystemOneLog 把这次评估落进请求日志，让它在面板的日志页里与其它请求一样可见。
//
// 不记的话，System One 的调用在面板上完全不存在 —— 用户会以为请求没发出去。
func recordSystemOneLog(c *gin.Context, model_, channel, targetModel string, started time.Time,
	inTokens, outTokens int64, errText string) {
	status := "success"
	if errText != "" {
		status = "failed"
	}
	apiKeyName := ""
	if v, ok := c.Get("api_key_name"); ok {
		if s, _ := v.(string); s != "" {
			apiKeyName = s
		}
	}
	op.RelayLogSave(model.RelayLog{
		Status:         status,
		Model:          model_,
		APIKeyName:     apiKeyName,
		TargetChannel:  channel,
		TargetModel:    targetModel,
		StartedAt:      started,
		FirstByteMs:    -1, // 非流式：首字节与总耗时同义，这里只记总耗时
		DurationMs:     time.Since(started).Milliseconds(),
		Attempts:       1,
		Decision:       "mode=systemone;reason=direct",
		PromptTokens:   inTokens,
		CompletionToks: outTokens,
		Error:          truncateText(errText, 500),
	})
}

// truncateText 截断过长文本（错误原文可能是一大段上游响应）。
func truncateText(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// rejectJSON 按统一形状回错误，与其它协议的错误体保持可读性一致。
func rejectJSON(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, gin.H{"error": gin.H{
		"message": message,
		"type":    "invalid_request_error",
	}})
}

// containsString 小工具：判断切片是否含某值。
func containsString(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
