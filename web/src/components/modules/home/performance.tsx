import { Activity, Gauge, Timer, Zap } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useAnalyticsOverview } from '@/api/analytics';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { SampleNote } from '@/components/sample-note';
import { formatCount } from '@/lib/utils';

/**
 * PerformancePanel 把「性能指标」（RPM / TPM / 吞吐）提到首页可见处（需求5）。
 *
 * # 为什么要单独摆一块
 *
 * 这三个数此前只在「请求窗口分析」那块长看板里出现：想知道"现在跑得动多快"，
 * 得先滚过榜单、模型监控，再在一堆卡片里找 —— 而它恰恰是"要不要现在点升级、
 * 要不要现在压测"这类决定最先要看的数。看数据的成本决定了它会不会被看。
 *
 * # 口径（与请求窗口分析逐字同源，不是另一套算法）
 *
 * 窗口档位固定 500：与 insight.tsx 的默认档一致，**同一个 queryKey 会被 React Query
 * 去重** —— 首页同时挂两块也只发一次请求，而且两处显示的数字必然相等。
 * 若这里换一个窗口，同一屏会出现两个"RPM"，用户没法判断该信哪个。
 *
 * 分母是**首尾请求的真实时间差**（span_seconds），不是窗口长度：500 条请求可能
 * 跨了 3 分钟，也可能跨了 3 天。"发满 500 条"与"发满 500 条"看起来一样，
 * 除以不同的时间尺度就是完全不同的负载 —— 所以基数必须写在卡片上。
 */
const WINDOW = 500;

// formatSpan 把秒折成读得懂的量级。只做展示，不参与任何计算。
function formatSpan(seconds: number): string {
    if (!Number.isFinite(seconds) || seconds <= 0) return '0s';
    if (seconds < 90) return `${Math.round(seconds)}s`;
    if (seconds < 5400) return `${(seconds / 60).toFixed(1)}min`;
    return `${(seconds / 3600).toFixed(1)}h`;
}

export function PerformancePanel() {
    const t = useTranslations('home.performance');
    const { data } = useAnalyticsOverview(WINDOW);

    const samples = data?.sample.samples ?? 0;
    const spanSeconds = data?.span_seconds ?? 0;
    // 样本为 0 时三个速率都没有意义：分母为 0 显示「—」而不是 0，
    // 「0 次/分」会被读成"系统闲着"，而事实是"还没有数据"（两种结论，两种处置）。
    const ready = samples > 0;

    const tiles = [
        {
            key: 'rpm',
            label: t('rpm'),
            value: ready ? (data!.avg_rpm < 10 ? data!.avg_rpm.toFixed(2) : data!.avg_rpm.toFixed(1)) : '—',
            unit: ready ? t('rpmUnit') : '',
            icon: Timer,
            bg: 'bg-chart-2/10',
        },
        {
            key: 'tpm',
            label: t('tpm'),
            value: ready ? formatCount(data!.avg_tpm).formatted.value : '—',
            unit: ready ? t('tpmUnit') : '',
            icon: Gauge,
            bg: 'bg-chart-1/10',
        },
        {
            key: 'tps',
            label: t('tps'),
            value: ready ? data!.throughput_tps.toFixed(1) : '—',
            unit: ready ? t('tpsUnit') : '',
            icon: Zap,
            bg: 'bg-chart-3/10',
        },
    ];

    // 活跃时段口径：分母是"有过请求的小时数之和"，不是首尾跨度。
    //
    // 为什么两个都要：负载不均时它们说的是两回事。线上实测 191 个请求里 178 个
    // 集中在一天（一次集中测试），其余零星摊在 6.6 天 —— 按首尾跨度算出的
    // AvgRpm 是 0.02，看着像系统闲着，而那天的真实强度完全看不出来。
    // Peak 回答"忙起来有多忙"，Avg 回答"这段时间总体多忙"。
    const activeHours = data?.active_hours ?? 0;
    const activeReady = ready && activeHours > 0;

    return (
        <section className="rounded-3xl border border-border bg-card p-5 text-card-foreground">
            <header className="mb-4 flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
                <div className="flex items-center gap-2">
                    <Activity className="size-4" />
                    <h3 className="text-sm font-medium">{t('title')}</h3>
                </div>
                <span className="text-[11px] text-muted-foreground">
                    {ready
                        ? t('basis', { count: samples, span: formatSpan(spanSeconds), window: WINDOW })
                        : t('empty')}
                </span>
            </header>

            <div className="grid grid-cols-1 gap-4 @xl/home:grid-cols-3">
                {tiles.map((tile) => (
                    <div key={tile.key} className="flex items-center gap-3">
                        <div className={`flex size-10 shrink-0 items-center justify-center rounded-xl text-primary ${tile.bg}`}>
                            <tile.icon className="size-5" />
                        </div>
                        <div className="flex min-w-0 flex-col">
                            <span className="text-xs text-muted-foreground">{tile.label}</span>
                            <div className="flex items-baseline gap-1">
                                <span className="text-xl tabular-nums">
                                    <AnimatedNumber value={tile.value} />
                                </span>
                                {tile.unit && <span className="text-sm text-muted-foreground">{tile.unit}</span>}
                            </div>
                        </div>
                    </div>
                ))}
            </div>

            {/* 活跃时段口径单独一块：它和上面三个数的分母不同，混在一行会让人
                以为"同一个 RPM 怎么有两个值"。分开并标明各自分母，才读得懂。 */}
            {activeReady && (
                <div className="mt-4 rounded-2xl border border-border/70 bg-muted/30 p-3">
                    <p className="text-xs font-medium text-muted-foreground">{t('activeTitle')}</p>
                    <div className="mt-2 flex flex-wrap items-baseline gap-x-6 gap-y-2">
                        <span className="flex items-baseline gap-1">
                            <span className="text-lg tabular-nums">
                                <AnimatedNumber value={data!.peak_rpm < 10 ? data!.peak_rpm.toFixed(2) : data!.peak_rpm.toFixed(1)} />
                            </span>
                            <span className="text-xs text-muted-foreground">{t('rpmUnit')}</span>
                        </span>
                        <span className="flex items-baseline gap-1">
                            <span className="text-lg tabular-nums">
                                <AnimatedNumber value={formatCount(data!.peak_tpm).formatted.value} />
                            </span>
                            <span className="text-xs text-muted-foreground">{t('tpmUnit')}</span>
                        </span>
                        <span className="text-[11px] text-muted-foreground/70">
                            {t('activeBasis', { hours: activeHours })}
                        </span>
                    </div>
                </div>
            )}

            <p className="mt-3 text-[11px] text-muted-foreground">{t('hint')}</p>
            <SampleNote sample={data?.sample} className="mt-1 text-[11px] text-muted-foreground" />
        </section>
    );
}
