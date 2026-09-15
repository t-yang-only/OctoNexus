package task

import (
	"context"
	"sync"

	"github.com/bestruirui/octopus/internal/health"
	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

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
		quota, used, remaining, ok := health.FetchBalance(ctx, target.BaseURL, target.MonitorToken, target.UseProxy)
		if !ok {
			// 上游不可达/字段不可解析：状态未知，宁可不动（防误停第一验收项）。
			log.Debugf("quota scan skipped (unreachable or unparsable): channel=%s(%d)", target.ChannelName, target.ChannelID)
			continue
		}
		fingerprint := health.FingerprintBalance(quota, used, remaining)
		quotaScanMu.Lock()
		last, seen := quotaScanFinger[target.ChannelID]
		changed := !seen || last != fingerprint
		if changed {
			quotaScanFinger[target.ChannelID] = fingerprint
		}
		quotaScanMu.Unlock()
		if !changed {
			continue
		}

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
				continue
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
}
