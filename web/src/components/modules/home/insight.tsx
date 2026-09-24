import { useMemo, useState } from 'react';
import {
    Activity,
    ArrowDownToLine,
    ArrowUpFromLine,
    Brain,
    Database,
    Gauge,
    HeartPulse,
    Layers,
    Timer,
    Wallet,
    Zap,
} from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from 'recharts';
import { useAnalyticsOverview } from '@/api/analytics';
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { formatCount, formatMoney } from '@/lib/utils';
import { useTheme } from '@/provider/theme';

// 后端把尾部模型并进这个键，前端按它显示本地化的「其他」。
const OTHER_KEY = '__other__';

// 堆叠图保留的模型数，与后端 relayAnalyticsTopModels 对齐。
const TOP_MODELS = 8;

// 窗口档位：用条数而不是天数 —— 流量差异极大时，按天会出现「最近一天只有 3 条」。
const WINDOWS = [200, 500, 2000] as const;

// 时间线的度量口径。
//
// 两档各回答一个问题，缺一不可：
//   tokens  谁在吃量
//   cost    钱花在哪
//
// 不做「请求数」档：后端按模型拆分的只有 tokens 与 cost 两张表，
// 请求数拆分会是多一份几乎同形的数据，而量/钱两个问题已经覆盖了看图的动机。
type TimelineMetric = 'tokens' | 'cost';

// 明细表的维度。
//
// 三个视角对应三个不同的问题，缺一不可：
//   models    哪个模型在吃资源
//   api_keys  哪个调用方在花我的钱
//   channels  钱实际落在哪个上游（failover 后与客户端填的分组名并不相同）
type DimensionKey = 'models' | 'api_keys' | 'channels';

const DIMENSIONS = ['models', 'api_keys', 'channels'] as const;

// 维度 → i18n 键。用映射而不是拼字符串，避免维度名与文案键名耦合。
const DIM_LABEL: Record<DimensionKey, string> = {
    models: 'dimModel',
    api_keys: 'dimApiKey',
    channels: 'dimChannel',
};

// 失败归因的配色：与日志页的失败下钻同语义（成功绿 / 取消灰 / 请求非法黄 /
// 成员与可恢复红橙 / 未分类中性）。
const FAULT_TONE = {
    success: 'bg-emerald-500',
    canceled: 'bg-muted-foreground/50',
    request: 'bg-amber-500',
    member: 'bg-rose-500',
    transient: 'bg-orange-500',
    unclassified: 'bg-muted-foreground/30',
} as const;

// successTone 按成功率着色，与模型榜、模型调用分析同口径。
function successTone(rate: number) {
    if (rate >= 95) return 'text-emerald-500';
    if (rate >= 80) return 'text-amber-500';
    return 'text-rose-500';
}

// cacheTone 按缓存命中率着色。
//
// 阈值刻意比成功率宽松：缓存命中率天然因场景而异（短对话命中率低是正常的，
// 长上下文复用才高），拿同一把尺子卡它会把正常情况标成告警。
function cacheTone(rate: number) {
    if (rate >= 30) return 'text-emerald-500';
    if (rate >= 10) return 'text-amber-500';
    return '';
}

// RequestInsight 是首页的「请求窗口分析」区块。
//
// ## 与上方「模型调用分析」的分工（刻意不重复）
//
//   模型调用分析  读 stats 快照 —— 累计画像：这个模型/渠道总体怎么样
//   请求窗口分析  读 relay_logs —— 窗口切片：最近 N 条请求里发生了什么
//
// 后者专做前者结构上做不到的三件事：
//
//   1. **时间线**：stats 快照落库时已聚合掉明细，画不出「按时间堆叠各模型的消耗」；
//   2. **失败归因**：请求非法 / 成员故障 / 可恢复 / 未分类的分桶，回答「该修什么」；
//   3. **RPM / TPM / 吞吐**：分母分别是窗口跨度与耗时之和，两者刻意不同（见后端说明）。
//
// ## 指标分两组而不是一长排
//
// 「用量与成本」与「健康与速率」是两类不同的问题：前者问"这段时间消耗了多少"，
// 后者问"跑得好不好"。混在一排里读的人会不自觉地把"总花费 3.2"和"成功率 60%"
// 当成同一种数来比。分组标题让每张卡有自己的分母。
export function RequestInsight() {
    const t = useTranslations('home.insight');
    useTheme(); // 订阅主题：--chart-* 取值随主题切换重渲染，与趋势图同机制。
    const [window, setWindow] = useState<number>(500);
    const [metric, setMetric] = useState<TimelineMetric>('tokens');
    const [dimension, setDimension] = useState<DimensionKey>('models');
    const { data } = useAnalyticsOverview(window);
    const series = data?.series ?? [];
    const models = data?.models ?? [];

    // 堆叠键按明细顺序（后端已按请求数倒序），末尾补上「其他」。
    const stackKeys = useMemo(() => {
        const keys: string[] = [];
        for (const stat of models) {
            if (keys.length >= TOP_MODELS) break;
            keys.push(stat.model);
        }
        const hasOther = series.some((bucket) => {
            const source = metric === 'cost' ? bucket.by_model_cost : bucket.by_model;
            return (source?.[OTHER_KEY] ?? 0) > 0;
        });
        if (hasOther) keys.push(OTHER_KEY);
        return keys;
    }, [models, series, metric]);

    const colors = useMemo(() => {
        if (typeof document === 'undefined') return [] as string[];
        const styles = getComputedStyle(document.documentElement);
        return stackKeys.map((_, index) => styles.getPropertyValue(`--chart-${(index % 8) + 1}`).trim() || '#888');
    }, [stackKeys]);

    const stackConfig = useMemo(() => {
        const config: Record<string, { label: string; color: string }> = {};
        stackKeys.forEach((key, index) => {
            config[key] = { label: key === OTHER_KEY ? t('other') : key, color: colors[index] ?? '#888' };
        });
        return config;
    }, [stackKeys, colors, t]);

    // 两个口径用同一份堆叠键，只换数据源：图例与配色因此在切换时保持不变，
    // 眼睛不用重新认一遍颜色 —— 这是"同一次观察换个视角"该有的行为。
    const chartData = useMemo(
        () =>
            series.map((bucket) => {
                const source = metric === 'cost' ? bucket.by_model_cost : bucket.by_model;
                const row: Record<string, string | number> = { bucket: bucket.bucket };
                for (const key of stackKeys) {
                    row[key] = source?.[key] ?? 0;
                }
                return row;
            }),
        [series, stackKeys, metric],
    );

    if (series.length === 0 || !data) return null;

    // 跨度按人话给：少于 1 小时显示分钟，否则显示小时。
    const spanMinutes = data.span_seconds / 60;
    const spanLabel = spanMinutes < 60 ? `${spanMinutes.toFixed(0)} min` : `${(spanMinutes / 60).toFixed(1)} h`;

    const money = formatMoney(data.total_cost);
    const tokens = formatCount(data.total_tokens);
    const avgCost = formatMoney(data.avg_cost_per_request);

    // 用量与成本：这一段回答"这段时间消耗了多少"。
    const usageCards = [
        {
            label: t('requests'),
            value: formatCount(data.request_count).formatted.value,
            unit: formatCount(data.request_count).formatted.unit,
            tone: '',
            icon: Layers,
            bg: 'bg-chart-4/10',
        },
        {
            label: t('totalTokens'),
            value: tokens.formatted.value,
            unit: tokens.formatted.unit,
            tone: '',
            icon: Database,
            bg: 'bg-chart-1/10',
        },
        {
            label: t('cacheHitRate'),
            value: data.cache_hit_rate.toFixed(1),
            unit: '%',
            tone: cacheTone(data.cache_hit_rate),
            icon: ArrowDownToLine,
            bg: 'bg-chart-2/10',
        },
        {
            label: t('totalCost'),
            value: money.formatted.value,
            unit: money.formatted.unit,
            tone: '',
            icon: Wallet,
            bg: 'bg-emerald-500/10',
        },
    ];

    // 健康与速率：这一段回答"跑得好不好"。
    const healthCards = [
        {
            label: t('successRate'),
            value: data.success_rate.toFixed(1),
            unit: '%',
            tone: successTone(data.success_rate),
            icon: Activity,
            bg: 'bg-primary/10',
        },
        {
            label: t('channelRate'),
            value: data.channel_rate.toFixed(1),
            unit: '%',
            tone: successTone(data.channel_rate),
            icon: HeartPulse,
            bg: 'bg-accent/10',
        },
        {
            label: t('avgDuration'),
            value: formatTimeMs(data.avg_duration_ms),
            unit: '',
            tone: '',
            icon: Timer,
            bg: 'bg-chart-3/10',
        },
        {
            label: t('avgCost'),
            value: avgCost.formatted.value,
            unit: avgCost.formatted.unit,
            tone: '',
            icon: Wallet,
            bg: 'bg-chart-4/10',
        },
        {
            label: t('avgRpm'),
            value: data.avg_rpm < 10 ? data.avg_rpm.toFixed(2) : data.avg_rpm.toFixed(1),
            unit: t('rpmUnit'),
            tone: '',
            icon: Timer,
            bg: 'bg-chart-2/10',
        },
        {
            label: t('avgTpm'),
            value: formatCount(data.avg_tpm).formatted.value,
            unit: t('tpmUnit'),
            tone: '',
            icon: Gauge,
            bg: 'bg-chart-1/10',
        },
        {
            label: t('throughput'),
            value: data.throughput_tps.toFixed(1),
            unit: t('tpsUnit'),
            tone: '',
            icon: Zap,
            bg: 'bg-chart-3/10',
        },
    ];

    // TOKEN 构成：三段互不重叠，加起来正好是总 TOKEN。
    //
    // 口径上最容易错的一步：cached 是**输入的子集**，不是独立的一段。
    // 若把「输入 + 缓存 + 输出」三段直接相加，缓存命中的那部分会被算两遍，
    // 总和对不上。所以这里拆的是「未命中输入 / 缓存命中输入 / 输出」。
    const cached = Math.max(0, data.cached_tokens);
    const promptMiss = Math.max(0, data.prompt_tokens - cached);
    const completion = Math.max(0, data.completion_tokens);
    const mixTotal = promptMiss + cached + completion;
    const mix = [
        { key: 'mixPrompt', value: promptMiss, tone: 'bg-chart-1' },
        { key: 'mixCached', value: cached, tone: 'bg-chart-2' },
        { key: 'mixCompletion', value: completion, tone: 'bg-chart-3' },
    ];

    // 明细表当前维度的行（后端已按请求数倒序），以及成本条的基准值。
    const dimensionRows = (data[dimension] ?? []) as typeof models;
    const maxCost = dimensionRows.reduce((peak, row) => (row.cost > peak ? row.cost : peak), 0);
    // 模型链路：只在确实有渠道换过模型时才展示整块（没异常时这块是纯噪声）。
    const chain = data.model_chain ?? [];
    const mismatchSamples = data.mismatch_samples ?? [];

    const faults = [
        { label: t('faultSuccess'), count: data.success_count, tone: FAULT_TONE.success },
        { label: t('faultCanceled'), count: data.canceled, tone: FAULT_TONE.canceled },
        { label: t('faultRequest'), count: data.request_fault, tone: FAULT_TONE.request },
        { label: t('faultMember'), count: data.member_fault, tone: FAULT_TONE.member },
        { label: t('faultTransient'), count: data.transient_fault, tone: FAULT_TONE.transient },
        { label: t('faultUnclassified'), count: data.unclassified, tone: FAULT_TONE.unclassified },
    ];

    const cardGrid = (cards: typeof usageCards) => (
        <div className="grid grid-cols-2 @xl/home:grid-cols-4 gap-4">
            {cards.map((card) => (
                <div key={card.label} className="flex items-center gap-3 rounded-2xl border border-border/50 p-3">
                    <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 text-primary ${card.bg}`}>
                        <card.icon className="w-5 h-5" />
                    </div>
                    <div className="flex flex-col min-w-0">
                        <span className="text-xs text-muted-foreground">{card.label}</span>
                        <div className="flex items-baseline gap-1">
                            <span className={`text-xl tabular-nums ${card.tone}`}>
                                <AnimatedNumber value={String(card.value)} />
                            </span>
                            {card.unit ? <span className="text-sm text-muted-foreground">{card.unit}</span> : null}
                        </div>
                    </div>
                </div>
            ))}
        </div>
    );

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
                            className={`rounded-lg px-2 py-1 text-[11px] tabular-nums transition-colors ${window === value ? 'bg-background font-medium shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}
                        >
                            {value}
                        </button>
                    ))}
                </div>
            </div>

            <div className="space-y-2">
                <h4 className="text-xs font-medium text-muted-foreground">{t('groupUsage')}</h4>
                {cardGrid(usageCards)}
            </div>

            <div className="space-y-2">
                <h4 className="text-xs font-medium text-muted-foreground">{t('groupHealth')}</h4>
                {cardGrid(healthCards)}
            </div>

            {/* TOKEN 构成：一条横条看出"token 花在哪"，比一个总数有信息量得多。 */}
            <div className="rounded-2xl border border-border/50 p-3 space-y-2">
                <div className="flex items-center justify-between gap-2">
                    <h4 className="flex items-center gap-1.5 text-sm font-medium">
                        <Brain className="size-3.5 text-muted-foreground" />
                        {t('tokenMix')}
                    </h4>
                    {data.reasoning_tokens > 0 ? (
                        <span className="text-[11px] text-muted-foreground tabular-nums">
                            {t('mixReasoning')} {formatCount(data.reasoning_tokens).formatted.value}
                        </span>
                    ) : null}
                </div>
                <div className="flex h-3 w-full overflow-hidden rounded-full bg-muted">
                    {mixTotal > 0
                        ? mix.map((segment) => (
                              <div
                                  key={segment.key}
                                  className={`h-full ${segment.tone}`}
                                  style={{ width: `${(segment.value / mixTotal) * 100}%` }}
                                  title={`${t(segment.key)} ${formatCount(segment.value).formatted.value}`}
                              />
                          ))
                        : null}
                </div>
                <div className="flex flex-wrap gap-x-4 gap-y-1">
                    {mix.map((segment) => (
                        <span key={segment.key} className="inline-flex items-center gap-1.5 text-[11px]">
                            <span className={`size-1.5 shrink-0 rounded-full ${segment.tone}`} />
                            <span className="text-muted-foreground">{t(segment.key)}</span>
                            <span className="font-medium tabular-nums">
                                {formatCount(segment.value).formatted.value}
                                {formatCount(segment.value).formatted.unit}
                            </span>
                            <span className="text-muted-foreground tabular-nums">
                                {mixTotal > 0 ? `${((segment.value / mixTotal) * 100).toFixed(1)}%` : '—'}
                            </span>
                        </span>
                    ))}
                </div>
            </div>

            <div className="rounded-2xl border border-border/50 px-2 pt-3">
                <div className="flex items-center justify-between gap-2 px-2">
                    <h4 className="text-sm font-medium">{t('timeline')}</h4>
                    <div className="flex items-center gap-2">
                        <div className="flex gap-1 rounded-lg bg-muted/50 p-0.5">
                            {(['tokens', 'cost'] as const).map((value) => (
                                <button
                                    key={value}
                                    type="button"
                                    onClick={() => setMetric(value)}
                                    className={`rounded-md px-2 py-0.5 text-[11px] transition-colors ${metric === value ? 'bg-background font-medium shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}
                                >
                                    {value === 'tokens' ? t('metricTokens') : t('metricCost')}
                                </button>
                            ))}
                        </div>
                        <span className="text-[11px] text-muted-foreground">
                            {t('spanPrefix')} {spanLabel}
                        </span>
                    </div>
                </div>
                <ChartContainer config={stackConfig} className="h-56 w-full">
                    <BarChart data={chartData} margin={{ left: 4, right: 8 }}>
                        <CartesianGrid strokeDasharray="3 3" vertical={false} />
                        <XAxis dataKey="bucket" tickLine={false} axisLine={false} tick={{ fontSize: 11 }} />
                        <YAxis
                            tickLine={false}
                            axisLine={false}
                            tick={{ fontSize: 11 }}
                            tickFormatter={(value) =>
                                metric === 'cost'
                                    ? formatMoney(value).formatted.value
                                    : formatCount(value).formatted.value
                            }
                        />
                        <ChartTooltip content={<ChartTooltipContent />} />
                        {stackKeys.map((key, index) => (
                            <Bar
                                key={key}
                                dataKey={key}
                                stackId={metric}
                                fill={colors[index] ?? '#888'}
                                radius={index === stackKeys.length - 1 ? [4, 4, 0, 0] : 0}
                            />
                        ))}
                    </BarChart>
                </ChartContainer>
            </div>

            {/* 按维度明细：同一批请求的三种切法，回答"谁在吃资源 / 谁在花钱"。 */}
            <div className="rounded-2xl border border-border/50 p-3 space-y-2">
                <div className="flex items-center justify-between gap-2">
                    <h4 className="text-sm font-medium">{t('breakdown')}</h4>
                    <div className="flex gap-1 rounded-lg bg-muted/50 p-0.5">
                        {DIMENSIONS.map((value) => (
                            <button
                                key={value}
                                type="button"
                                onClick={() => setDimension(value)}
                                className={`rounded-md px-2 py-0.5 text-[11px] transition-colors ${dimension === value ? 'bg-background font-medium shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}
                            >
                                {t(DIM_LABEL[value])}
                            </button>
                        ))}
                    </div>
                </div>
                {dimensionRows.length === 0 ? (
                    <p className="py-2 text-xs text-muted-foreground">{t('emptyDimension')}</p>
                ) : (
                    <div className="space-y-0.5">
                        <div className="grid grid-cols-[1fr_auto_auto] @3xl/home:grid-cols-[1fr_auto_auto_auto_auto] gap-2 px-1 pb-1 text-[11px] text-muted-foreground">
                            <span>{t('colName')}</span>
                            <span className="text-right">{t('requests')}</span>
                            <span className="text-right">{t('successRate')}</span>
                            <span className="hidden text-right @3xl/home:block">{t('totalTokens')}</span>
                            <span className="hidden text-right @3xl/home:block">{t('totalCost')}</span>
                        </div>
                        {dimensionRows.map((row) => (
                            <div
                                key={row.model}
                                className="grid grid-cols-[1fr_auto_auto] @3xl/home:grid-cols-[1fr_auto_auto_auto_auto] items-center gap-2 rounded-lg px-1 py-1 transition-colors hover:bg-muted/40"
                            >
                                <div className="min-w-0">
                                    <div className="truncate text-xs" title={row.model}>
                                        {row.model}
                                    </div>
                                    {/* 成本占比条：以当前维度里最贵的一项为满格，一眼看出谁在花钱 */}
                                    <div className="mt-1 h-1 w-full overflow-hidden rounded-full bg-muted">
                                        <div
                                            className="h-full bg-chart-1"
                                            style={{ width: `${maxCost > 0 ? (row.cost / maxCost) * 100 : 0}%` }}
                                        />
                                    </div>
                                </div>
                                <span className="text-right text-xs tabular-nums">{row.requests}</span>
                                <span className={`text-right text-xs tabular-nums ${successTone(row.success_rate)}`}>
                                    {row.success_rate.toFixed(0)}%
                                </span>
                                <span className="hidden text-right text-xs tabular-nums @3xl/home:block">
                                    {formatCount(row.total_tokens).formatted.value}
                                    {formatCount(row.total_tokens).formatted.unit}
                                </span>
                                <span className="hidden text-right text-xs tabular-nums @3xl/home:block">
                                    {formatMoney(row.cost).formatted.value}
                                    {formatMoney(row.cost).formatted.unit}
                                </span>
                            </div>
                        ))}
                    </div>
                )}
            </div>

            <div>
                <div className="flex items-center justify-between gap-2">
                    <h4 className="text-sm font-medium">{t('faults')}</h4>
                    <span className="text-[11px] text-muted-foreground">
                        {data.truncated ? t('truncatedHint') : t('completeHint')}
                    </span>
                </div>
                <div className="mt-2 flex flex-wrap gap-2">
                    {faults.map((fault) => (
                        <span
                            key={fault.label}
                            className="inline-flex items-center gap-1.5 rounded-full border border-border/60 px-2.5 py-1 text-xs"
                        >
                            <span className={`size-1.5 shrink-0 rounded-full ${fault.tone}`} />
                            <span className="text-muted-foreground">{fault.label}</span>
                            <span className="font-medium tabular-nums">{fault.count}</span>
                        </span>
                    ))}
                </div>
            </div>

            {/*
              模型链路一致性：把「客户端请求的 → 渠道内目标 → 上游自称回报的」三段名字放在一起看。
              这三段散落各处时，「正常别名解析」与「上游偷换模型」长得一模一样 ——
              请求 High-flash 实际跑 glm-5.3-flash 是分组名被解析（正确路由），
              而上游回报了另一个版本号才是模型被换。前者混进不匹配率就会报假故障。
            */}
            {chain.map((row) => row.mismatched > 0).some(Boolean) && (
                <div>
                    <div className="flex items-center justify-between gap-2">
                        <h4 className="text-sm font-medium">{t('modelChain')}</h4>
                        <span className="text-[11px] text-muted-foreground">{t('modelChainHint')}</span>
                    </div>
                    <div className="mt-2 overflow-x-auto">
                        <table className="w-full min-w-[540px] border-collapse text-xs">
                            <thead>
                                <tr className="text-muted-foreground">
                                    <th className="py-1 pr-3 text-left font-normal">{t('colChannel')}</th>
                                    <th className="py-1 pr-3 text-right font-normal">{t('chainRequests')}</th>
                                    <th className="py-1 pr-3 text-right font-normal">
                                        <span title={t('chainAliasHint')} className="border-b border-dotted border-muted-foreground/50">
                                            {t('chainAlias')}
                                        </span>
                                    </th>
                                    <th className="py-1 pr-3 text-right font-normal">
                                        <span title={t('chainReportedHint')} className="border-b border-dotted border-muted-foreground/50">
                                            {t('chainReported')}
                                        </span>
                                    </th>
                                    <th className="py-1 pr-3 text-right font-normal">{t('chainMatched')}</th>
                                    <th className="py-1 pr-3 text-right font-normal">{t('chainMismatched')}</th>
                                    <th className="py-1 pr-3 text-right font-normal">
                                        <span title={t('chainSilentHint')} className="border-b border-dotted border-muted-foreground/50">
                                            {t('chainSilent')}
                                        </span>
                                    </th>
                                    <th className="py-1 text-right font-normal">{t('chainRate')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {chain.map((row) => (
                                    <tr key={row.channel} className="border-t border-border/40">
                                        <td className="py-1 pr-3">{row.channel}</td>
                                        <td className="py-1 pr-3 text-right tabular-nums">{row.requests}</td>
                                        <td className="py-1 pr-3 text-right tabular-nums text-muted-foreground">
                                            {row.alias_resolved}
                                        </td>
                                        <td className="py-1 pr-3 text-right tabular-nums">{row.reported}</td>
                                        <td className="py-1 pr-3 text-right tabular-nums">{row.matched}</td>
                                        <td className={`py-1 pr-3 text-right tabular-nums ${row.mismatched > 0 ? 'font-medium text-red-600' : ''}`}>
                                            {row.mismatched}
                                        </td>
                                        <td className="py-1 pr-3 text-right tabular-nums text-muted-foreground">
                                            {row.silent}
                                        </td>
                                        <td className={`py-1 text-right tabular-nums ${row.mismatch_rate > 0 ? 'text-red-600' : 'text-muted-foreground'}`}>
                                            {row.reported > 0 ? `${row.mismatch_rate.toFixed(1)}%` : '—'}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                    {mismatchSamples.length > 0 && (
                        <div className="mt-3 space-y-1">
                            <div className="text-[11px] text-muted-foreground">{t('mismatchSampleTitle')}</div>
                            {mismatchSamples.map((sample) => (
                                <div key={sample.id} className="flex flex-wrap items-center gap-1.5 text-[11px]">
                                    <span className="rounded border border-border/60 px-1.5 py-0.5 text-muted-foreground">
                                        #{sample.id}
                                    </span>
                                    <span>{sample.channel}</span>
                                    <span className="text-muted-foreground">{sample.requested}</span>
                                    <span className="text-muted-foreground">→</span>
                                    <span className="text-muted-foreground">{sample.target_model}</span>
                                    <span className="text-red-600">≠</span>
                                    <span className="font-medium text-red-600">{sample.reported_model}</span>
                                </div>
                            ))}
                        </div>
                    )}
                </div>
            )}
        </section>
    );
}

// formatTimeMs 把毫秒变成最短的可读形式。
//
// 不用 lib/utils 的 formatTime：那个是给"多久以前"设计的相对时间，
// 这里是时长，语义不同（2.5 秒的请求不该显示成"刚刚"）。
function formatTimeMs(ms: number): string {
    if (!Number.isFinite(ms) || ms <= 0) return '—';
    if (ms < 1000) return `${ms.toFixed(0)}ms`;
    if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
    const minutes = Math.floor(ms / 60000);
    const seconds = Math.round((ms % 60000) / 1000);
    return `${minutes}m${seconds}s`;
}
