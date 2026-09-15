package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
	"github.com/looplj/axonhub/llm"
	"github.com/looplj/axonhub/llm/httpclient"
)

// R-probe-001 主动探活: 已进入冷却的成员不必干等冷却到期, 由后台任务主动发一次最小真实请求,
// 一旦确认恢复就立刻解除冷却, 让流量在冷却到期前就回到该成员。
//
// 三条口径:
//  1. 探的是"成员绑定的真实模型", 不是渠道的 /v1/models: 凭据有效但模型仍在上游报错的成员,
//     只探模型列表会被误判成已恢复, 而冷却后的第一次真实请求照样失败。
//  2. 默认关闭 (route_probe_enabled): 每次探测都是一次真实计费请求, 是否花这笔钱由用户决定。
//  3. 探测失败不改冷却时长、不重试: 冷却是"成员不可用"的既有证据, 探测失败只是再次确认,
//     只记日志, 下一轮再探; 只有探测成功才解除冷却, 并作为一次成功样本进入质量/延迟指标。
//
// 只探仍在冷却期内的成员: 冷却已到期的成员本来就会被下一个真实请求当作探测请求放行
// (见 route.go 的 ProbeItemID 语义), 后台不必跟它重复。

const (
	// probeMaxTokens 是探测请求的输出上限: 1 token 足够证明上游可用, 也把探测成本压到最低。
	probeMaxTokens = 1
	// probeTimeoutSeconds 是单次探测的上游等待上限, 与分组配置的非流式超时解耦:
	// 探测是后台任务, 不能因为某个分组配了很长的超时就把整轮探活拖住。
	probeTimeoutSeconds = 20
	// probeMaxMembersPerRun 是单轮探测的成员上限, 避免一次任务把上游打爆; 其余成员留给下一轮。
	probeMaxMembersPerRun = 10
)

// probeTarget 是一个待探测的成员: 它所在的分组 (路由状态按分组持有) 与展平后的成员行。
type probeTarget struct {
	group model.Group
	item  model.GroupItem
}

// probeResult 是探测成功时带回的信息, 供恢复事件与指标复用。
type probeResult struct {
	latencyMs   int64
	channelName string
	channelID   int
}

// coolingProbeTargets 收集当前所有分组里仍在冷却期内的成员, 按分组 ID 与成员 ID 定序以便复现。
// 冷却的键是展平后的成员行 ID, 故这里与转发热路径用同一套展平口径。
func coolingProbeTargets() []probeTarget {
	now := time.Now().UnixMilli()
	groups := op.GroupList()
	targets := make([]probeTarget, 0, len(groups))
	for _, group := range groups {
		// 手动模式没有进程内路由与冷却, 无可探。
		if group.Mode == model.GroupModeManual {
			continue
		}
		group = group.WithItems(op.FlattenGroupItems(group))
		for itemID, deadline := range RouteStateOf(group).Cooldowns {
			// 冷却已到期: 交给下一个真实请求充当探测, 后台不重复。
			if deadline <= now {
				continue
			}
			item := itemOf(group, itemID)
			if item.ID == 0 {
				continue
			}
			// 渠道/凭据已被停用的成员本就转发不了: 探它只是白花钱, 等人工改回可用后随冷却一起自然恢复。
			if !item.Available {
				continue
			}
			targets = append(targets, probeTarget{group: group, item: item})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].group.ID != targets[j].group.ID {
			return targets[i].group.ID < targets[j].group.ID
		}
		return targets[i].item.ID < targets[j].item.ID
	})
	return targets
}

// ProbeCoolingMembers 跑一轮主动探活: 对仍在冷却期内的成员各发一次最小真实请求, 成功即解除其冷却
// 并推送恢复事件。返回本轮探测成员数与恢复数, 供任务日志与测试断言。
func ProbeCoolingMembers(ctx context.Context) (probed int, recovered int) {
	for _, target := range coolingProbeTargets() {
		if probed >= probeMaxMembersPerRun || ctx.Err() != nil {
			break
		}
		probed++
		result, err := probeMember(ctx, target)
		if err != nil {
			// 只记日志: 不改冷却时长也不重试, 下一轮再探。
			log.Debugf("route probe failed: group=%s(%d) member=%d model=%s: %v",
				target.group.Name, target.group.ID, target.item.ID, target.item.ModelName, err)
			continue
		}
		// 冷却已被别的路径解除 (例如真实请求已经成功) 时不重复记账。
		if !clearMemberCooldown(target.group, target.item.ID) {
			continue
		}
		recordMemberOutcome(target.item.ID, true, result.latencyMs)
		recovered++
		log.Infof("route probe recovered: group=%s(%d) member=%d channel=%s model=%s latency=%dms",
			target.group.Name, target.group.ID, target.item.ID, result.channelName, target.item.ModelName, result.latencyMs)
		if err := notify.PostWebhook(ctx, notify.Event{
			Type:      "route_probe_recovered",
			Channel:   result.channelName,
			ChannelID: result.channelID,
			Message:   "cooling member recovered, cooldown cleared",
			Detail: map[string]any{
				"group":      target.group.Name,
				"group_id":   target.group.ID,
				"member_id":  target.item.ID,
				"model":      target.item.ModelName,
				"latency_ms": result.latencyMs,
			},
		}); err != nil {
			log.Warnf("route probe webhook failed: member=%d: %v", target.item.ID, err)
		}
	}
	return probed, recovered
}

// probeMember 对单个成员发一次最小真实请求。请求走与转发热路径同一套地址拼接、凭据注入与渠道参数覆盖,
// 否则会出现"探测通过但真实请求失败"的假恢复。
func probeMember(ctx context.Context, target probeTarget) (probeResult, error) {
	grant, err := op.ChannelGrantGet(target.item.GrantRef())
	if err != nil {
		return probeResult{}, fmt.Errorf("grant unavailable: %w", err)
	}
	if grant.ChannelKey == nil {
		return probeResult{}, fmt.Errorf("grant %d has no credential", grant.ID)
	}
	channel, err := op.ChannelGet(grant.ChannelModel.ChannelID)
	if err != nil {
		return probeResult{}, fmt.Errorf("channel unavailable: %w", err)
	}
	result := probeResult{channelName: channel.Name, channelID: channel.ID}

	// 探测协议优先 OpenAI Chat, 授权不支持时由 buildOutbound 按既有兜底顺序选 (Anthropic > Responses > Chat);
	// 只探授权自身支持的协议, 否则协议不匹配会被误判成成员不可用。
	outbound, _, _, err := buildOutbound(channel, grant, *grant.ChannelKey, model.ProtocolOpenAIChatCompletion)
	if err != nil {
		return result, fmt.Errorf("build probe outbound: %w", err)
	}

	probeText := "probe"
	maxTokens := int64(probeMaxTokens)
	request, err := outbound.TransformRequest(ctx, &llm.Request{
		Model:     grant.ChannelModel.Name,
		Messages:  []llm.Message{{Role: "user", Content: llm.MessageContent{Content: &probeText}}},
		MaxTokens: &maxTokens,
	})
	if err != nil {
		return result, fmt.Errorf("build probe request: %w", err)
	}
	// 渠道参数覆盖与自定义 Header 对探测与真实请求一视同仁。
	if err := applyChannelConfig(channel, request); err != nil {
		return result, fmt.Errorf("apply channel config: %w", err)
	}

	client, closeIdle, err := resolveUpstreamClient(channel)
	if err != nil {
		return result, fmt.Errorf("resolve upstream client: %w", err)
	}
	if closeIdle != nil {
		defer closeIdle()
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeoutSeconds*time.Second)
	defer cancel()

	startedAt := time.Now()
	response, err := httpclient.NewHttpClientWithClient(client).Do(probeCtx, request)
	result.latencyMs = time.Since(startedAt).Milliseconds()
	if err != nil {
		var failure *httpclient.Error
		if errors.As(err, &failure) && len(failure.Body) > 0 {
			return result, fmt.Errorf("%w: %s", err, truncateProbeBody(failure.Body))
		}
		return result, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return result, fmt.Errorf("upstream returned %d: %s", response.StatusCode, truncateProbeBody(response.Body))
	}
	// 部分上游把错误包在 200 正文的 error 字段里下发, 与真实转发同口径: 带 error 不算恢复。
	if err := probeBodyError(response.Body); err != nil {
		return result, err
	}
	return result, nil
}

// probeBodyError 识别"以 200 下发的错误终态": 顶层 error 非空即判失败。非 JSON 正文不据此判定。
func probeBodyError(body []byte) error {
	if len(body) == 0 {
		return nil
	}
	var payload struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	if len(payload.Error) == 0 || string(payload.Error) == "null" {
		return nil
	}
	return fmt.Errorf("upstream returned error body: %s", truncateProbeBody(payload.Error))
}

// truncateProbeBody 截断上游正文, 避免把整段响应写进日志与告警事件。
func truncateProbeBody(body []byte) string {
	const limit = 200
	if len(body) > limit {
		return string(body[:limit]) + "..."
	}
	return string(body)
}
