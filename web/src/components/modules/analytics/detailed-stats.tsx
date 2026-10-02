import { useQuery } from '@tanstack/react-query';
import dayjs from 'dayjs';
import { CalendarCheck, CalendarRange, Flame, MessageSquare, ServerCog, Sigma } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { statsDailyQueryOptions } from '@/api/queries';
import { useStatsTotal } from '@/api/stats';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import type { StatsDaily } from '@/api/stats';

// streaks 从「有请求的日期」列表算当前连续天数与最长连续天数。
//
// 为什么单独抽出来：连续天数是**跨行**推导出来的（要按日期排序后看相邻间隔），
// 塞进渲染里会变成一段没人敢改的循环。抽成具名函数后这段口径是可被读、可被改的。
//
// **不导出**：本文件只该导出组件，多导出一个函数会让整块组件的热更新失效
//（eslint 的 react-refresh/only-export-components）。真需要单测时按 FilterBar → filtered.ts
// 的先例挪到独立文件，而不是就地导出。
//
// 三条口径写清楚（否则读数和直觉对不上时会以为是 bug）：
//   · 只算**真的发过请求**的日子；日表里零请求的行不算活跃日；
//   · 「当前连续」允许断在昨天 —— 今天还没发请求不代表连续被打破
//     （否则每天凌晨打开面板都会看到连续天数归零）；
//   · 断两天及以上则当前连续为 0，而不是 1（只有今天一天不算「连续」）。
function streaks(rows: StatsDaily[]): { current: number; longest: number } {
    const dates = rows
        .filter((row) => (row.request_success ?? 0) + (row.request_failed ?? 0) > 0)
        .map((row) => row.date)
        .sort();
    if (dates.length === 0) return { current: 0, longest: 0 };

    let longest = 1;
    let run = 1;
    for (let i = 1; i < dates.length; i += 1) {
        const gap = dayjs(dates[i]).diff(dayjs(dates[i - 1]), 'day');
        run = gap === 1 ? run + 1 : 1;
        if (run > longest) longest = run;
    }

    const lastActive = dayjs(dates[dates.length - 1]);
    const daysSinceLast = dayjs().startOf('day').diff(lastActive.startOf('day'), 'day');
    const current = daysSinceLast <= 1 ? run : 0;
    return { current, longest };
}

// DetailedStats 是分析页顶部的「详细统计」总览：把散在各处的规模数字汇成一排。
//
// 与主页的 Total 卡片分工不同：那边是**本次窗口**的读数（会随数据滚动），
// 这里是**累计口径**（从装上那天算起），回答「这套系统一共服务了多少」。
// 两者数字必然不同，且都对 —— 所以标题里写明是累计，避免被当成滚动读数。
export function DetailedStats() {
    const t = useTranslations('analytics.detailed');
    const { data: total } = useStatsTotal();
    // 原始日行：与格式化版本共用同一个 queryKey，因此不会多发请求。
    // 这里要原始值是为了做跨行推导（连续天数），格式化后的「万/亿」没法比较。
    const { data: daily } = useQuery(statsDailyQueryOptions);

    const rows = (daily?.items ?? []) as StatsDaily[];
    const { current, longest } = streaks(rows);
    const activeDays = rows.filter((row) => (row.request_success ?? 0) + (row.request_failed ?? 0) > 0).length;

    const cards = [
        { key: 'cumulativeToken', metric: total?.total_token, Icon: Sigma, tone: 'text-chart-1 bg-chart-1/10' },
        { key: 'cumulativeRequest', metric: total?.request_count, Icon: MessageSquare, tone: 'text-chart-2 bg-chart-2/10' },
        { key: 'peakDailyRequest', raw: daily?.max_request_count, unitKey: 'timesUnit', Icon: ServerCog, tone: 'text-chart-3 bg-chart-3/10' },
        { key: 'activeDays', raw: activeDays, unitKey: 'daysUnit', Icon: CalendarCheck, tone: 'text-chart-4 bg-chart-4/10' },
        { key: 'currentStreak', raw: current, unitKey: 'daysUnit', Icon: Flame, tone: 'text-primary bg-primary/10' },
        { key: 'longestStreak', raw: longest, unitKey: 'daysUnit', Icon: CalendarRange, tone: 'text-primary bg-primary/10' },
    ] as const;

    return (
        <section className="rounded-3xl border border-border bg-card p-5 text-card-foreground">
            <div className="mb-4 flex items-center gap-2">
                <h3 className="text-sm font-medium">{t('title')}</h3>
                <span className="ml-auto text-xs text-muted-foreground">{t('cumulativeNote')}</span>
            </div>
            <div className="grid grid-cols-2 gap-4 @2xl/analytics:grid-cols-3 @5xl/analytics:grid-cols-6">
                {cards.map((card) => {
                    const value = 'metric' in card ? card.metric?.formatted.value : card.raw;
                    const unit = 'metric' in card ? card.metric?.formatted.unit : t(card.unitKey);
                    return (
                        <div key={card.key} className="flex items-center gap-3">
                            <div className={`flex size-10 shrink-0 items-center justify-center rounded-xl ${card.tone}`}>
                                <card.Icon className="size-5" />
                            </div>
                            <div className="flex min-w-0 flex-col">
                                <span className="truncate text-xs text-muted-foreground">{t(card.key)}</span>
                                <div className="flex items-baseline gap-1">
                                    <span className="text-xl">
                                        <AnimatedNumber value={value === undefined || value === null ? '—' : value} />
                                    </span>
                                    {unit && value !== undefined && value !== null && (
                                        <span className="text-xs text-muted-foreground">{unit}</span>
                                    )}
                                </div>
                            </div>
                        </div>
                    );
                })}
            </div>
        </section>
    );
}
