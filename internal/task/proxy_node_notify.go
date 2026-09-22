package task

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// T-proxy-002 出口节点挡住渠道时主动通知。
//
// 为什么需要它：告警规则引擎的两个指标（错误率/延迟）都基于**已发生的转发流量**，
// 而"出口节点坏了"是流量还没发生的状态 —— 渠道被坏节点挡住之后根本没有请求进来，
// 于是错误率为零、永远不触发告警。实测这台机器上 115 个节点里 99 个不通、
// 15 个渠道里 10 个绑着坏节点，而面板与通知渠道全程静默。
//
// 去重是必需的：节点每 5 分钟探一轮，不去重就是每 5 分钟一条骚扰，
// 结果必然是用户把通知关掉 —— 那比不通知更糟。
// 口径：只在"受影响的渠道集合发生变化"时发一条；集合不变则安静。
// 集合清空（全部恢复）时也发一条，让用户知道问题结束了。

var (
	proxyBlockMu    sync.Mutex
	proxyBlockState string // 上一次通知过的受影响渠道指纹；空串表示上次是"正常"
)

// proxyBlockerFingerprint 把受影响渠道集合压成一个可比较的指纹。
//
// 只含渠道 ID 与节点 ID，不含错误原文：节点错误文案偶有抖动
// （超时 vs EOF），把文案算进指纹会让集合没变也反复通知。
func proxyBlockerFingerprint(blockers []op.ProxyChannelBlocker) string {
	if len(blockers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(blockers))
	for _, b := range blockers {
		parts = append(parts, fmt.Sprintf("%d@%d", b.ChannelID, b.NodeID))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// proxyBlockedChannelNotify 在节点探活之后跑，把"渠道被坏节点挡住"这一事实推给通知渠道。
func proxyBlockedChannelNotify() {
	blockers, err := op.ProxyNodeBlockedChannels()
	if err != nil {
		log.Warnf("检查被节点挡住的渠道失败：%v", err)
		return
	}
	fingerprint := proxyBlockerFingerprint(blockers)

	proxyBlockMu.Lock()
	previous := proxyBlockState
	proxyBlockState = fingerprint
	proxyBlockMu.Unlock()

	if fingerprint == previous {
		// 集合没变：安静。这是"不要变成骚扰源"的关键一步。
		return
	}

	ctx := context.Background()
	if len(blockers) == 0 {
		if previous == "" {
			return // 一直是正常的，不必通知
		}
		log.Infof("出口节点已恢复：不再有渠道被不通的节点挡住")
		notify.Send(ctx, notify.Event{
			Type:    "proxy_node_recovered",
			Title:   "OctoNexus 出口节点已恢复",
			Message: "此前被不通出口节点挡住的渠道已全部恢复，转发应能正常进行。",
			At:      time.Now(),
		})
		return
	}

	lines := make([]string, 0, len(blockers)+2)
	lines = append(lines, fmt.Sprintf("有 %d 个启用中的渠道绑定了探活不通的出口节点，这些渠道当前无法转发：", len(blockers)))
	for _, b := range blockers {
		detail := b.NodeError
		if detail == "" {
			detail = "探活失败"
		}
		lines = append(lines, fmt.Sprintf("· %s（渠道 #%d）← 节点 %s（#%d）：%s",
			b.ChannelName, b.ChannelID, b.NodeName, b.NodeID, detail))
	}
	lines = append(lines, "", "处理方式：在「代理」页换一个探活通过的节点，或到节点所属订阅处更新节点。")

	log.Warnf("出口节点挡住渠道：%d 个（集合相对上次发生变化）", len(blockers))
	notify.Send(ctx, notify.Event{
		Type:    "proxy_node_blocked",
		Title:   "OctoNexus 出口节点不可用",
		Message: strings.Join(lines, "\n"),
		At:      time.Now(),
	})
}
