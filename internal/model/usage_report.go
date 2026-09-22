package model

import (
	"fmt"
	"time"
)

// 用量报告（吸收上游 lingyuins/octopus 的 Usage Reports）。
//
// 解决的问题：聚合网关接了几十家上游之后，"这个月花了多少、哪家最贵、哪家在掉成功率"
// 只能靠人翻面板。周期报告把这段摘要直接推到已经配好的通知渠道（Server酱/飞书/邮件…），
// 让人在不打开面板的情况下知道该不该管。
//
// 三条口径：
//   - 报告只描述**已经落库的统计**，不额外扫描日志（统计落库周期由 stats_save_interval 决定，
//     未落库的内存增量不在报告里——所以报告会明说数据截止时间，避免"数字对不上"的困惑）。
//   - 周期边界按本地时区的自然日/自然周（周一起）/自然月切分，与用户看日历的习惯一致。
//   - 同一个周期只发一次：周期键（如 2026-09-22 / 2026-W38 / 2026-09）落库，
//     重启或重复触发都不会补发第二封。
type UsageReportPeriod string

const (
	UsageReportDaily   UsageReportPeriod = "daily"
	UsageReportWeekly  UsageReportPeriod = "weekly"
	UsageReportMonthly UsageReportPeriod = "monthly"
)

// IsValidUsageReportPeriod 报告周期是否合法。
func IsValidUsageReportPeriod(value string) bool {
	switch UsageReportPeriod(value) {
	case UsageReportDaily, UsageReportWeekly, UsageReportMonthly:
		return true
	}
	return false
}

// UsageReportPeriodKey 计算某个时刻所属周期的唯一键。
//
// 这个键同时承担两个职责：决定"报告覆盖哪段时间"，以及"这个周期是否已经发过"。
// 周按 ISO 周（周一起）算，与 Go 的 ISOWeek 一致；月按自然月。
func UsageReportPeriodKey(period UsageReportPeriod, at time.Time) string {
	switch period {
	case UsageReportWeekly:
		year, week := at.ISOWeek()
		return fmt.Sprintf("%04d-W%02d", year, week)
	case UsageReportMonthly:
		return at.Format("2006-01")
	default:
		return at.Format("2006-01-02")
	}
}

// UsageReportWindow 返回该周期覆盖的时间区间 [from, to)，用于查统计。
//
// 语义：报告在周期**结束后**发（如 9 月 22 日 9 点发的是 9 月 21 日全天），
// 因此窗口是"上一个完整周期"。这样报告里的数字不会再变，避免"9 点发的报告到晚上又涨了"。
func UsageReportWindow(period UsageReportPeriod, at time.Time) (from, to time.Time, label string) {
	loc := at.Location()
	day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, loc)

	switch period {
	case UsageReportWeekly:
		// 找到今天所在周的周一，再往前推一周。
		weekday := int(day.Weekday())
		if weekday == 0 {
			weekday = 7 // 周日归到上一周的末尾
		}
		thisMonday := day.AddDate(0, 0, -(weekday - 1))
		from = thisMonday.AddDate(0, 0, -7)
		to = thisMonday
		label = fmt.Sprintf("%s ~ %s", from.Format("01-02"), to.AddDate(0, 0, -1).Format("01-02"))
	case UsageReportMonthly:
		thisMonth := time.Date(at.Year(), at.Month(), 1, 0, 0, 0, 0, loc)
		from = thisMonth.AddDate(0, -1, 0)
		to = thisMonth
		label = from.Format("2006-01")
	default:
		from = day.AddDate(0, 0, -1)
		to = day
		label = from.Format("2006-01-02")
	}
	return from, to, label
}

// UsageReportState 记录"哪个周期已经发过报告"。
//
// 单独一张表而不是塞进设置表：设置是用户可编辑的配置，发送状态是系统记账，
// 混在一起会让"导出/导入设置"把发送历史一起带走，也会让用户在面板上看到一条看不懂的键。
type UsageReportState struct {
	PeriodKey string    `json:"period_key" gorm:"primaryKey;size:32"` // 周期键（2026-09-22 / 2026-W38 / 2026-09）
	Period    string    `json:"period" gorm:"size:16"`                // 发送时的周期口径，便于排查
	SentAt    time.Time `json:"sent_at"`                              // 发送时刻
	Delivered int       `json:"delivered"`                            // 成功送达的渠道数
	Failed    int       `json:"failed"`                               // 失败的渠道数
	Summary   string    `json:"summary"`                              // 报告摘要（一行，便于事后核对发了什么）
	CreatedAt time.Time `json:"created_at"`
}
