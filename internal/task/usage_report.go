package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// 用量报告任务（吸收上游 lingyuins/octopus 的 Usage Reports）。
//
// 投递放在 task 而不是 op：internal/notify 经 internal/rhttp 反向依赖 internal/op，
// 在 op 里 import notify 会构成 import cycle。task 同时依赖两者，是这一层的自然归属。

// UsageReportSendNow 立即组装并投递一份报告。
//
// force=true（面板上的"立即发送"）跳过周期记账，让用户想试几次就试几次——
// 否则"试一下"会把当天的正式报告名额吃掉，用户第二天才发现没收到日报。
// 定时任务传 force=false，保证同一周期只发一次。
func UsageReportSendNow(ctx context.Context, period model.UsageReportPeriod, force bool) (op.UsageReport, []notify.Result, error) {
	now := time.Now()
	report, err := op.UsageReportPrepare(ctx, period, now)
	if err != nil {
		return report, nil, err
	}

	results := notify.Send(ctx, notify.Event{
		Type:    "usage_report",
		Title:   "OctoNexus 用量报告",
		Message: report.Text,
		Detail:  report,
		At:      now,
	})

	delivered, failed := 0, 0
	for _, r := range results {
		if r.Sent {
			delivered++
		} else {
			failed++
		}
	}

	if !force {
		if err := op.UsageReportMarkSent(ctx, report, delivered, failed, now); err != nil {
			return report, results, err
		}
	}
	return report, results, nil
}

// usageReportOnce 是定时任务的一轮：只有"到点 + 本周期没发过"才真的发。
//
// 任务按小时注册，所以"到点"的判定就是"当前整点等于配置时刻"；
// 不在那个整点跑就什么都不做（一轮一次判断，代价可以忽略）。
func usageReportOnce() {
	ctx := context.Background()
	period, key, due := op.UsageReportDue(ctx, time.Now())
	if !due {
		return
	}
	report, results, err := UsageReportSendNow(ctx, period, false)
	if err != nil {
		log.Warnf("usage report %s failed: %v", key, err)
		return
	}
	delivered := 0
	for _, r := range results {
		if r.Sent {
			delivered++
		}
	}
	// 送达 0 个渠道不是错误（用户可能压根没配通知渠道），但要说清楚，
	// 否则"开了报告却什么都没收到"会变成一个查不出来的问题。
	if delivered == 0 {
		log.Warnf("usage report %s built but delivered to 0 channels (no notification channel enabled?)", key)
		return
	}
	log.Infof("usage report %s delivered to %d channel(s), %d request(s)", key, delivered, report.Requests)
}
