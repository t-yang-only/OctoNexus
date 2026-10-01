import { useMemo, useState } from 'react';
import { AlarmClock, Clock, Hourglass, TrendingDown } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useAnalyticsLatency, type LatencyQuantiles } from '@/api/analytics';
import { formatCount } from '@/lib/utils';
import { SampleNote } from '@/components/sample-note';

// T-insight-004 首页「延迟分布」区块。
//
// ## 与上方「请求窗口分析」的分工
//
//   请求窗口分析  回答「多少量、多少钱、谁在吃 token、失败都归谁」
//   延迟分布      回答「整体有多慢，慢是普遍的还是被少数拖累的」
//
// ## 为什么同时给首字节与总耗时两个维度
//
// 两者量的是两件事：前者是「什么时候开始看到东西」，后者是「全部看完花了多久」。
// 同一个系统完全可能首字节很快而总耗时很慢（长输出），也可能首字节就慢（上游排队）。
// 合成一个数字会同时丢掉两边信息。
//
// ## 为什么强调 p95/p99 而不是平均值
//
// 耗时的分布是长尾的，平均值由少数极端值决定：一次 60 秒的超时能把
// 100 次 1 秒的请求拉高到 1.6 秒 —— 这个数字不能描述任何一次真实体验。
// 因此这里只给分位数与直方图，一个平均值都不显示。

// 窗口档位：与 RequestInsight 同款（用条数不用天数 —— 流量差异极大时按天会出现
// 「最近一天只有 3 条」）。
const WINDOWS = [200, 500, 2000] as const;

// p95 的着色阈值（毫秒）：与后端「慢」的语义分开 —— 后端那个是单请求阈值
// （首字节 3000 / 总耗时 30000），这里是"整条健康线在哪"的粗读。
const P95_WARN_MS = 30000;
const P95_BAD_MS = 60000;

// formatMs 把毫秒写成人读的紧凑文本。
//
// 小于 1 秒给整数毫秒（4ms 写成 0.004s 没有意义），1 秒以上给一位小数的秒，
// 分钟以上给「1m23s」—— 60 秒以上的数字用秒表示会变成「83.4s」，
// 那不是一个能一眼读懂的量级。
function formatMs(value: number) {
    if (value <= 0) return '—';
    if (value < 1000) return `${Math.round(value)}ms`;
    if (value < 60000) return `${(value / 1000).toFixed(1)}s`;
    const minutes = Math.floor(value / 60000);
    const seconds = Math.round((value % 60000) / 1000);
    return `${minutes}m${String(seconds).padStart(2, '0')}s`;
}

// p95Tone 按整条健康线着色：与日志页的失败色系同语义（绿/琥珀/玫红）。
function p95Tone(value: number) {
    if (value <= 0) return 'text-muted-foreground';
    if (value >= P95_BAD_MS) return 'text-rose-500';
    if (value >= P95_WARN_MS) return 'text-amber-500';
    return 'text-emerald-500';
}

// QuantileRow 把一组分位数铺成一行四格。
function QuantileRow({ quantiles }: { quantiles: LatencyQuantiles }) {
    const t = useTranslations('home.latency');
    const hasSamples = quantiles.samples > 0;
    const cells = [
        { key: 'p50', label: t('p50'), value: quantiles.p50_ms },
        { key: 'p90', label: t('p90'), value: quantiles.p90_ms },
        { key: 'p95', label: t('p95'), value: quantiles.p95_ms, tone: true },
        { key: 'p99', label: t('p99'), value: quantiles.p99_ms },
    ];
    return (
        <div className="grid grid-cols-4 gap-x-4 gap-y-1">
            {cells.map((cell) => (
                <div key={cell.key} className="min-w-0">
                    <div className="text-[11px] text-muted-foreground">{cell.label}</div>
                    <div
                        className={`truncate text-sm font-semibold tabular-nums ${
                            cell.tone ? p95Tone(cell.value) : ''
                        }`}
                    >
                        {/* 没有样本时显示「—」而不是 0ms：0 会被读成"快得不可思议"，
                            而那与"没有数据"是两件完全不同的事。 */}
                        {hasSamples ? formatMs(cell.value) : '—'}
                    </div>
                </div>
            ))}
        </div>
    );
}

// LatencyDistribution 是首页的「延迟分布」区块。
export function LatencyDistributionPanel() {
    const t = useTranslations('home.latency');
    const [window, setWindow] = useState<number>(500);
    const { data } = useAnalyticsLatency(window);

    // 直方图的柱高按最大计数归一 —— 用绝对计数会让一次高峰把其余全部压平。
    const histogram = useMemo(() => {
        const buckets = data?.duration_histogram ?? [];
        const max = Math.max(...buckets.map((bucket) => bucket.count), 1);
        return { buckets, max };
    }, [data]);

    // 无数据或样本为空时整块不渲染：首页不该出现一排「—」。
    if (!data || (data.duration.samples === 0 && data.first_byte.samples === 0)) return null;

    const duration = data.duration;
    const firstByte = data.first_byte;

    // 四个头部指标卡。
    const cards = [
        {
            label: t('firstByteP50'),
            value: firstByte.samples > 0 ? formatMs(firstByte.p50_ms) : '—',
            hint: t('firstByteHint'),
            icon: Clock,
            bg: 'bg-chart-2/10',
            tone: '',
        },
        {
            label: t('durationP50'),
            value: duration.samples > 0 ? formatMs(duration.p50_ms) : '—',
            hint: t('durationHint'),
            icon: Hourglass,
            bg: 'bg-primary/10',
            tone: '',
        },
        {
            label: t('durationP95'),
            value: duration.samples > 0 ? formatMs(duration.p95_ms) : '—',
            hint: t('p95Hint'),
            icon: TrendingDown,
            bg: 'bg-accent/10',
            tone: p95Tone(duration.p95_ms),
        },
        {
            label: t('slowRatio'),
            value: `${data.duration_tail.slow_ratio.toFixed(1)}%`,
            hint: t('slowRatioHint', {
                threshold: formatMs(data.duration_tail.threshold_ms),
                count: formatCount(data.duration_tail.slow_count).formatted.value,
            }),
            icon: AlarmClock,
            bg: 'bg-chart-3/10',
            tone: data.duration_tail.slow_ratio >= 10 ? 'text-amber-500' : '',
        },
    ];

    return (
        <section className="rounded-3xl bg-card border-border border p-5 text-card-foreground space-y-5">
            <div className="flex items-start justify-between gap-3">
                <div className="min-w-0">
                    <h3 className="font-semibold text-base">{t('title')}</h3>
                    <p className="mt-1 text-xs text-muted-foreground">{t('subtitle')}</p>
                </div>
                <div className="flex shrink-0 gap-1 rounded-xl bg-muted/50 p-1">
                    {WINDOWS.map((value) => (
                        <button
                            key={value}
                            type="button"
                            onClick={() => setWindow(value)}
                            className={`rounded-lg px-2 py-1 text-[11px] tabular-nums transition-colors ${
                                window === value
                                    ? 'bg-background font-medium shadow-sm'
                                    : 'text-muted-foreground hover:text-foreground'
                            }`}
                        >
                            {value}
                        </button>
                    ))}
                </div>
            </div>

            <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
                {cards.map((card) => (
                    <div key={card.label} className="rounded-2xl border border-border/50 p-3" title={card.hint}>
                        <div className="flex items-center gap-2">
                            <span className={`flex size-6 items-center justify-center rounded-lg ${card.bg}`}>
                                <card.icon aria-hidden="true" className="size-3.5" />
                            </span>
                            <span className="truncate text-[11px] text-muted-foreground">{card.label}</span>
                        </div>
                        <div className={`mt-2 truncate text-lg font-semibold tabular-nums ${card.tone}`}>
                            {card.value}
                        </div>
                    </div>
                ))}
            </div>

            {/* 两个维度并排：首字节答"什么时候开始看到东西"，总耗时答"全部看完花了多久"。 */}
            <div className="grid gap-4 md:grid-cols-2">
                <div className="space-y-3 rounded-2xl border border-border/50 p-4">
                    <div className="flex items-baseline justify-between gap-2">
                        <span className="text-sm font-medium">{t('firstByteTitle')}</span>
                        <span className="text-[11px] text-muted-foreground">
                            {t('samples', { count: formatCount(firstByte.samples).formatted.value })}
                        </span>
                    </div>
                    <QuantileRow quantiles={firstByte} />
                    <p className="text-[11px] leading-relaxed text-muted-foreground">{t('firstByteNote')}</p>
                </div>

                <div className="space-y-3 rounded-2xl border border-border/50 p-4">
                    <div className="flex items-baseline justify-between gap-2">
                        <span className="text-sm font-medium">{t('durationTitle')}</span>
                        <span className="text-[11px] text-muted-foreground">
                            {t('samples', { count: formatCount(duration.samples).formatted.value })}
                        </span>
                    </div>
                    <QuantileRow quantiles={duration} />
                    <p className="text-[11px] leading-relaxed text-muted-foreground">{t('durationNote')}</p>
                </div>
            </div>

            {/* 直方图用原生 div 而不是图表库：固定区间的横向条在信息量上等于柱状图，
                但不需要坐标轴与 tooltip，加载也不会因此把图表库拉进首页包。 */}
            <div className="space-y-2">
                <div className="flex items-baseline justify-between gap-2">
                    <span className="text-sm font-medium">{t('histogramTitle')}</span>
                    <span className="text-[11px] text-muted-foreground">{t('histogramNote')}</span>
                </div>
                <SampleNote sample={data.sample} className="mt-1 text-[11px] text-muted-foreground" />
                <div className="space-y-1.5">
                    {histogram.buckets.map((bucket, index) => (
                        <div key={`${bucket.upper_ms}-${index}`} className="flex items-center gap-3">
                            <span className="w-20 shrink-0 text-[11px] tabular-nums text-muted-foreground">
                                {bucket.upper_ms === 0
                                    ? t('bucketOver', { bound: formatMs(60000) })
                                    : t('bucketRange', {
                                          lower: formatMs(index === 0 ? 0 : histogram.buckets[index - 1].upper_ms),
                                          upper: formatMs(bucket.upper_ms),
                                      })}
                            </span>
                            <span className="h-4 min-w-0 flex-1 overflow-hidden rounded bg-muted/60">
                                <span
                                    className="block h-full rounded bg-primary/70"
                                    style={{ width: `${(bucket.count / histogram.max) * 100}%` }}
                                />
                            </span>
                            <span className="w-24 shrink-0 text-right text-[11px] tabular-nums text-muted-foreground">
                                {formatCount(bucket.count).formatted.value} · {bucket.ratio.toFixed(1)}%
                            </span>
                        </div>
                    ))}
                </div>
            </div>

            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
                <span>{t('scanned', { count: formatCount(data.window).formatted.value })}</span>
                <span>
                    {t('overOneMinute')}: {formatCount(data.duration_tail.over_one_minute_count).formatted.value}
                </span>
                {/* 触到上限必须写在明面上：否则「最近 500 条」与「全部 500 条」长得一样。 */}
                {data.truncated && <span className="text-amber-600 dark:text-amber-500">{t('truncated')}</span>}
            </div>
        </section>
    );
}
