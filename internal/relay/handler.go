package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/gin-contrib/sse"
	"github.com/gin-gonic/gin"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/looplj/axonhub/llm/transformer/anthropic"
	"github.com/looplj/axonhub/llm/transformer/openai"
	"github.com/looplj/axonhub/llm/transformer/openai/responses"
)

// errStreamIdleTimeout 表示流式响应在首个事件之后长时间没有任何进展（上游不发、也不结束）。
// 首字节已提交, 无法换目标重试: 只结束本次响应, 并按成员真实失败记账（T-timeout-001）。
var errStreamIdleTimeout = errors.New("upstream stream idle timeout")

// Forward 按客户端协议承载一个请求的完整转发过程: 解析请求, 定位分组, 循环选目标请求上游, 直至提交响应或请求结束。
func Forward(format llm.APIFormat) gin.HandlerFunc {
	// 客户端协议同时定出入站转换器和请求协议位: 后者随请求状态推给界面, 也是每轮选择上游协议的首选。
	var inbound transformer.Inbound
	requestProtocol := model.ProtocolOpenAIChatCompletion
	switch format {
	case llm.APIFormatOpenAIResponse:
		inbound = responses.NewInboundTransformer()
		requestProtocol = model.ProtocolOpenAIResponse
	case llm.APIFormatAnthropicMessage:
		inbound = anthropic.NewInboundTransformer()
		requestProtocol = model.ProtocolAnthropicMessage
	default:
		inbound = openai.NewInboundTransformer()
	}

	return func(c *gin.Context) {
		// 完整读取客户端请求, 正文先登记到请求状态, 后续每轮直接改写为当前目标请求。
		raw, err := httpclient.ReadHTTPRequest(c.Request)
		if err != nil {
			rejectRequest(c, inbound, err)
			return
		}

		// 此处只读取选组和分流所需字段; 完整协议校验由同协议上游或跨协议 pipeline 完成。
		var metadata struct {
			Model     string `json:"model"`  // 客户端请求的分组名称。
			Streaming bool   `json:"stream"` // 客户端是否请求流式响应。
		}
		if err := json.Unmarshal(raw.Body, &metadata); err != nil {
			rejectRequest(c, inbound, err)
			return
		}

		// API Key 限定了模型范围时只放行范围内的模型, 为空表示不限制。
		if allowed, ok := c.Get("supported_models"); ok {
			if names, _ := allowed.([]string); len(names) > 0 && !slices.Contains(names, metadata.Model) {
				rejectRequest(c, inbound, errors.New("model not supported by this api key"))
				return
			}
		}

		// 客户端请求的模型名称即分组名称; 分组不存在说明模型名错误, 等待也不会出现该分组。
		// 分组主键随请求状态一并登记, 界面由此可直接按主键取分组而不必按名称回查。
		group, err := op.GroupGetByName(metadata.Model)
		if err != nil {
			rejectRequest(c, inbound, errors.New("model not found"))
			return
		}

		// 登记进程内请求状态, 返回的记录是后续全部状态写入和前端可视化推送的入口。
		request := newRequestState(c.Request.Context(), metadata.Model, group.ID, requestProtocol, string(raw.Body), c.GetInt("api_key_id"))
		// Key 级 TPM 记账: 终态时把实际词元量交给鉴权层注册的回调 (未限流 key 回调缺省, 跳过)。
		AttachUsageRecorder(c.Request.Context(), request.ID, func(promptTokens, completionTokens int64) {
			if recorderAny, ok := c.Get("key_usage_recorder"); ok {
				if recorder, ok := recorderAny.(func(int64)); ok {
					recorder(promptTokens + completionTokens)
				}
			}
		})
		ctx := c.Request.Context()
		failedItemID := 0 // 当前累计连续失败次数的成员 ID。
		failures := 0     // 该成员包含首次请求的连续失败次数。
		attempts := 0     // 本请求已经打向上游的尝试次数（用于单请求尝试上限）。
		var lastErr error // 最近一次上游失败原因（上限触发时回给客户端）。
		// rejectedItems 是本请求内被上游判定为"请求本身非法"而拒绝过的成员: 同一份请求再发给它只会被同样拒绝。
		rejectedItems := map[int]bool{}
		// 智能路由（GroupModeSmart）的请求特征：只看客户端原始正文，与目标协议无关，
		// 因此整轮循环里算一次即可（同一份请求无论换到哪个成员，复杂度判定都不该变）。
		smartFeatures := SmartScoreBody(raw.Body)

		for {
			if ctx.Err() != nil {
				request.markCanceled(ctx.Err(), "", nil)
				return
			}

			// 分组配置和成员随时可改, 故每轮重新读取; 分组被删除时等待它重新出现。
			group, err = op.GroupGetByName(metadata.Model)
			if err != nil {
				if !request.wait(ctx, model.DefaultGroupRelayConfig().MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			// 手动模式取人工指定的成员, 故障转移模式按优先级选择未禁用且不在冷却中的成员。
			// 没有目标时等待重新选择, 期间人工切换渠道, 补齐成员或成员冷却到期即可让请求继续。
			// 子分组按树形递归解析: 亲和/冷却/上限等路由状态按顶层分组持有,
			// 冷却与亲和的键是展平后具体成员行 ID (跨树唯一)。
			// 同包内直接调用, 不经导出外壳 (外壳专供单测与外部消费)。
			// 加权轮询 flag (T-route-002) 默认关: 关时行为与原路径完全一致;
			// 开时仅改 failover 候选定序, 冷却/探测/亲和仍归顶层 RouteState。
			// 已被上游以"请求本身非法"拒绝的成员在本请求内不再重复尝试（其余成员正常参与选路）。
			items := dropRejectedMembers(op.FlattenGroupItems(group), rejectedItems)
			item := pickGroupItemHotWithFeatures(group.WithItems(items), smartFeatures)
			if item.ID == 0 {
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}

			// 成员指向的授权缺失、凭据被停用或已被删除时等待: 该成员可能很快被改回或恢复可用。
			// 解析与出站准备统一走 prepareRoundTarget: 首字竞速要并发多路, 每路必须有独立的请求副本,
			// 否则 applyChannelConfig 改写 Body/Headers 会在并发下数据竞争。
			primary, prepErr := prepareRoundTarget(item, raw, metadata.Streaming, format, requestProtocol)
			if prepErr != nil {
				if !request.wait(ctx, group.RelayConfig.MemberRetryIntervalSeconds) {
					return
				}
				continue
			}
			channelModel := primary.channelModel
			channelKey := primary.channelKey
			channel := primary.channel
			targetProtocol := primary.targetProtocol
			passthrough := primary.passthrough
			outbound := primary.outbound
			roundRaw := primary.raw
			err = nil

			// 为本轮上游调用建立独立取消入口并登记当前目标; 取消原因用于区分人工中止与响应超时。
			// 每一轮独立上下文: 人工中止 / 客户端取消 / 超时都通过它传播;
			// 首字竞速的多路尝试都挂在它下面, 人工中止时一并取消。
			roundCtx, cancelRoundCause := context.WithCancelCause(ctx)
			// 人工中止和本地取消都使用普通 canceled 原因, 此时回写请求状态为超时会造成误解。
			cancelRound := func() {
				cancelRoundCause(context.Canceled)
			}
			request.startRound(cancelRound, item.ID, channel.Name, channelModel.Name, targetProtocol)

			roundStartedAt := time.Now() // 本轮调用的开始时间, 用于统计首个有效响应耗时

			// 首字竞速 (T-hedge-001): 触发条件满足时并发请求排序靠前的多个成员, 取最快给出有效响应者。
			// 两个触发条件: 高峰期(分组在途数达阈值, 立刻并发) 与 慢启动(首选超过阈值毫秒仍无响应, 再并发)。
			settings := hedgeSettingsOf(group.RelayConfig)
			var hedgeTargets []preparedTarget
			hedgeImmediately := false
			if settings.enabled && settings.width > 1 {
				if settings.peak > 0 && groupInFlight(metadata.Model) >= settings.peak {
					hedgeImmediately = true
				}
				candidates := hotRouteDeps().rankedHedgeCandidates(group.WithItems(op.FlattenGroupItems(group)))
				for _, candidate := range candidates {
					if len(hedgeTargets) >= settings.width-1 {
						break
					}
					if candidate.ID == item.ID {
						continue
					}
					target, targetErr := prepareRoundTarget(candidate, raw, metadata.Streaming, format, requestProtocol)
					if targetErr != nil {
						continue
					}
					hedgeTargets = append(hedgeTargets, target)
				}
			}

			var result *upstreamResponse
			if len(hedgeTargets) == 0 {
				// 未开启竞速或候选不足: 与既有行为完全一致（单路, 超时即切下一个成员）。
				timeoutSeconds := group.RelayConfig.MemberNonStreamResponseTimeoutSeconds
				timeoutErr := errors.New("upstream non-stream response timeout")
				if metadata.Streaming {
					timeoutSeconds = group.RelayConfig.MemberStreamFirstEventTimeoutSeconds
					timeoutErr = errors.New("upstream stream first event timeout")
				}
				// 超时只取消本轮的上游调用, 不会在等待 HTTP 响应或首个流事件的调用间超时重叠。
				timeoutTimer := time.AfterFunc(time.Duration(timeoutSeconds)*time.Second, func() {
					cancelRoundCause(timeoutErr)
				})
				if passthrough {
					result, err = sendPassthrough(roundCtx, format, roundRaw, channel, outbound, metadata.Streaming, channelModel.Name)
				} else {
					result, err = sendConverted(roundCtx, format, roundRaw, channel, outbound, metadata.Streaming)
				}
				if !timeoutTimer.Stop() {
					cancelRoundCause(timeoutErr)
				}
				if context.Cause(roundCtx) == timeoutErr {
					err = timeoutErr
					if result != nil && result.events != nil {
						result.events.Close()
						if result.closeIdle != nil {
							result.closeIdle()
						}
					}
				}
			} else {
				winner, raceResult, raceErr, losers := runRoundWithHedge(roundCtx, group, primary, hedgeTargets,
					metadata.Streaming, format, hedgeImmediately, settings.afterMs)
				if raceErr == nil {
					logHedge(group, primary, winner, losers, time.Since(roundStartedAt))
					// 胜出者可能不是首选: 把本轮目标换成胜出者, 后续统计 / 亲和 / 日志都按它记账。
					// 落选者是被我们自己取消的, 不算失败、不进冷却（设计稿 §5）。
					item = winner.item
					channelModel, channelKey, channel = winner.channelModel, winner.channelKey, winner.channel
					targetProtocol, passthrough, outbound = winner.targetProtocol, winner.passthrough, winner.outbound
					request.retargetRound(winner.item.ID, winner.channel.Name, winner.channelModel.Name, winner.targetProtocol)
				}
				result, err = raceResult, raceErr
			}
			if err != nil {
				// 记录本轮上游调用已经结束及其失败原因。
				request.finishRound(err.Error())
				// 父上下文结束说明客户端已经取消, 归还探测占用并以取消终态结束请求。
				if ctx.Err() != nil {
					releaseRouteProbe(group, item.ID)
					request.markCanceled(ctx.Err(), "", nil)
					return
				}
				// 仅人工中止本轮时不计失败也不等待; 响应超时属于真实失败并消耗尝试次数。
				if context.Cause(roundCtx) == context.Canceled {
					releaseRouteProbe(group, item.ID)
					continue
				}
				cancelRound()
				// 单请求尝试次数上限: 成员持续不可用时不再无限重试（上游 issue #388/#338),
				// 给客户端一个明确的失败响应, 而不是让它自己超时、后台还在每秒打上游。
				// 计数放在处置分支之前, 覆盖所有失败类别（含"请求本身非法"的快速失败）,
				// 嵌套分组等异常情形也不会绕过上限。
				attempts++
				lastErr = err
				if attempts >= attemptCap(len(op.FlattenGroupItems(group)), group.RelayConfig.MemberMaxAttempts) {
					failRequest(c, inbound, request, lastErr)
					return
				}
				disposition := classifyUpstreamFailure(err)
				// 请求本身被上游判定为非法（400/413/422 等）: 换成员同样会被拒绝, 因此既不计成员失败
				// （那不是成员的锅, 计了会拉低成员质量）、也不打冷却（否则一个坏请求就能把健康成员冻住),
				// 只把该成员记入本请求的拒绝集合后换下一个; 全部成员都拒绝时以明确错误结束请求。
				if disposition == dispositionRequestFault {
					rejectedItems[item.ID] = true
					if allMembersRejected(op.FlattenGroupItems(group), rejectedItems) {
						failRequest(c, inbound, request, errors.New(requestFaultMessage(err)))
						return
					}
					continue
				}
				// 本轮真实失败只计入当前渠道和成员, 客户端取消与人工中止不计为渠道故障。
				metrics := model.StatsMetrics{WaitTime: time.Since(roundStartedAt).Milliseconds(), RequestFailed: 1}
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)

				// 成员改变时重新开始累计该成员在本请求内的连续失败次数。
				if failedItemID == item.ID {
					failures++
				} else {
					failedItemID = item.ID
					failures = 1
				}
				// 成员自身的问题（401/403/404 等）重试没有意义: 按尝试次数已用尽处理, 立即冷却换人。
				if disposition == dispositionMemberFault && failures < group.RelayConfig.MemberMaxAttempts {
					failures = group.RelayConfig.MemberMaxAttempts
				}
				// 达到总尝试次数时成员进入冷却并立即重新选路, 否则按退避等待后重试。
				if recordRouteFailure(group, item.ID, failures, time.Since(roundStartedAt).Milliseconds()) {
					continue
				}
				// 退避: 同一成员连续失败时把重试间隔逐次翻倍（封顶 30 秒）, 避免每秒一次地打上游。
				if !request.wait(ctx, retryBackoff(group.RelayConfig.MemberRetryIntervalSeconds, failures)) {
					return
				}
				continue
			}
			// 记录本轮已经取得可提交的上游响应。
			request.finishRound("")
			roundWaitTime := time.Since(roundStartedAt).Milliseconds() // 流式响应只统计等待首帧的时间。
			// 上游成功后解除该成员的冷却与探测占用, 并按路由配置开始亲和。
			recordRouteSuccess(group, item.ID, roundWaitTime)
			// 同协议透传时原样返回上游响应头; 跨协议响应没有需要透传的响应头。
			for key, values := range result.header {
				c.Writer.Header()[key] = values
			}

			// 非流式响应已经完整取得, 提交后一次写给客户端。
			if !metadata.Streaming {
				cancelRound()
				if c.Writer.Header().Get("Content-Type") == "" {
					c.Header("Content-Type", "application/json")
				}
				// 非流式响应已有完整用量, 本轮渠道和成员统计可在提交前一次完成。
				metrics := usageMetrics(channelModel.Name, result.usage)
				metrics.WaitTime = roundWaitTime
				metrics.RequestSuccess = 1
				_ = op.ChannelStatsUpdate(channel.ID, metrics)
				_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
				_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
				request.markCommitted()
				n, err := c.Writer.Write(result.body)
				if err == nil && n != len(result.body) {
					err = io.ErrShortWrite
				}
				if err != nil {
					if ctx.Err() != nil {
						request.markCanceled(ctx.Err(), string(result.body), result.usage)
					} else {
						request.markFailed(err, string(result.body), result.usage)
					}
					return
				}
				request.markSucceeded(string(result.body), result.usage)
				return
			}

			// 首帧提交后仍需逐个事件判断协议终态: 上游发出结束事件后未必立即关闭响应体, 继续读取会一直阻塞到
			// 客户端断开, 从而把已完整交付的响应误判为 context canceled。
			if c.Writer.Header().Get("Content-Type") == "" {
				c.Header("Content-Type", "text/event-stream")
			}
			var encoded bytes.Buffer
			var chunks []*httpclient.StreamEvent
			event := result.first
			last := result.last // 已转发的最后一个事件是否已按客户端协议结束整个响应流。
			committed := false
			// 首个事件之后的「无进展」看门狗: 上游吐了首帧就不再出字时, 不能把客户端无限挂着。
			// 每收到一个上游事件、每成功写给客户端一帧都重置计时; 0 表示关闭（保持旧行为）。
			idleSeconds := group.RelayConfig.MemberStreamIdleTimeoutSeconds
			var idleTimer *time.Timer
			if idleSeconds > 0 {
				idleTimer = time.AfterFunc(time.Duration(idleSeconds)*time.Second, func() {
					cancelRoundCause(errStreamIdleTimeout)
				})
				defer idleTimer.Stop()
			}
			resetIdle := func() {
				if idleTimer != nil {
					idleTimer.Reset(time.Duration(idleSeconds) * time.Second)
				}
			}
			for {
				if event != nil {
					chunks = append(chunks, event)
					encoded.Reset()
					if encodeErr := sse.Encode(&encoded, sse.Event{Id: event.LastEventID, Event: event.Type, Data: event.Data}); encodeErr != nil {
						err = encodeErr
						break
					}
					if !committed {
						request.markCommitted()
						committed = true
					}
					n, writeErr := c.Writer.Write(encoded.Bytes())
					if writeErr == nil && n != encoded.Len() {
						writeErr = io.ErrShortWrite
					}
					if writeErr != nil {
						err = writeErr
						break
					}
					c.Writer.Flush()
					resetIdle()
				}
				if last {
					break
				}
				if !result.events.Next() {
					err = result.events.Err()
					break
				}
				event = result.events.Current()
				resetIdle()
				// 已提交的响应不能再换目标重试, 结束事件自身携带的失败原样转发给客户端, 并在转发后作为本请求终态。
				last, err = inspectStreamEvent(format, event)
			}
			// 看门狗命中时上层读到的是被取消的读错误, 这里把它还原成明确的失败原因。
			if context.Cause(roundCtx) == errStreamIdleTimeout {
				err = errStreamIdleTimeout
			}
			result.events.Close()
			// 事件流已读完, 渠道专用代理的独占连接池到此归还。
			if result.closeIdle != nil {
				result.closeIdle()
			}
			cancelRound()
			// 使用客户端协议转换器聚合已转发事件, 统一取得最终响应正文和用量。
			responseBody, meta, aggregateErr := inbound.AggregateStreamChunks(context.WithoutCancel(ctx), chunks)
			if aggregateErr == nil {
				result.usage = meta.Usage
			}
			// 流式响应结束并聚合出用量后, 按最终结果完成本轮渠道和成员统计。
			metrics := usageMetrics(channelModel.Name, result.usage)
			metrics.WaitTime = roundWaitTime
			// 与提交前的口径保持一致: 客户端在首字节之后离开（ctx 结束）不算成员故障。
			// 否则客户端自己的超时/取消会持续拉低成员质量 —— 线上假死探针的 123 次超时取消
			// 就是这样把 11 个健康成员误判成假死并降级的（见日志包 README 与 T-retry-001）。
			if err == nil {
				metrics.RequestSuccess = 1
			} else if ctx.Err() == nil {
				metrics.RequestFailed = 1
			}
			_ = op.ChannelStatsUpdate(channel.ID, metrics)
			_ = op.ChannelModelStatsUpdate(channelModel.ID, metrics)
			_ = op.ChannelKeyStatsUpdate(channelKey.ID, metrics)
			if err != nil {
				if ctx.Err() != nil {
					request.markCanceled(ctx.Err(), string(responseBody), result.usage)
				} else {
					request.markFailed(err, string(responseBody), result.usage)
				}
				return
			}
			request.markSucceeded(string(responseBody), result.usage)
			return
		}
	}
}

// failRequest 在请求已经没有希望时给客户端一个明确的失败响应。
//
// 触发条件: 单请求尝试次数达到上限（上游 issue #388/#338 的无限重试）, 或所有成员都以
// "请求本身非法" 拒绝了同一份请求。没有这个出口时, 重试循环会一直转到客户端自己超时为止 ——
// 客户端看到的是"卡住", 而服务端还在每秒一次地打上游。
func failRequest(c *gin.Context, inbound transformer.Inbound, request *RequestState, err error) {
	message := "all members failed"
	if err != nil && err.Error() != "" {
		message = err.Error()
	}
	request.markFailed(err, "", nil)
	response := inbound.TransformError(c.Request.Context(), &llm.ResponseError{
		StatusCode: http.StatusBadGateway,
		Detail:     llm.ErrorDetail{Message: message, Type: "upstream_error"},
	})
	c.Data(response.StatusCode, "application/json", response.Body)
	c.Abort()
}

// dropRejectedMembers 去掉本请求内已被上游判定为"请求本身非法"而拒绝过的成员。
// 全部成员都被拒绝时保持原样返回: 该情形由 allMembersRejected 直接结束请求, 不依赖这里兜底。
func dropRejectedMembers(items []model.GroupItem, rejected map[int]bool) []model.GroupItem {
	if len(rejected) == 0 {
		return items
	}
	kept := make([]model.GroupItem, 0, len(items))
	for _, item := range items {
		if rejected[item.ID] {
			continue
		}
		kept = append(kept, item)
	}
	if len(kept) == 0 {
		return items
	}
	return kept
}

// allMembersRejected 报告分组里是否已经每个成员都被"请求本身非法"拒绝过。
// 传展平后的成员列表（而非分组顶层项）: 嵌套分组下顶层项是子分组, 其主键与成员行主键不同。
// 此时继续换成员没有意义, 请求应当以明确错误结束而不是空转。
func allMembersRejected(members []model.GroupItem, rejected map[int]bool) bool {
	if len(members) == 0 {
		return false
	}
	for _, item := range members {
		if !rejected[item.ID] {
			return false
		}
	}
	return true
}

// rejectRequest 以客户端协议的错误格式返回请求级失败, 用于尚未登记状态因而无需定稿的请求。
func rejectRequest(c *gin.Context, inbound transformer.Inbound, err error) {
	response := inbound.TransformError(c.Request.Context(), &llm.ResponseError{
		StatusCode: http.StatusBadRequest,
		Detail:     llm.ErrorDetail{Message: err.Error(), Type: "invalid_request_error"},
	})
	c.Data(response.StatusCode, "application/json", response.Body)
	c.Abort()
}
