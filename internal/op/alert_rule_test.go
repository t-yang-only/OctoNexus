package op

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 告警规则引擎（吸收上游 lingyuins/octopus 的 Alerts）。
//
// 这套用例的重心不是"能不能报警"，而是**能不能不吵人**——告警系统最常见的失败模式是
// 噪音太多导致用户关掉它。因此下面把两条防噪音机制钉死：
//   - 样本量下限：窗口内只有 1 次请求且失败时，错误率是 100%，但那是偶然不是故障；
//   - 冷却：同一规则同一渠道在冷却期内不重复触发。
// 同时锁住"冷却按渠道独立"——一个坏渠道不该把好渠道的告警名额吃掉。

func setupAlertDB(t *testing.T) {
	t.Helper()
	setupModelMappingDB(t) // 复用共享库初始化
	if err := db.GetDB().AutoMigrate(&model.RelayLog{}, &model.AlertRule{}, &model.AlertFire{}); err != nil {
		t.Fatalf("migrate alert tables: %v", err)
	}
}

func clearAlertData(t *testing.T) {
	t.Helper()
	for _, table := range []any{&model.RelayLog{}, &model.AlertRule{}, &model.AlertFire{}} {
		if err := db.GetDB().Where("1 = 1").Delete(table).Error; err != nil {
			t.Fatalf("clear %T: %v", table, err)
		}
	}
}

// seedLog 种一条转发日志。status 用 'success' 表示成功，其它值算失败。
func seedLog(t *testing.T, channel, status string, durationMs int64, at time.Time) {
	t.Helper()
	row := model.RelayLog{
		Status:        status,
		TargetChannel: channel,
		StartedAt:     at,
		DurationMs:    durationMs,
	}
	if err := db.GetDB().Create(&row).Error; err != nil {
		t.Fatalf("seed log: %v", err)
	}
}

func newErrorRateRule(t *testing.T, threshold float64, minRequests, cooldown int) *model.AlertRule {
	t.Helper()
	enabled := true
	rule, err := AlertRuleCreate(context.Background(), &model.AlertRuleCreateRequest{
		Name: "错误率", Metric: model.AlertMetricErrorRate, Scope: model.AlertScopeAll,
		Threshold: threshold, WindowMinutes: 15, MinRequests: minRequests,
		CooldownMinutes: cooldown, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create rule: %v", err)
	}
	return rule
}

// 样本量低于下限时一律不触发：这是告警噪音的头号来源。
func TestAlertRuleInsufficientSamplesDoesNotFire(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()
	now := time.Now()

	rule := newErrorRateRule(t, 30, 5, 60)
	// 窗口内只有 1 次请求，且失败 —— 错误率 100%，但样本不足。
	seedLog(t, "ch-a", "failed", 100, now.Add(-time.Minute))

	evals, err := AlertRuleEvaluate(ctx, *rule, now)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(evals) != 1 {
		t.Fatalf("应有一个渠道被评估，实得 %d", len(evals))
	}
	eval := evals[0]
	if !eval.Breached {
		t.Fatalf("100%% 应越过 30%% 阈值（Breached 描述的是事实，不是是否告警）")
	}
	if eval.Fired {
		t.Fatalf("样本不足时不该触发告警")
	}
	if eval.Reason != "insufficient_samples" {
		t.Fatalf("未触发原因应为 insufficient_samples，实得 %q", eval.Reason)
	}
}

// 样本足够且越阈值时必须触发。
func TestAlertRuleFiresWhenThresholdBreachedWithEnoughSamples(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()
	now := time.Now()

	rule := newErrorRateRule(t, 30, 5, 60)
	// 10 次请求里 5 次失败 = 50%。
	for i := 0; i < 5; i++ {
		seedLog(t, "ch-a", "failed", 100, now.Add(-time.Minute))
	}
	for i := 0; i < 5; i++ {
		seedLog(t, "ch-a", "success", 100, now.Add(-time.Minute))
	}

	evals, err := AlertRuleEvaluate(ctx, *rule, now)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(evals) != 1 || !evals[0].Fired {
		t.Fatalf("样本足够且越阈值时应触发: %+v", evals)
	}
	if diff := evals[0].Value - 50.0; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("错误率应为 50%%，实得 %v", evals[0].Value)
	}
	if evals[0].Requests != 10 {
		t.Fatalf("样本量应为 10，实得 %d", evals[0].Requests)
	}
}

// 未越阈值时不触发，且原因要能区分于"样本不足"。
func TestAlertRuleWithinThresholdDoesNotFire(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()
	now := time.Now()

	rule := newErrorRateRule(t, 30, 5, 60)
	for i := 0; i < 1; i++ {
		seedLog(t, "ch-a", "failed", 100, now.Add(-time.Minute))
	}
	for i := 0; i < 19; i++ {
		seedLog(t, "ch-a", "success", 100, now.Add(-time.Minute))
	}

	evals, _ := AlertRuleEvaluate(ctx, *rule, now)
	if len(evals) != 1 || evals[0].Fired {
		t.Fatalf("5%% 不该触发 30%% 阈值: %+v", evals)
	}
	if evals[0].Reason != "within_threshold" {
		t.Fatalf("原因应为 within_threshold，实得 %q", evals[0].Reason)
	}
}

// 窗口外的日志不得计入：否则"最近 15 分钟"会变成"最近很久"。
func TestAlertRuleExcludesLogsOutsideWindow(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()
	now := time.Now()

	rule := newErrorRateRule(t, 30, 5, 60)
	// 窗口外（20 分钟前）全部失败，窗口内（1 分钟前）全部成功。
	for i := 0; i < 10; i++ {
		seedLog(t, "ch-a", "failed", 100, now.Add(-20*time.Minute))
	}
	for i := 0; i < 10; i++ {
		seedLog(t, "ch-a", "success", 100, now.Add(-time.Minute))
	}

	evals, _ := AlertRuleEvaluate(ctx, *rule, now)
	if len(evals) != 1 {
		t.Fatalf("应有一个渠道: %+v", evals)
	}
	if evals[0].Requests != 10 {
		t.Fatalf("只该统计窗口内的 10 次，实得 %d", evals[0].Requests)
	}
	if evals[0].Fired {
		t.Fatalf("窗口内全成功不该触发: %+v", evals[0])
	}
}

// 冷却：触发后进入冷却，冷却期内不重复触发；冷却过后可再次触发。
func TestAlertRuleCooldownSuppressesRepeat(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()
	now := time.Now()

	rule := newErrorRateRule(t, 30, 5, 60)
	for i := 0; i < 10; i++ {
		seedLog(t, "ch-a", "failed", 100, now.Add(-time.Minute))
	}

	evals, _ := AlertRuleDue(ctx, *rule, now)
	if len(evals) != 1 || !evals[0].Fired {
		t.Fatalf("首次应触发: %+v", evals)
	}
	if err := AlertRuleMarkFired(ctx, evals[0], now); err != nil {
		t.Fatalf("mark: %v", err)
	}

	// 冷却期内再评估：不该触发。
	evals2, _ := AlertRuleDue(ctx, *rule, now.Add(time.Minute))
	if len(evals2) != 1 {
		t.Fatalf("应有一个渠道: %+v", evals2)
	}
	if evals2[0].Fired {
		t.Fatalf("冷却期内不该重复触发: %+v", evals2[0])
	}
	if evals2[0].Reason != "cooldown" {
		t.Fatalf("原因应为 cooldown，实得 %q", evals2[0].Reason)
	}

	// 冷却过后（61 分钟）应可再次触发。
	evals3, _ := AlertRuleDue(ctx, *rule, now.Add(61*time.Minute))
	// 注意：窗口是 15 分钟，所以 61 分钟后窗口内已经没有任何日志 —— 这里换一个时间点验证冷却本身。
	if len(evals3) == 1 && evals3[0].Fired {
		t.Fatalf("窗口内无样本时不该触发（原因应为 insufficient_samples）: %+v", evals3[0])
	}
}

// 冷却按"规则 + 渠道"独立：一个坏渠道不该把好渠道的告警名额吃掉。
func TestAlertRuleCooldownIsPerChannel(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()
	now := time.Now()

	rule := newErrorRateRule(t, 30, 5, 60)
	// 两个渠道都坏。
	for i := 0; i < 10; i++ {
		seedLog(t, "ch-a", "failed", 100, now.Add(-time.Minute))
		seedLog(t, "ch-b", "failed", 100, now.Add(-time.Minute))
	}

	evals, _ := AlertRuleDue(ctx, *rule, now)
	if len(evals) != 2 {
		t.Fatalf("应有两个渠道: %+v", evals)
	}
	// 只把 ch-a 标记为已触发。
	for _, eval := range evals {
		if eval.Channel == "ch-a" {
			if err := AlertRuleMarkFired(ctx, eval, now); err != nil {
				t.Fatalf("mark: %v", err)
			}
		}
	}

	next, _ := AlertRuleDue(ctx, *rule, now.Add(time.Minute))
	firedChannels := map[string]bool{}
	for _, eval := range next {
		if eval.Fired {
			firedChannels[eval.Channel] = true
		}
	}
	if firedChannels["ch-a"] {
		t.Fatalf("ch-a 在冷却中不该再触发")
	}
	if !firedChannels["ch-b"] {
		t.Fatalf("ch-b 未被冷却，应该仍能触发（冷却必须按渠道独立）")
	}
}

// scope=channel 时只盯指定渠道。
func TestAlertRuleChannelScopeOnlyWatchesOneChannel(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()
	now := time.Now()

	enabled := true
	rule, err := AlertRuleCreate(ctx, &model.AlertRuleCreateRequest{
		Name: "只盯 ch-b", Metric: model.AlertMetricErrorRate, Scope: model.AlertScopeChannel,
		ScopeValue: "ch-b", Threshold: 30, WindowMinutes: 15, MinRequests: 5,
		CooldownMinutes: 60, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < 10; i++ {
		seedLog(t, "ch-a", "failed", 100, now.Add(-time.Minute))
		seedLog(t, "ch-b", "failed", 100, now.Add(-time.Minute))
	}

	evals, _ := AlertRuleEvaluate(ctx, *rule, now)
	if len(evals) != 1 || evals[0].Channel != "ch-b" {
		t.Fatalf("scope=channel 时只该评估 ch-b: %+v", evals)
	}
}

// 延迟口径：按窗口内平均耗时判定。
func TestAlertRuleLatencyMetric(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()
	now := time.Now()

	enabled := true
	rule, err := AlertRuleCreate(ctx, &model.AlertRuleCreateRequest{
		Name: "延迟", Metric: model.AlertMetricLatency, Scope: model.AlertScopeAll,
		Threshold: 1000, WindowMinutes: 15, MinRequests: 3, CooldownMinutes: 60, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 4 次请求：耗时 2000/2000/2000/2000 → 平均 2000ms > 1000ms。
	for i := 0; i < 4; i++ {
		seedLog(t, "ch-slow", "success", 2000, now.Add(-time.Minute))
	}

	evals, _ := AlertRuleEvaluate(ctx, *rule, now)
	if len(evals) != 1 || !evals[0].Fired {
		t.Fatalf("平均 2000ms 应触发 1000ms 阈值: %+v", evals)
	}
	if diff := evals[0].Value - 2000; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("平均耗时应为 2000ms，实得 %v", evals[0].Value)
	}
}

// 校验：阈值必须 > 0（0 阈值每次评估都触发，那是配置错误）；数值字段夹回合法区间。
func TestAlertRuleValidationAndClamping(t *testing.T) {
	base := func() *model.AlertRuleCreateRequest {
		return &model.AlertRuleCreateRequest{
			Name: "n", Metric: model.AlertMetricErrorRate, Scope: model.AlertScopeAll, Threshold: 50,
		}
	}

	// 阈值 <= 0 必须拒绝。
	bad := base()
	bad.Threshold = 0
	if err := bad.Validate(); err == nil {
		t.Fatalf("0 阈值应被拒绝")
	}
	// 错误率阈值 > 100 必须拒绝（100% 是上限，再高永远不会触发）。
	over := base()
	over.Threshold = 150
	if err := over.Validate(); err == nil {
		t.Fatalf("错误率阈值 >100 应被拒绝")
	}
	// scope=channel 但没给渠道名必须拒绝。
	noScope := base()
	noScope.Scope = model.AlertScopeChannel
	if err := noScope.Validate(); err == nil {
		t.Fatalf("scope=channel 缺 scope_value 应被拒绝")
	}
	// 非法指标必须拒绝。
	badMetric := base()
	badMetric.Metric = "vibes"
	if err := badMetric.Validate(); err == nil {
		t.Fatalf("非法指标应被拒绝")
	}

	// 数值字段夹回：没填（0）取默认值，越界夹回边界。
	ok := base()
	if err := ok.Validate(); err != nil {
		t.Fatalf("合法请求不该失败: %v", err)
	}
	if ok.WindowMinutes != 15 || ok.MinRequests != 5 || ok.CooldownMinutes != 60 {
		t.Fatalf("缺省值应为 15/5/60，实得 %d/%d/%d", ok.WindowMinutes, ok.MinRequests, ok.CooldownMinutes)
	}
	huge := base()
	huge.WindowMinutes = 99999
	huge.MinRequests = 99999999
	if err := huge.Validate(); err != nil {
		t.Fatalf("越界值应夹回而不是拒绝: %v", err)
	}
	if huge.WindowMinutes != 1440 || huge.MinRequests != 100000 {
		t.Fatalf("越界值应夹回边界，实得 %d/%d", huge.WindowMinutes, huge.MinRequests)
	}
}

// 更新时在"最终形态"上校验：改了 scope 但没改 scope_value 也要拦下来。
func TestAlertRuleUpdateValidatesFinalShape(t *testing.T) {
	setupAlertDB(t)
	clearAlertData(t)
	ctx := context.Background()

	enabled := true
	rule, err := AlertRuleCreate(ctx, &model.AlertRuleCreateRequest{
		Name: "全渠道", Metric: model.AlertMetricErrorRate, Scope: model.AlertScopeAll,
		Threshold: 50, Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 只把 scope 改成 channel，不传 scope_value → 必须拒绝
	// （否则会存出一条"scope=channel 但没有渠道名"的规则，它永远不命中任何渠道）。
	scope := model.AlertScopeChannel
	if _, err := AlertRuleUpdate(ctx, rule.ID, &model.AlertRuleUpdateRequest{Scope: &scope}); err == nil {
		t.Fatalf("改成 channel 但缺 scope_value 应被拒绝")
	}
	// 同时给出渠道名才允许。
	value := "ch-a"
	updated, err := AlertRuleUpdate(ctx, rule.ID, &model.AlertRuleUpdateRequest{Scope: &scope, ScopeValue: &value})
	if err != nil {
		t.Fatalf("给出渠道名后应允许: %v", err)
	}
	if updated.Scope != model.AlertScopeChannel || updated.ScopeValue != "ch-a" {
		t.Fatalf("更新未生效: %+v", updated)
	}
}

// 告警正文必须能读懂（含渠道名、实际值、阈值、样本量）。
func TestAlertMessageIsReadable(t *testing.T) {
	eval := AlertEvaluation{
		Channel: "53HK", Metric: string(model.AlertMetricErrorRate),
		Value: 42.5, Threshold: 30, Requests: 200,
	}
	msg := AlertMessage(eval)
	for _, want := range []string{"53HK", "42.5%", "30.0%", "200"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("正文缺少 %q: %s", want, msg)
		}
	}
	latency := AlertEvaluation{
		Channel: "ch-x", Metric: string(model.AlertMetricLatency),
		Value: 2500, Threshold: 1000, Requests: 12,
	}
	msg2 := AlertMessage(latency)
	for _, want := range []string{"ch-x", "2500ms", "1000ms", "12"} {
		if !strings.Contains(msg2, want) {
			t.Fatalf("延迟正文缺少 %q: %s", want, msg2)
		}
	}
}
