package task

import (
	"context"
	"net/http"
	"sync"

	"github.com/bestruirui/octopus/internal/health"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/bestruirui/octopus/internal/rhttp"
	"github.com/charmbracelet/log"
)

// balanceClientFor 为该渠道构造读余额用的客户端：与转发走同一条出口。
//
// 绑定节点时出口是该节点的本地入站；节点没就绪就**不读**——宁可这一轮"未读到"，
// 也不能从真实 IP 去读余额（那正是分出口要防的关联痕迹）。
func balanceClientFor(target op.QuotaScanTarget) (*http.Client, string, string) {
	if target.ProxyNodeID > 0 {
		endpoint, err := op.ProxyNodeEndpoint(target.ProxyNodeID)
		if err != nil {
			return nil, health.ReasonProxyNode, "绑定的出口节点未就绪：" + err.Error()
		}
		client, err := rhttp.New(endpoint)
		if err != nil {
			return nil, health.ReasonProxyNode, "出口节点不可用：" + err.Error()
		}
		return client, "", ""
	}
	if target.UseProxy {
		client, err := rhttp.Proxy()
		if err != nil || client == nil {
			return nil, health.ReasonUnreachable, "渠道代理不可用：" + errText(err)
		}
		return client, "", ""
	}
	client, err := rhttp.Direct()
	if err != nil || client == nil {
		return nil, health.ReasonUnreachable, "直连客户端不可用：" + errText(err)
	}
	return client, "", ""
}

// errText 把可能为 nil 的 error 变成一句可展示的话。
func errText(err error) string {
	if err == nil {
		return "未知原因"
	}
	return err.Error()
}

// T-quota-001 接线·消费链（163 清单 ①③⑤，NM-CUR-217/L3）。
// 周期任务逐渠道采集（health.FetchBalance，只读 30s 超时）→ 指纹去重 →
// 阈值告警事件（当前消费方为日志，U-alert-001 接通道后改为发通知）→
// 归零停用（op.QuotaZeroStop，remaining<=0 恒判，不依赖告警阈值是否配置）。
//
// 防误停第一验收项：采集失败/解析失败一律跳过（不采集成功就绝不改动凭据状态）；
// 指纹未变亦跳过，避免每 5 分钟重复触发停用与重复告警。
// 指纹表是进程内状态：重启后首轮按"首次变化"处理一次，QuotaZeroStop 幂等（already_stopped 审计），无害。

var (
	quotaScanMu     sync.Mutex
	quotaScanFinger = map[int]string{} // 渠道 ID → 上次采集余额指纹
)

// QuotaScanNow 立刻跑一轮余额扫描（管理面"重新扫描"按钮走这里）。
// 与周期任务共用同一条链路：并发触发不会出现两轮同时改额度状态。
func QuotaScanNow() {
	quotaScanOnce()
}

// resetQuotaScanStateForTest 清空指纹表，供集成测试间隔轮生效互不污染。
func resetQuotaScanStateForTest() {
	quotaScanMu.Lock()
	defer quotaScanMu.Unlock()
	quotaScanFinger = map[int]string{}
}

// quotaScanOnce 跑一轮全量余额扫描。单个渠道失败只跳过该渠道，不阻断整轮。
func quotaScanOnce() {
	ctx := context.Background()
	threshold := op.QuotaAlertThreshold()
	for _, target := range op.QuotaScanTargets() {
		// ???????????????????"????/???"???????????
		// ??? 15 ?? 12 ????????3 ??????????????????
		if reading, ok := op.CollectorReadingForChannel(ctx, target.ChannelID); ok {
			log.Infof("quota scan (collector #%d): channel=%s(%d) remaining=%.2f",
				reading.SourceID, target.ChannelName, target.ChannelID, reading.Balance)
			handleQuotaReading(ctx, target, reading.Balance, threshold)
			continue
		}
		client, reasonKey, reasonText := balanceClientFor(target)
		if client == nil {
			// 绑定的出口节点没就绪时不去读：走真实 IP 读余额会留下可关联的痕迹。
			op.RecordChannelBalanceReason(target.ChannelID, reasonKey, reasonText)
			log.Debugf("quota scan skipped (exit not ready): channel=%s(%d)", target.ChannelName, target.ChannelID)
			continue
		}
		// 按协议族依次探测（/user/balance → /usage → /api/user/self → OpenAI Billing）：
		// 实测只有 3/15 家能用 sk- Key 直接读到余额，而它们恰好走的是三种不同接口，
		// 只试 new-api 口径会把本来读得到的站点也记成"未读到"。
		read := health.ReadBalanceAuto(ctx, target.BaseURL, target.MonitorToken, client, op.BalanceEndpointPath())
		if !read.OK {
			// 上游不可达/字段不可解析：状态未知，宁可不动（防误停第一验收项）。
			// 原因要留下来：面板只报"未读到"而说不出为什么，用户无从下手（本轮实测）。
			op.RecordChannelBalanceReason(target.ChannelID, read.ReasonKey, read.ReasonText)
			log.Debugf("quota scan skipped (%s): channel=%s(%d)", read.ReasonKey, target.ChannelName, target.ChannelID)
			continue
		}
		op.RecordChannelBalanceReason(target.ChannelID, "", "")
		quota, used, remaining := read.Quota, read.Used, read.Remaining
		fingerprint := health.FingerprintBalance(quota, used, remaining)
		quotaScanMu.Lock()
		last, seen := quotaScanFinger[target.ChannelID]
		changed := !seen || last != fingerprint
		if changed {
			quotaScanFinger[target.ChannelID] = fingerprint
			// 顺手把余额快照留给加权选路与总余额用（内存, 不落库）；
			// 单位口径一并带上：货币读数不能再按"点"折算（否则 32.53 美元会显示成 $0.000065）。
			op.RecordChannelBalanceWithUnit(target.ChannelID, remaining, read.InCurrency)
		}
		quotaScanMu.Unlock()
		if !changed {
			continue
		}

		handleQuotaReading(ctx, target, remaining, threshold)
	}
}

// handleQuotaReading 是"读到数之后"的统一处理：额度告警 + 归零自动停表。
// 两条读数路径（内置协议探测 / 账号级采集凭据）共用它 —— 只给其中一条接告警，
// 会让"采集凭据读到的余额低于阈值"静默不报警，而低额度告警恰恰是用户最需要的那条。
func handleQuotaReading(ctx context.Context, target op.QuotaScanTarget, remaining, threshold float64) {
	log.Infof("quota scan: channel=%s(%d) remaining=%.2f", target.ChannelName, target.ChannelID, remaining)
	if health.BelowThreshold(remaining, threshold) {
		// 告警事件出口: 落日志 + 投递到全部启用渠道 (R-alert-001: webhook/飞书/钉钉/企微/SMTP)。
		log.Warnf("quota alert: channel=%s(%d) remaining=%.2f below threshold=%.2f", target.ChannelName, target.ChannelID, remaining, threshold)
		notify.Send(ctx, notify.Event{
			Type:      "quota_alert",
			Channel:   target.ChannelName,
			ChannelID: target.ChannelID,
			Message:   "channel balance below threshold",
			Remaining: remaining,
			Detail:    map[string]float64{"threshold": threshold},
		})
	}
	if remaining <= model.QuotaZeroThreshold {
		stopped, err := op.QuotaZeroStop(target.ChannelID, remaining)
		if err != nil {
			log.Errorf("quota zero-stop failed: channel=%s(%d): %v", target.ChannelName, target.ChannelID, err)
			return
		}
		if len(stopped) > 0 {
			log.Warnf("quota zero-stop: channel=%s(%d) disabled %d keys", target.ChannelName, target.ChannelID, len(stopped))
			notify.Send(ctx, notify.Event{
				Type:      "quota_zero_stop",
				Channel:   target.ChannelName,
				ChannelID: target.ChannelID,
				Message:   "channel balance exhausted, keys auto disabled",
				Remaining: remaining,
				Detail:    map[string]any{"stopped_keys": stopped},
			})
		}
	}
}
