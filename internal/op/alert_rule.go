package op

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 告警规则引擎的评估与写路径（吸收上游 lingyuins/octopus 的 Alerts）。
//
// 评估口径（model/alert_rule.go 的三条设计取舍在这里落地）：
//   - 逐渠道独立判定：scope=all 时把窗口内的日志按目标渠道分组，各自算各自的指标；
//   - 样本量下限：窗口内请求数低于 MinRequests 一律不判定（避免"1 次失败=100%"的噪音）；
//   - 冷却：同一规则同一渠道在 CooldownMinutes 内不重复触发。
//
// 数据源是 relay_logs（StartedAt 有索引）。不缓存聚合结果的原因：规则是分钟级低频评估，
// 一次窗口查询的代价远小于维护增量聚合的复杂度与出错面。

// AlertEvaluation 是一次评估的结果（命中或未命中都返回，便于面板解释"为什么没报"）。
type AlertEvaluation struct {
	RuleID    int     `json:"rule_id"`
	RuleName  string  `json:"rule_name"`
	Channel   string  `json:"channel"`
	Metric    string  `json:"metric"`
	Value     float64 `json:"value"`
	Threshold float64 `json:"threshold"`
	Requests  int64   `json:"requests"`
	Breached  bool    `json:"breached"` // 指标是否越过阈值
	Fired     bool    `json:"fired"`    // 是否真的发出了告警（受样本量与冷却约束）
	Reason    string  `json:"reason"`   // 未触发的原因（样本不足 / 冷却中 / 未越阈值）
}

// alertChannelStat 是窗口内单个渠道的聚合。
type alertChannelStat struct {
	Channel  string
	Total    int64
	Failed   int64
	Duration int64 // 所有请求耗时之和（毫秒），用于算平均
}

// AlertRuleList 返回全部规则（按主键，便于面板稳定展示）。
func AlertRuleList(ctx context.Context) []model.AlertRule {
	out := make([]model.AlertRule, 0)
	_ = db.GetDB().WithContext(ctx).Model(&model.AlertRule{}).Order("id").Find(&out).Error
	return out
}

// AlertFireList 返回最近的触发记录（面板用来回答"最近报过什么"）。
func AlertFireList(ctx context.Context, limit int) []model.AlertFire {
	if limit <= 0 {
		limit = 50
	}
	out := make([]model.AlertFire, 0)
	_ = db.GetDB().WithContext(ctx).Model(&model.AlertFire{}).
		Order("fired_at DESC").Limit(limit).Find(&out).Error
	return out
}

// AlertRuleGet 按主键取规则。
func AlertRuleGet(ctx context.Context, id int) (*model.AlertRule, error) {
	var item model.AlertRule
	if err := db.GetDB().WithContext(ctx).First(&item, id).Error; err != nil {
		return nil, fmt.Errorf("get alert rule %d: %w", id, err)
	}
	return &item, nil
}

// AlertRuleCreate 新建规则。
func AlertRuleCreate(ctx context.Context, req *model.AlertRuleCreateRequest) (*model.AlertRule, error) {
	if err := req.Validate(); err != nil {
		return nil, err
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	item := &model.AlertRule{
		Name:            req.Name,
		Metric:          req.Metric,
		Scope:           req.Scope,
		ScopeValue:      req.ScopeValue,
		Threshold:       req.Threshold,
		WindowMinutes:   req.WindowMinutes,
		MinRequests:     req.MinRequests,
		CooldownMinutes: req.CooldownMinutes,
		Enabled:         enabled,
	}
	if err := db.GetDB().WithContext(ctx).Create(item).Error; err != nil {
		return nil, fmt.Errorf("create alert rule: %w", err)
	}
	return item, nil
}

// AlertRuleUpdate 更新规则（只改传了的字段）。
func AlertRuleUpdate(ctx context.Context, id int, req *model.AlertRuleUpdateRequest) (*model.AlertRule, error) {
	item, err := AlertRuleGet(ctx, id)
	if err != nil {
		return nil, err
	}
	updates := map[string]any{}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" {
			return nil, fmt.Errorf("name is required")
		}
		updates["name"] = name
	}
	metric := item.Metric
	if req.Metric != nil {
		if !model.IsValidAlertMetric(string(*req.Metric)) {
			return nil, fmt.Errorf("invalid metric: must be error_rate or latency")
		}
		metric = *req.Metric
		updates["metric"] = metric
	}
	scope := item.Scope
	if req.Scope != nil {
		if !model.IsValidAlertScope(string(*req.Scope)) {
			return nil, fmt.Errorf("invalid scope: must be all or channel")
		}
		scope = *req.Scope
		updates["scope"] = scope
	}
	scopeValue := item.ScopeValue
	if req.ScopeValue != nil {
		scopeValue = strings.TrimSpace(*req.ScopeValue)
		updates["scope_value"] = scopeValue
	}
	// 校验的是**最终形态**：改了 scope 但没改 scope_value 时也要校验，
	// 否则会存出一条"scope=channel 但没指定渠道"的规则，它永远不会命中任何渠道。
	if scope == model.AlertScopeChannel && scopeValue == "" {
		return nil, fmt.Errorf("scope_value (channel name) is required when scope is channel")
	}
	threshold := item.Threshold
	if req.Threshold != nil {
		threshold = *req.Threshold
		updates["threshold"] = threshold
	}
	if threshold <= 0 {
		return nil, fmt.Errorf("threshold must be greater than 0")
	}
	if metric == model.AlertMetricErrorRate && threshold > 100 {
		return nil, fmt.Errorf("error_rate threshold must be between 0 and 100")
	}
	if req.WindowMinutes != nil {
		updates["window_minutes"] = model.ClampAlertWindow(*req.WindowMinutes)
	}
	if req.MinRequests != nil {
		updates["min_requests"] = model.ClampAlertMinRequests(*req.MinRequests)
	}
	if req.CooldownMinutes != nil {
		updates["cooldown_minutes"] = model.ClampAlertCooldown(*req.CooldownMinutes)
	}
	if req.Enabled != nil {
		updates["enabled"] = *req.Enabled
	}
	if len(updates) > 0 {
		if err := db.GetDB().WithContext(ctx).Model(&model.AlertRule{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, fmt.Errorf("update alert rule %d: %w", id, err)
		}
	}
	return AlertRuleGet(ctx, id)
}

// AlertRuleDelete 删除规则（触发历史保留，便于回溯"这条规则报过什么"）。
func AlertRuleDelete(ctx context.Context, id int) error {
	if err := db.GetDB().WithContext(ctx).Delete(&model.AlertRule{}, id).Error; err != nil {
		return fmt.Errorf("delete alert rule %d: %w", id, err)
	}
	return nil
}

// alertChannelStats 读窗口内按渠道聚合的请求数与失败数。
func alertChannelStats(ctx context.Context, since time.Time) ([]alertChannelStat, error) {
	type row struct {
		Channel  string
		Total    int64
		Failed   int64
		Duration int64
	}
	rows := make([]row, 0)
	err := db.GetDB().WithContext(ctx).Model(&model.RelayLog{}).
		Select("target_channel AS channel, COUNT(*) AS total, "+
			"SUM(CASE WHEN status <> 'success' THEN 1 ELSE 0 END) AS failed, "+
			"SUM(duration_ms) AS duration").
		Where("started_at >= ? AND target_channel <> ''", since).
		Group("target_channel").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]alertChannelStat, 0, len(rows))
	for _, r := range rows {
		out = append(out, alertChannelStat{Channel: r.Channel, Total: r.Total, Failed: r.Failed, Duration: r.Duration})
	}
	return out, nil
}

// alertValueOf 按规则口径算一个渠道的实际指标值。
func alertValueOf(rule model.AlertRule, stat alertChannelStat) float64 {
	switch rule.Metric {
	case model.AlertMetricLatency:
		if stat.Total == 0 {
			return 0
		}
		return float64(stat.Duration) / float64(stat.Total)
	default: // error_rate
		if stat.Total == 0 {
			return 0
		}
		return float64(stat.Failed) / float64(stat.Total) * 100
	}
}

// AlertRuleEvaluate 评估一条规则（只算不报），返回每个被覆盖渠道的判定结果。
//
// 面板的"试算"用它回答"这条规则现在会不会报、为什么"——把未触发的原因也带出来，
// 否则用户只能看到"没报"而不知道是样本不足还是阈值太高。
func AlertRuleEvaluate(ctx context.Context, rule model.AlertRule, now time.Time) ([]AlertEvaluation, error) {
	window := time.Duration(rule.WindowMinutes) * time.Minute
	if window <= 0 {
		window = 15 * time.Minute
	}
	stats, err := alertChannelStats(ctx, now.Add(-window))
	if err != nil {
		return nil, fmt.Errorf("load channel stats: %w", err)
	}

	out := make([]AlertEvaluation, 0, len(stats))
	for _, stat := range stats {
		if rule.Scope == model.AlertScopeChannel && stat.Channel != rule.ScopeValue {
			continue
		}
		value := alertValueOf(rule, stat)
		eval := AlertEvaluation{
			RuleID:    rule.ID,
			RuleName:  rule.Name,
			Channel:   stat.Channel,
			Metric:    string(rule.Metric),
			Value:     value,
			Threshold: rule.Threshold,
			Requests:  stat.Total,
			Breached:  value > rule.Threshold,
		}
		switch {
		case !eval.Breached:
			eval.Reason = "within_threshold"
		case stat.Total < int64(rule.MinRequests):
			// 样本不足不判定：一次偶然失败会被算成 100% 错误率，那是最常见的告警噪音源。
			eval.Reason = "insufficient_samples"
		default:
			eval.Fired = true
			eval.Reason = "breached"
		}
		out = append(out, eval)
	}
	return out, nil
}

// AlertRuleDue 返回此刻应当真正触发的评估结果（已扣除冷却）。
//
// 冷却按"规则 + 渠道"粒度：一个坏渠道不该把好渠道的告警名额吃掉。
func AlertRuleDue(ctx context.Context, rule model.AlertRule, now time.Time) ([]AlertEvaluation, error) {
	evals, err := AlertRuleEvaluate(ctx, rule, now)
	if err != nil {
		return nil, err
	}
	out := make([]AlertEvaluation, 0, len(evals))
	for _, eval := range evals {
		if !eval.Fired {
			out = append(out, eval)
			continue
		}
		if AlertRuleInCooldown(ctx, rule, eval.Channel, now) {
			eval.Fired = false
			eval.Reason = "cooldown"
		}
		out = append(out, eval)
	}
	return out, nil
}

// AlertRuleInCooldown 判断该规则该渠道是否还在冷却期内。
func AlertRuleInCooldown(ctx context.Context, rule model.AlertRule, channel string, now time.Time) bool {
	if rule.CooldownMinutes <= 0 {
		return false
	}
	var last model.AlertFire
	err := db.GetDB().WithContext(ctx).Model(&model.AlertFire{}).
		Where("rule_id = ? AND channel = ?", rule.ID, channel).
		Order("fired_at DESC").First(&last).Error
	if err != nil {
		return false // 没报过 → 不在冷却
	}
	return now.Sub(last.FiredAt) < time.Duration(rule.CooldownMinutes)*time.Minute
}

// AlertRuleMarkFired 记录一次触发。
func AlertRuleMarkFired(ctx context.Context, eval AlertEvaluation, now time.Time) error {
	fire := model.AlertFire{
		RuleID:    eval.RuleID,
		RuleName:  eval.RuleName,
		Channel:   eval.Channel,
		Metric:    eval.Metric,
		Value:     eval.Value,
		Threshold: eval.Threshold,
		Requests:  eval.Requests,
		Message:   AlertMessage(eval),
		FiredAt:   now,
	}
	if err := db.GetDB().WithContext(ctx).Create(&fire).Error; err != nil {
		return fmt.Errorf("record alert fire: %w", err)
	}
	// LastFiredAt 只是给面板看的"最近一次"，权威判据是 AlertFire 表。
	if err := db.GetDB().WithContext(ctx).Model(&model.AlertRule{}).
		Where("id = ?", eval.RuleID).Update("last_fired_at", now.Unix()).Error; err != nil {
		return fmt.Errorf("update alert rule last fired: %w", err)
	}
	return nil
}

// AlertRulePreviewAll 预演一轮全部启用规则的评估：算、但不发送、不记账。
//
// 为什么需要它：定时评估与「立即检查」都会**真的投递**。用户调阈值时最想知道的是
// "按现在这套规则，此刻会报几条、报的是哪些渠道"——如果看这个答案的代价是先把手机刷一遍，
// 那就没人敢调阈值了。预演与真发的判定口径完全一致（同样走 AlertRuleDue，含冷却），
// 差别只在最后一步不投递。
func AlertRulePreviewAll(ctx context.Context, now time.Time) []AlertEvaluation {
	out := make([]AlertEvaluation, 0)
	for _, rule := range AlertRuleList(ctx) {
		if !rule.Enabled {
			continue
		}
		evals, err := AlertRuleDue(ctx, rule, now)
		if err != nil {
			continue
		}
		for _, eval := range evals {
			if eval.Fired {
				out = append(out, eval)
			}
		}
	}
	return out
}

// AlertMessage 生成人类可读的告警正文。
func AlertMessage(eval AlertEvaluation) string {
	switch eval.Metric {
	case string(model.AlertMetricLatency):
		return fmt.Sprintf("渠道 %s 最近平均耗时 %.0fms，超过阈值 %.0fms（窗口内 %d 次请求）",
			eval.Channel, eval.Value, eval.Threshold, eval.Requests)
	default:
		return fmt.Sprintf("渠道 %s 最近错误率 %.1f%%，超过阈值 %.1f%%（窗口内 %d 次请求）",
			eval.Channel, eval.Value, eval.Threshold, eval.Requests)
	}
}
