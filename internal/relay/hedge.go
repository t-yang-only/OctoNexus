package relay

import (
	"context"
	"errors"
	"log"
	"slices"
	"time"

	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
	"github.com/looplj/axonhub/llm/transformer"
	"github.com/tidwall/sjson"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/op"
)

// 首字竞速 (T-hedge-001)。
//
// 现状: 每轮只挑一个成员, 挑中一个慢启动的成员就得等它的整响应/首事件超时(默认 120s / 30s)才换下一个。
// 竞速: 在**提交首字节之前**, 允许并发向排序靠前的 N 个成员发同一请求, 取第一个给出有效响应者, 其余立即取消。
//
// 三条硬边界:
//  1. 只在提交之前发生 —— 客户端已经收到字节之后成员即定局, 与既有"首字节提交后不可重试"一致;
//  2. 落选的成员不算失败 —— 它是被我们自己取消的, 不进冷却、不记失败、不污染质量/延迟样本;
//  3. 默认关闭 —— 竞速意味着同一份输入可能被多个上游各处理一次, 上游按 token 计费时最坏付 width 份钱。

const (
	// hedgeDefaultWidth 默认并发路数（含首选）。
	hedgeDefaultWidth = 2
	// hedgeDefaultAfterMs 默认慢启动阈值: 首选在该毫秒数内没给出首个有效响应就追加竞速路。
	hedgeDefaultAfterMs = 800
	// hedgeMaxWidth 并发路数上限: 再宽对首字收益很小, 但费用是线性增长的。
	hedgeMaxWidth = 5
	// hedgeMaxPeakInFlight 高峰期触发的在途数上限（防手滑写个天文数字）。
	hedgeMaxPeakInFlight = 64
)

type hedgeSettings struct {
	enabled bool
	width   int // 含首选
	afterMs int // 0 表示不做延迟触发
	peak    int // 0 表示不做在途触发
}

// hedgeSettingsOf 读取并夹紧分组上的竞速配置（老分组缺少这些键时按默认值, 且默认关闭）。
func hedgeSettingsOf(rc model.GroupRelayConfig) hedgeSettings {
	settings := hedgeSettings{
		enabled: rc.HedgeEnabled,
		width:   rc.HedgeWidth,
		afterMs: rc.HedgeAfterMs,
		peak:    rc.HedgePeakInFlight,
	}
	if settings.width <= 0 {
		settings.width = hedgeDefaultWidth
	}
	if settings.width < 2 {
		settings.width = 2
	}
	if settings.width > hedgeMaxWidth {
		settings.width = hedgeMaxWidth
	}
	if settings.afterMs < 0 {
		settings.afterMs = 0
	}
	if settings.afterMs == 0 && settings.peak == 0 && settings.enabled {
		// 开启竞速但两个触发条件都没配: 给延迟触发一个默认阈值, 让"打开开关就有效果"。
		settings.afterMs = hedgeDefaultAfterMs
	}
	if settings.peak < 0 {
		settings.peak = 0
	}
	if settings.peak > hedgeMaxPeakInFlight {
		settings.peak = hedgeMaxPeakInFlight
	}
	return settings
}

// preparedTarget 是一路尝试所需的全部上下文: 成员、渠道、凭据、出站配置与**独立的**请求体副本。
// 独立副本是并发的前提: applyChannelConfig 会改写 Body/JSONBody/Headers, 共享同一份会数据竞争。
type preparedTarget struct {
	item           model.GroupItem
	grant          model.ChannelGrant
	channel        model.Channel
	channelModel   model.ChannelModel
	channelKey     model.ChannelKey
	outbound       transformer.Outbound
	passthrough    bool
	targetProtocol model.Protocol
	raw            *httpclient.Request
}

// prepareRoundTarget 解析一条成员并把出站配置准备成一个可并发使用的独立副本。
func prepareRoundTarget(item model.GroupItem, base *httpclient.Request, streaming bool,
	format llm.APIFormat, want model.Protocol) (preparedTarget, error) {
	target := preparedTarget{item: item}
	grant, err := op.ChannelGrantGet(item.GrantRef())
	if err != nil {
		return target, err
	}
	if grant.ChannelModel == nil || grant.ChannelKey == nil {
		return target, errors.New("channel grant is incomplete")
	}
	target.grant = grant
	target.channelModel = *grant.ChannelModel
	target.channelKey = *grant.ChannelKey
	channel, err := op.ChannelGet(target.channelModel.ChannelID)
	if err != nil {
		return target, err
	}
	target.channel = channel

	raw := cloneHTTPRequest(base)
	raw.Body, err = sjson.SetBytes(raw.Body, "model", target.channelModel.Name)
	if err != nil {
		return target, err
	}
	if streaming && format == llm.APIFormatOpenAIChatCompletion {
		// OpenAI Chat 流式响应要求带上末尾用量对象。
		raw.Body, err = sjson.SetBytes(raw.Body, "stream_options.include_usage", true)
		if err != nil {
			return target, err
		}
	}
	target.raw = raw
	outbound, protocol, passthrough, err := buildOutbound(channel, grant, target.channelKey, want)
	if err != nil {
		return target, err
	}
	target.outbound = outbound
	target.targetProtocol = protocol
	target.passthrough = passthrough
	return target, nil
}

// cloneHTTPRequest 深拷贝请求: Body/JSONBody/Headers/Query 必须是独立副本, 因为出站前会被改写。
func cloneHTTPRequest(src *httpclient.Request) *httpclient.Request {
	if src == nil {
		return &httpclient.Request{}
	}
	clone := *src
	clone.Body = slices.Clone(src.Body)
	if len(src.JSONBody) > 0 {
		clone.JSONBody = slices.Clone(src.JSONBody)
	}
	if src.Headers != nil {
		clone.Headers = src.Headers.Clone()
	}
	if src.Query != nil {
		query := make(map[string][]string, len(src.Query))
		for key, values := range src.Query {
			query[key] = slices.Clone(values)
		}
		clone.Query = query
	}
	return &clone
}

// rankedHedgeCandidatesWithFeatures 是带请求特征的竞速候选入口。
//
// 智能路由（T-smart-001）下**只在选中那一档内竞速**：档位决定「去哪一档」，竞速只决定「这一档里谁先答」。
// 不这样收口的话，简单请求会因为竞速把靠前的强成员也拉进来跑一遍 —— 复杂度的成本控制被竞速悄悄绕过，
// 而且用户会看到简单请求也在花贵渠道的钱（竞速本身就是要多发一份请求的）。
// 档内没有可竞速成员时与选路一致地回退到全体成员。其余模式行为与不带特征时逐字一致。
func (deps routeDeps) rankedHedgeCandidatesWithFeatures(group model.Group, smart SmartRoute) []model.GroupItem {
	// 额度分压 (T-allocate-001) 的竞速候选与选路同一套权重与定序, 但**只读**:
	// 竞速是一次预演, 不该推进分配累加器 (否则每次预演都改了下一次当选者, 分配比例被自己的候选计算带偏)。
	if group.Mode == model.GroupModeAllocate {
		if ranked := rankedAllocateCandidates(group, deps, smart.Features); len(ranked) > 0 {
			return ranked
		}
		return deps.rankedHedgeCandidates(group)
	}
	if group.Mode != model.GroupModeSmart {
		return deps.rankedHedgeCandidates(group)
	}
	tiered := smartTierItems(group.Items, smart.DecisionMembers,
		SmartComplex(smart.Features, group.RelayConfig.SmartRouteThreshold))
	if len(tiered) > 0 {
		tierGroup := group
		tierGroup.Mode = model.GroupModeFailover // 档内走 failover 口径（含加权轮询与延迟档内排序）
		tierGroup.Items = tiered
		if ranked := deps.rankedHedgeCandidates(tierGroup); len(ranked) > 0 {
			return ranked
		}
	}
	return deps.rankedHedgeCandidates(group)
}

// rankedHedgeCandidates 返回该分组按当前模式排序、且可参与竞速的成员（冷却中/不可用/额度归零的不参与）。
func (deps routeDeps) rankedHedgeCandidates(group model.Group) []model.GroupItem {
	routeMu.Lock()
	route := routes[group.ID]
	if route == nil {
		route = &RouteState{GroupID: group.ID, Cooldowns: make(map[int]int64)}
		routes[group.ID] = route
	}
	cooldowns := cloneCooldowns(route.Cooldowns)
	round := route.balanceRound
	routeMu.Unlock()

	items := group.Items
	nowMs := time.Now().UnixMilli()
	switch group.Mode {
	case model.GroupModeLowestCost:
		if ranked, err := rankByLowestCost(items, cooldowns, nowMs, nil, deps.cost); err == nil {
			return ranked
		}
	case model.GroupModeQualityFirst:
		if ranked, err := rankByQuality(items, cooldowns, nowMs, nil, deps.quality); err == nil {
			return ranked
		}
	case model.GroupModeLowestLatency:
		if ranked, err := rankByLatency(items, cooldowns, nowMs, nil, deps.latency); err == nil {
			return ranked
		}
	case model.GroupModeLeastBusy:
		if ranked, err := rankByBusy(items, cooldowns, nowMs, nil, deps.busy); err == nil {
			return ranked
		}
	case model.GroupModeLowestTpmRpm:
		if ranked, err := rankByRecentLoad(items, cooldowns, nowMs, nil, deps.load); err == nil {
			return ranked
		}
	}
	// failover / manual: 与加权轮询同一套口径（含延迟档内排序）。
	ranked, err := rankCandidates(items, cooldowns, nowMs, nil, deps.latency, round)
	if err != nil {
		return nil
	}
	return ranked
}

func cloneCooldowns(source map[int]int64) map[int]int64 {
	if len(source) == 0 {
		return map[int]int64{}
	}
	clone := make(map[int]int64, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

// groupInFlight 返回该分组当前在途请求数（按客户端模型名=分组名统计, 与 least_busy 同一份活动请求注册表）。
func groupInFlight(modelName string) int {
	return inFlightByModel(modelName)
}

// hedgeLoser 记录一路落选的尝试, 仅用于日志与观测, 不计失败。
type hedgeLoser struct {
	itemID int
	err    error
}

// runRoundWithHedge 并发执行主路与竞速路, 返回第一个成功的尝试。
//
// 触发方式:
//   - startHedgeImmediately=true（高峰期: 在途数已达阈值）时, 竞速路与主路同时发出;
//   - 否则 afterMs 毫秒后主路仍未有结果, 再追加竞速路（冷启动/被限速时的慢启动）。
//
// 任一路成功后立即取消其余路; 全部失败时返回主路的错误（保持既有失败语义与重试/冷却行为）。
func runRoundWithHedge(ctx context.Context, group model.Group, primary preparedTarget, hedge []preparedTarget,
	streaming bool, format llm.APIFormat, addHedgeImmediately bool, afterMs int) (preparedTarget, *upstreamResponse, error, []hedgeLoser) {
	timeoutSeconds := group.RelayConfig.MemberNonStreamResponseTimeoutSeconds
	timeoutErr := errors.New("upstream non-stream response timeout")
	if streaming {
		timeoutSeconds = group.RelayConfig.MemberStreamFirstEventTimeoutSeconds
		timeoutErr = errors.New("upstream stream first event timeout")
	}
	if timeoutSeconds <= 0 {
		timeoutSeconds = model.DefaultGroupRelayConfig().MemberNonStreamResponseTimeoutSeconds
		if streaming {
			timeoutSeconds = model.DefaultGroupRelayConfig().MemberStreamFirstEventTimeoutSeconds
		}
	}

	raceCtx, cancelRace := context.WithCancel(ctx)
	defer cancelRace()

	type outcome struct {
		target preparedTarget
		result *upstreamResponse
		err    error
	}
	results := make(chan outcome, len(hedge)+1)
	started := 1

	run := func(target preparedTarget) {
		roundCtx, cancelCause := context.WithCancelCause(raceCtx)
		timer := time.AfterFunc(time.Duration(timeoutSeconds)*time.Second, func() {
			cancelCause(timeoutErr)
		})
		var result *upstreamResponse
		var err error
		if target.passthrough {
			result, err = sendPassthrough(roundCtx, format, target.raw, target.channel, target.channelKey, target.outbound,
				streaming, target.channelModel.Name)
		} else {
			result, err = sendConverted(roundCtx, format, target.raw, target.channel, target.channelKey, target.outbound, streaming)
		}
		if !timer.Stop() {
			cancelCause(timeoutErr)
		}
		if context.Cause(roundCtx) == timeoutErr {
			err = timeoutErr
			if result != nil && result.events != nil {
				result.events.Close()
				if result.closeIdle != nil {
					result.closeIdle()
				}
			}
			result = nil
		}
		results <- outcome{target: target, result: result, err: err}
	}

	go run(primary)
	if addHedgeImmediately {
		for _, target := range hedge {
			started++
			go run(target)
		}
		hedge = nil
	} else if len(hedge) > 0 && afterMs > 0 {
		timer := time.AfterFunc(time.Duration(afterMs)*time.Millisecond, func() {
			// 主路到点还没回来才追加: 成功路径会 cancelRace, 这里的 goroutine 随即退出。
			if raceCtx.Err() != nil {
				return
			}
			for _, target := range hedge {
				go run(target)
			}
		})
		defer timer.Stop()
		started += len(hedge)
	}

	losers := make([]hedgeLoser, 0, started-1)
	var primaryErr error
	for received := 0; received < started; received++ {
		select {
		case <-ctx.Done():
			cancelRace()
			// 客户端取消: 保持既有语义, 由调用方按 ctx.Err() 判定。
			return primary, nil, ctx.Err(), losers
		case item := <-results:
			if item.err == nil {
				cancelRace()
				// 剩余未回来的路会被取消, 记进落选用于日志。
				for i := received + 1; i < started; i++ {
					select {
					case leftover := <-results:
						losers = append(losers, hedgeLoser{itemID: leftover.target.item.ID, err: leftover.err})
					case <-time.After(200 * time.Millisecond):
						i = started
					}
				}
				return item.target, item.result, nil, losers
			}
			if item.target.item.ID == primary.item.ID {
				primaryErr = item.err
			}
			losers = append(losers, hedgeLoser{itemID: item.target.item.ID, err: item.err})
		}
	}
	if primaryErr == nil {
		primaryErr = errors.New("all hedge attempts failed")
	}
	return primary, nil, primaryErr, losers
}

// logHedge 打印一次竞速的执行情况, 便于事后算"多花的钱换回了多少首字"。
func logHedge(group model.Group, primary preparedTarget, winner preparedTarget, losers []hedgeLoser, waited time.Duration) {
	if winner.item.ID == primary.item.ID && len(losers) == 0 {
		return
	}
	lost := make([]int, 0, len(losers))
	for _, loser := range losers {
		if loser.itemID != winner.item.ID {
			lost = append(lost, loser.itemID)
		}
	}
	reason := ""
	if winner.item.ID != primary.item.ID {
		reason = "hedge won"
	} else {
		reason = "primary won"
	}
	log.Printf("hedge group=%s(%d) %s primary=%d winner=%d loser=%v waited=%dms",
		group.Name, group.ID, reason, primary.item.ID, winner.item.ID, lost, waited.Milliseconds())
}
