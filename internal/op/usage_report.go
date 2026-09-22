package op

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/bestruirui/octopus/internal/db"
	"github.com/bestruirui/octopus/internal/model"
)

// 用量报告（吸收上游 lingyuins/octopus 的 Usage Reports）。
//
// 口径见 model/usage_report.go 的说明：报告只描述**已落库的统计**，覆盖**上一个完整周期**，
// 同一个周期只发一次。三条都是为了让报告里的数字"看过就不再变"，否则用户会反复怀疑
// "为什么昨天看的报告和今天不一样"。

// UsageReportTop 是报告里的排行项。
type UsageReportTop struct {
	Name    string  `json:"name"`
	Count   int64   `json:"count"`
	Cost    float64 `json:"cost"`
	Success int64   `json:"success"`
	Failed  int64   `json:"failed"`
}

// UsageReport 是一次报告的完整内容（也是面板与接口的返回体）。
type UsageReport struct {
	Period      string           `json:"period"`
	PeriodKey   string           `json:"period_key"`
	Label       string           `json:"label"`
	From        string           `json:"from"`
	To          string           `json:"to"`
	Requests    int64            `json:"requests"`
	Success     int64            `json:"success"`
	Failed      int64            `json:"failed"`
	SuccessRate float64          `json:"success_rate"`
	InputToken  int64            `json:"input_token"`
	OutputToken int64            `json:"output_token"`
	Cost        float64          `json:"cost"`
	WaitTimeMs  int64            `json:"wait_time_ms"`
	TopModels   []UsageReportTop `json:"top_models"`
	TopChannels []UsageReportTop `json:"top_channels"`
	// DetailRequests 是"明细排行实际覆盖的请求数"。
	//
	// 为什么需要它：总计来自逐日统计（stats_dailies，保留久），排行来自小时桶
	// （usage_hourlies，有独立保留期且可能只从某个版本才开始积累）。两者覆盖范围不一致时，
	// 报告会出现"总数一万多、排行只有 3 次"这种看起来像 bug 的矛盾。
	// 与其让用户去猜，不如把覆盖量直接写进正文。
	DetailRequests int64   `json:"detail_requests"`
	Balance        float64 `json:"balance"` // 发送时的总余额（快照，不是周期内的变化）
	// BalanceKnown 区分"余额真的是 0"与"还没扫到任何渠道的余额"。
	//
	// 项目既有口径（T-balance-001）：未读到的渠道绝不进总额——把"还没扫到"当 0
	// 会让人看成"没钱了"。报告里同理：没扫到时必须说"未知"，而不是印一个 0.00。
	BalanceKnown bool   `json:"balance_known"`
	Currency     string `json:"currency"`
	DataThrough  string `json:"data_through"` // 数据截止时间（统计落库是周期性的，如实说明）
	Empty        bool   `json:"empty"`        // 周期内没有任何请求
	Text         string `json:"text"`         // 给通知渠道的纯文本正文
}

// UsageReportBuild 组装一份报告（不发送）。at 为"现在"，窗口由它推出的上一个完整周期决定。
func UsageReportBuild(ctx context.Context, period model.UsageReportPeriod, at time.Time) (UsageReport, error) {
	from, to, label := model.UsageReportWindow(period, at)
	report := UsageReport{
		Period:      string(period),
		PeriodKey:   model.UsageReportPeriodKey(period, at),
		Label:       label,
		From:        from.Format(time.RFC3339),
		To:          to.Format(time.RFC3339),
		DataThrough: time.Now().Format(time.RFC3339),
	}

	// 周期内的逐日统计（stats_dailies.date 的格式是 **20060102 无分隔符**，
	// 与 stats_daily 表里实际写入的口径一致——用 2006-01-02 去比会让每一行都被跳过，
	// 报告恒为空且不报错，是本轮实际踩过的坑）。
	rows, err := StatsGetDaily(ctx, from.Format("20060102"))
	if err != nil {
		return report, fmt.Errorf("load daily stats: %w", err)
	}
	fromDay := from.Format("20060102")
	toDay := to.Format("20060102")
	for _, row := range rows {
		if row.Date < fromDay || row.Date >= toDay {
			continue
		}
		report.Requests += row.RequestSuccess + row.RequestFailed
		report.Success += row.RequestSuccess
		report.Failed += row.RequestFailed
		report.InputToken += row.InputToken
		report.OutputToken += row.OutputToken
		report.Cost += row.InputCost + row.OutputCost
		report.WaitTimeMs += row.WaitTime
	}

	// 明细排行直接查小时桶表（usage_hourly 的 hour 是 2006010215 格式，正好按字符串区间筛）。
	// 不用 op.UsageQuery 的原因：它只接受 24h/7d/30d 三个固定窗口，而报告的窗口是"上一个自然周期"。
	modelTotals := map[string]*UsageReportTop{}
	channelTotals := map[string]*UsageReportTop{}
	if detail, err := usageHourlyInWindow(ctx, from, to); err == nil {
		for _, row := range detail {
			report.DetailRequests += row.RequestSuccess + row.RequestFailed
			if row.ModelName != "" {
				addUsageTotal(modelTotals, row.ModelName, row.StatsMetrics)
			}
			if row.ChannelName != "" {
				addUsageTotal(channelTotals, row.ChannelName, row.StatsMetrics)
			}
		}
	}
	report.TopModels = topUsage(modelTotals, 5)
	report.TopChannels = topUsage(channelTotals, 5)

	// 余额是"此刻的快照"，不是周期内的变化量——单独取，并在正文里说明。
	summary := BalanceSummaryGet()
	report.Balance = summary.Total
	report.BalanceKnown = summary.KnownChannels > 0
	report.Currency = summary.Currency
	if report.Currency == "" {
		report.Currency = "USD"
	}

	report.Empty = report.Requests == 0
	if report.Requests > 0 {
		report.SuccessRate = float64(report.Success) / float64(report.Requests) * 100
	}
	report.Text = renderUsageReport(report)
	return report, nil
}

// usageHourlyInWindow 读窗口内的小时桶（含未落库的内存桶，与面板看到的一致）。
func usageHourlyInWindow(ctx context.Context, from, to time.Time) ([]model.UsageHourly, error) {
	out := make([]model.UsageHourly, 0)
	err := db.GetDB().WithContext(ctx).Model(&model.UsageHourly{}).
		Where("hour >= ? AND hour < ?", from.Format("2006010215"), to.Format("2006010215")).
		Find(&out).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}

func addUsageTotal(bucket map[string]*UsageReportTop, name string, m model.StatsMetrics) {
	item, ok := bucket[name]
	if !ok {
		item = &UsageReportTop{Name: name}
		bucket[name] = item
	}
	item.Count += m.RequestSuccess + m.RequestFailed
	item.Success += m.RequestSuccess
	item.Failed += m.RequestFailed
	item.Cost += m.InputCost + m.OutputCost
}

// topUsage 按请求数取前 n 项（并列时按费用倒序，让"贵且忙"的排在前面）。
func topUsage(bucket map[string]*UsageReportTop, n int) []UsageReportTop {
	out := make([]UsageReportTop, 0, len(bucket))
	for _, item := range bucket {
		out = append(out, *item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// renderUsageReport 把报告渲染成通知渠道用的纯文本。
//
// 排版按"手机上能一眼看完"来定：先给结论（请求数/费用/成功率），再给明细；
// 空周期单独说一句，而不是发一堆 0 让人以为坏了。
func renderUsageReport(r UsageReport) string {
	var b strings.Builder
	periodName := map[string]string{"daily": "日报", "weekly": "周报", "monthly": "月报"}[r.Period]
	if periodName == "" {
		periodName = "报告"
	}
	fmt.Fprintf(&b, "OctoNexus 用量%s（%s）\n", periodName, r.Label)
	if r.Empty {
		b.WriteString("本周期没有请求记录。\n")
	} else {
		fmt.Fprintf(&b, "请求 %d 次（成功 %d / 失败 %d，成功率 %.1f%%）\n",
			r.Requests, r.Success, r.Failed, r.SuccessRate)
		fmt.Fprintf(&b, "词元 输入 %s / 输出 %s\n", humanCount(r.InputToken), humanCount(r.OutputToken))
		fmt.Fprintf(&b, "费用 %.4f %s\n", r.Cost, r.Currency)
		if r.WaitTimeMs > 0 && r.Requests > 0 {
			fmt.Fprintf(&b, "平均耗时 %.2fs\n", float64(r.WaitTimeMs)/float64(r.Requests)/1000)
		}
		if len(r.TopModels) > 0 {
			b.WriteString("模型 Top:\n")
			for _, item := range r.TopModels {
				fmt.Fprintf(&b, "  %s · %d 次 · %.4f\n", item.Name, item.Count, item.Cost)
			}
		}
		if len(r.TopChannels) > 0 {
			b.WriteString("渠道 Top:\n")
			for _, item := range r.TopChannels {
				fmt.Fprintf(&b, "  %s · %d 次 · 成功率 %s\n", item.Name, item.Count, rateOf(item))
			}
		}
		// 覆盖范围不一致时说清楚，否则"总数一万多、排行只有 3 次"会被当成 bug。
		if r.DetailRequests < r.Requests {
			fmt.Fprintf(&b, "（排行按小时明细桶统计，覆盖 %d 次；小时桶保留期与总计不同，故可能少于总数）\n",
				r.DetailRequests)
		}
	}
	if r.BalanceKnown {
		fmt.Fprintf(&b, "当前总余额 %.2f %s\n", r.Balance, r.Currency)
	} else {
		// 没扫到任何渠道的余额时印 0.00 会被看成"没钱了"——如实说未知（T-balance-001 口径）。
		b.WriteString("当前总余额：未知（还没有渠道读到余额）\n")
	}
	return b.String()
}

func rateOf(item UsageReportTop) string {
	if item.Count == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", float64(item.Success)/float64(item.Count)*100)
}

// humanCount 把大数字缩成 K/M/B（通知渠道里一眼可读，不必数零）。
func humanCount(n int64) string {
	switch {
	case n >= 1_000_000_000:
		return fmt.Sprintf("%.2fB", float64(n)/1_000_000_000)
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.2fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// UsageReportDue 判断此刻是否该发报告，并返回周期与周期键。
//
// 两个条件同时成立才发：当前整点等于配置的时刻，且这个周期还没发过。
// 前者保证"一天只在一个时刻发"，后者保证"重启/重复触发不补发"。
func UsageReportDue(ctx context.Context, now time.Time) (model.UsageReportPeriod, string, bool) {
	enabled, err := SettingGetBool(model.SettingKeyUsageReportEnabled)
	if err != nil || !enabled {
		return "", "", false
	}
	periodRaw, err := SettingGetString(model.SettingKeyUsageReportPeriod)
	if err != nil || !model.IsValidUsageReportPeriod(periodRaw) {
		return "", "", false
	}
	hour, err := SettingGetInt(model.SettingKeyUsageReportHour)
	if err != nil || hour < 0 || hour > 23 {
		return "", "", false
	}
	if now.Hour() != hour {
		return "", "", false
	}
	period := model.UsageReportPeriod(periodRaw)
	key := model.UsageReportPeriodKey(period, now)
	if UsageReportSent(ctx, key) {
		return "", "", false
	}
	return period, key, true
}

// UsageReportSent 该周期键是否已经发过。
//
// 查库出错时返回 true（当作"已发过"）：查询失败通常意味着表还没就绪，
// 此时发送也写不进记账、下一轮会再发一次，用户就会收到重复报告。
// 宁可漏发一轮（用户可在面板上手动补发），也不要重复打扰。
func UsageReportSent(ctx context.Context, periodKey string) bool {
	if periodKey == "" {
		return false
	}
	var count int64
	err := db.GetDB().WithContext(ctx).Model(&model.UsageReportState{}).
		Where("period_key = ?", periodKey).Count(&count).Error
	if err != nil {
		return true
	}
	return count > 0
}

// UsageReportSend 组装报告并记账（**不负责投递**）。
//
// 为什么投递不在这里：internal/notify 经 internal/rhttp 反向依赖 internal/op，
// 在 op 里 import notify 会构成 import cycle。投递由 internal/task 完成
// （它同时依赖 op 与 notify），这也是本项目既有的分层口径。
//
// 记账与投递的先后由调用方决定：调用方拿到结果后把 delivered/failed 回填给 UsageReportMarkSent。
func UsageReportPrepare(ctx context.Context, period model.UsageReportPeriod, at time.Time) (UsageReport, error) {
	return UsageReportBuild(ctx, period, at)
}

// UsageReportMarkSent 记录"这个周期已发过"，保证同一周期不补发。
func UsageReportMarkSent(ctx context.Context, report UsageReport, delivered, failed int, at time.Time) error {
	state := model.UsageReportState{
		PeriodKey: report.PeriodKey,
		Period:    report.Period,
		SentAt:    at,
		Delivered: delivered,
		Failed:    failed,
		Summary:   firstLine(report.Text),
		CreatedAt: time.Now(),
	}
	if err := db.GetDB().WithContext(ctx).Create(&state).Error; err != nil {
		// 记账失败要如实上报：否则下一轮会再发一次，用户收到重复报告。
		return fmt.Errorf("record usage report state: %w", err)
	}
	return nil
}

// UsageReportHistory 返回最近的发送记录（面板用来回答"上次什么时候发的"）。
func UsageReportHistory(ctx context.Context, limit int) []model.UsageReportState {
	if limit <= 0 {
		limit = 20
	}
	out := make([]model.UsageReportState, 0)
	_ = db.GetDB().WithContext(ctx).Model(&model.UsageReportState{}).
		Order("sent_at DESC").Limit(limit).Find(&out).Error
	return out
}

func firstLine(text string) string {
	if idx := strings.IndexByte(text, '\n'); idx >= 0 {
		return text[:idx]
	}
	return text
}
