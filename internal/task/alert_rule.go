package task

import (
	"context"
	"time"

	"github.com/bestruirui/octopus/internal/model"
	"github.com/bestruirui/octopus/internal/notify"
	"github.com/bestruirui/octopus/internal/op"
	"github.com/charmbracelet/log"
)

// 告警规则的定时评估（吸收上游 lingyuins/octopus 的 Alerts）。
//
// 投递放在 task 而不是 op：internal/notify 经 internal/rhttp 反向依赖 internal/op，
// 在 op 里 import notify 会构成 import cycle。

// AlertRuleEvaluateAll 跑一轮全部启用规则的评估，并对该触发的发出告警。
//
// 返回真正发出的告警条数，便于测试与日志。逐条规则独立处理：一条规则出错不影响其它规则
// （规则是用户各自配的，一条写坏不该让整轮评估停摆）。
func AlertRuleEvaluateAll(ctx context.Context) int {
	now := time.Now()
	fired := 0
	for _, rule := range op.AlertRuleList(ctx) {
		if !rule.Enabled {
			continue
		}
		evals, err := op.AlertRuleDue(ctx, rule, now)
		if err != nil {
			log.Warnf("alert rule %d (%s) evaluation failed: %v", rule.ID, rule.Name, err)
			continue
		}
		for _, eval := range evals {
			if !eval.Fired {
				continue
			}
			notify.Send(ctx, notify.Event{
				Type:    "alert_rule",
				Title:   "OctoNexus 告警：" + eval.RuleName,
				Channel: eval.Channel,
				Message: op.AlertMessage(eval),
				Detail:  eval,
				At:      now,
			})
			// 先记账再计数：记账失败时不能算作"已发"，
			// 否则下一次评估会因为冷却缺失而重复发送。
			if err := op.AlertRuleMarkFired(ctx, eval, now); err != nil {
				log.Warnf("alert rule %d: record fire failed: %v", rule.ID, err)
				continue
			}
			fired++
		}
	}
	return fired
}

// alertRuleOnce 是定时任务的一轮。
func alertRuleOnce() {
	fired := AlertRuleEvaluateAll(context.Background())
	if fired > 0 {
		log.Infof("alert rules: %d alert(s) fired", fired)
	}
}

// 保证 model 包被引用（规则口径常量在 model 里，这里只做编排）。
var _ = model.AlertRule{}
