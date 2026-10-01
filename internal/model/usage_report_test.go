package model

import (
	"testing"
	"time"
)

// 用量报告的周期口径（吸收上游 Usage Reports）。
//
// 这套用例守的是两条会让用户直接困惑的性质：
//   - **窗口是"上一个完整周期"**：报告发出来之后数字不该再变，否则用户会反复怀疑
//     "为什么昨天看的报告和今天不一样"。
//   - **周期键唯一**：同一个周期只能有一个键，否则会重复发送或漏发。

func mustParse(t *testing.T, value string) time.Time {
	t.Helper()
	at, err := time.ParseInLocation("2006-01-02 15:04", value, time.Local)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return at
}

// 日报窗口 = 昨天 00:00 到今天 00:00（不是"最近 24 小时"）。
func TestUsageReportWindowDailyIsPreviousCalendarDay(t *testing.T) {
	at := mustParse(t, "2026-09-22 09:00")
	from, to, label := UsageReportWindow(UsageReportDaily, at)

	if from.Format("2006-01-02 15:04") != "2026-09-21 00:00" {
		t.Fatalf("日报起点应为昨天 00:00: %s", from.Format(time.RFC3339))
	}
	if to.Format("2006-01-02 15:04") != "2026-09-22 00:00" {
		t.Fatalf("日报终点应为今天 00:00: %s", to.Format(time.RFC3339))
	}
	if label != "2026-09-21" {
		t.Fatalf("日报标签: %s", label)
	}
}

// 跨月边界：10 月 1 日的日报覆盖 9 月 30 日（不能因为跨月就丢掉）。
func TestUsageReportWindowDailyAcrossMonthBoundary(t *testing.T) {
	at := mustParse(t, "2026-10-01 09:00")
	from, to, _ := UsageReportWindow(UsageReportDaily, at)
	if from.Format("2006-01-02") != "2026-09-30" || to.Format("2006-01-02") != "2026-10-01" {
		t.Fatalf("跨月日报窗口错误: %s ~ %s", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
}

// 周报窗口 = 上一个完整自然周（周一到下周一），且必须真的是一周。
func TestUsageReportWindowWeeklyIsPreviousFullWeek(t *testing.T) {
	// 2026-09-22 是周二，所以上一周是 09-14(一) ~ 09-21(一)。
	at := mustParse(t, "2026-09-22 09:00")
	from, to, label := UsageReportWindow(UsageReportWeekly, at)

	if from.Weekday() != time.Monday {
		t.Fatalf("周报起点必须是周一: %s", from.Weekday())
	}
	if to.Weekday() != time.Monday {
		t.Fatalf("周报终点必须是周一: %s", to.Weekday())
	}
	if to.Sub(from) != 7*24*time.Hour {
		t.Fatalf("周报窗口必须正好 7 天: %v", to.Sub(from))
	}
	if label != "09-14 ~ 09-20" {
		t.Fatalf("周报标签: %s", label)
	}
}

// 周一当天发周报时，上一周是"再往前一周"，不能算成空窗口。
func TestUsageReportWindowWeeklyOnMonday(t *testing.T) {
	at := mustParse(t, "2026-09-21 09:00") // 周一
	from, to, _ := UsageReportWindow(UsageReportWeekly, at)
	if to.Sub(from) != 7*24*time.Hour {
		t.Fatalf("周一当天周报窗口应仍是 7 天: %v", to.Sub(from))
	}
	if to.Format("2006-01-02") != "2026-09-21" {
		t.Fatalf("周一当天周报终点应是本周一: %s", to.Format("2006-01-02"))
	}
}

// 周日归到当周（ISO 周），不能因为 Go 的 Weekday()==0 把周日算成下一周的开头。
func TestUsageReportWindowWeeklyOnSunday(t *testing.T) {
	at := mustParse(t, "2026-09-27 09:00") // 周日
	from, to, _ := UsageReportWindow(UsageReportWeekly, at)
	if to.Format("2006-01-02") != "2026-09-21" {
		t.Fatalf("周日发周报，终点应是本周一 09-21: %s", to.Format("2006-01-02"))
	}
	if to.Sub(from) != 7*24*time.Hour {
		t.Fatalf("窗口应正好 7 天: %v", to.Sub(from))
	}
}

// 月报窗口 = 上一个自然月，天数随月份变化（2 月 28 天）。
func TestUsageReportWindowMonthly(t *testing.T) {
	at := mustParse(t, "2026-03-05 09:00")
	from, to, label := UsageReportWindow(UsageReportMonthly, at)
	if from.Format("2006-01-02") != "2026-02-01" || to.Format("2006-01-02") != "2026-03-01" {
		t.Fatalf("月报窗口错误: %s ~ %s", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
	if label != "2026-02" {
		t.Fatalf("月报标签: %s", label)
	}
	// 2026 不是闰年，2 月 28 天。
	if days := int(to.Sub(from).Hours() / 24); days != 28 {
		t.Fatalf("2026-02 应有 28 天, 实得 %d", days)
	}
}

// 跨年边界：1 月的月报覆盖上一年 12 月。
func TestUsageReportWindowMonthlyAcrossYear(t *testing.T) {
	at := mustParse(t, "2027-01-10 09:00")
	from, to, _ := UsageReportWindow(UsageReportMonthly, at)
	if from.Format("2006-01-02") != "2026-12-01" || to.Format("2006-01-02") != "2027-01-01" {
		t.Fatalf("跨年月报窗口错误: %s ~ %s", from.Format("2006-01-02"), to.Format("2006-01-02"))
	}
}

// 周期键在同周期内必须稳定（同一周的任意一天算出同一个键），跨周期必须不同。
func TestUsageReportPeriodKeyStability(t *testing.T) {
	monday := mustParse(t, "2026-09-21 09:00")
	wednesday := mustParse(t, "2026-09-23 09:00")
	sunday := mustParse(t, "2026-09-27 09:00")
	nextMonday := mustParse(t, "2026-09-28 09:00")

	keyMon := UsageReportPeriodKey(UsageReportWeekly, monday)
	if keyMon != UsageReportPeriodKey(UsageReportWeekly, wednesday) {
		t.Fatalf("同周不同日应得同一周期键: %s vs %s", keyMon, UsageReportPeriodKey(UsageReportWeekly, wednesday))
	}
	if keyMon != UsageReportPeriodKey(UsageReportWeekly, sunday) {
		t.Fatalf("同周周日应得同一周期键: %s vs %s", keyMon, UsageReportPeriodKey(UsageReportWeekly, sunday))
	}
	if keyMon == UsageReportPeriodKey(UsageReportWeekly, nextMonday) {
		t.Fatalf("下一周必须换键: %s", keyMon)
	}

	// 日/月键同理。
	dayKey := UsageReportPeriodKey(UsageReportDaily, monday)
	if dayKey != "2026-09-21" {
		t.Fatalf("日键格式: %s", dayKey)
	}
	if UsageReportPeriodKey(UsageReportDaily, monday) == UsageReportPeriodKey(UsageReportDaily, wednesday) {
		t.Fatalf("不同日必须不同键")
	}
	if got := UsageReportPeriodKey(UsageReportMonthly, monday); got != "2026-09" {
		t.Fatalf("月键格式: %s", got)
	}
}

// 三种周期在同一个时刻必须得到三个互不相同的键：否则日报会顶掉月报的记账。
func TestUsageReportPeriodKeysAreDistinctAcrossPeriods(t *testing.T) {
	at := mustParse(t, "2026-09-22 09:00")
	daily := UsageReportPeriodKey(UsageReportDaily, at)
	weekly := UsageReportPeriodKey(UsageReportWeekly, at)
	monthly := UsageReportPeriodKey(UsageReportMonthly, at)
	if daily == weekly || weekly == monthly || daily == monthly {
		t.Fatalf("三种周期的键必须互不相同: %s / %s / %s", daily, weekly, monthly)
	}
}

// 周期合法性校验：只认三个口径，别的值一律拒绝（设置项校验用它挡脏数据）。
func TestIsValidUsageReportPeriod(t *testing.T) {
	for _, ok := range []string{"daily", "weekly", "monthly"} {
		if !IsValidUsageReportPeriod(ok) {
			t.Fatalf("%q 应合法", ok)
		}
	}
	for _, bad := range []string{"", "DAILY", "yearly", "hourly", " daily"} {
		if IsValidUsageReportPeriod(bad) {
			t.Fatalf("%q 应被拒绝", bad)
		}
	}
}
