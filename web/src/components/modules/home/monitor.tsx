import { useMemo } from 'react';
import { Activity, Bot, CircleDollarSign, HeartPulse, Timer } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { Bar, BarChart, CartesianGrid, Cell, XAxis, YAxis } from 'recharts';
import { useModelMonitor } from '@/api/monitor';
import { ChartContainer, ChartTooltip, ChartTooltipContent } from '@/components/ui/chart';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { formatCount, formatMoney, formatTime } from '@/lib/utils';
import { useTheme } from '@/provider/theme';
import { useHomeViewStore } from './store';

// successTone 按成功率着色：与模型榜 successRate 文案同口径，颜色用本仓语义色（emerald/amber/rose）。
function successTone(rate: number) {
    if (rate >= 95) return 'text-emerald-500';
    if (rate >= 80) return 'text-amber-500';
    return 'text-rose-500';
}

// ModelMonitor 是首页追加的"模型调用分析"区块：new-api 模型调用分析看板的 octopus 画风版。
// 约束登记（NM-CUR-025）裁决：不做三 Tab 看板、不新增导航页，复用本仓卡片/图表/Badge 风格，
// 数据复用 channel/stats 快照派生（R-spec 落定前不另建分钟桶表），只做真实流量的被动聚合。
export function ModelMonitor() {
    const t = useTranslations('home.monitor');
    const { rows, summary } = useModelMonitor();
    useTheme(); // 订阅主题：--chart-* 取色随主题切换重渲染，与趋势图同机制。
    const isChannelNameHidden = useHomeViewStore((state) => state.isChannelNameHidden);

    // 明细按调用量倒序，取 Top 8：与模型榜默认排序一致，表格只放得下头部。
    const topRows = useMemo(() => [...rows].sort((a, b) => b.count - a.count).slice(0, 8), [rows]);
    // 消耗分布按费用倒序取 Top 8 堆叠：与趋势图同色系（--chart-1..8 循环），复用 recharts 栈。
    const distribution = useMemo(() => [...rows].sort((a, b) => b.totalCost - a.totalCost).slice(0, 8), [rows]);
    const chartColors = useMemo(() => {
        if (typeof document === 'undefined') return [] as string[];
        const styles = getComputedStyle(document.documentElement);
        return distribution.map((_, index) => styles.getPropertyValue(`--chart-${(index % 8) + 1}`).trim() || '#888');
    }, [distribution]);

    if (rows.length === 0) {
        return (
            <section className="rounded-3xl bg-card border-border border p-5 text-card-foreground">
                <h3 className="font-semibold text-base">{t('title')}</h3>
                <p className="mt-1 text-xs text-muted-foreground">{t('subtitle')}</p>
                <p className="py-8 text-center text-sm text-muted-foreground">{t('noData')}</p>
            </section>
        );
    }

    const cards = [
        { label: t('totalCount'), value: formatCount(summary.totalCount), icon: Activity, bg: 'bg-primary/10' },
        { label: t('totalCost'), value: formatMoney(summary.totalCost), icon: CircleDollarSign, bg: 'bg-chart-2/10' },
        { label: t('totalTokens'), value: formatCount(summary.totalTokens), icon: Bot, bg: 'bg-chart-1/10' },
        { label: t('successRate'), value: { formatted: { value: summary.avgSuccessRate.toFixed(1), unit: '%' }, raw: summary.avgSuccessRate }, icon: HeartPulse, bg: 'bg-accent/10' },
        { label: t('avgLatency'), value: formatTime(summary.avgWaitMs), icon: Timer, bg: 'bg-chart-3/10' },
    ];

    return (
        <section className="rounded-3xl bg-card border-border border p-5 text-card-foreground space-y-5">
            <div>
                <h3 className="font-semibold text-base">{t('title')}</h3>
                <p className="mt-1 text-xs text-muted-foreground">{t('subtitle')}</p>
            </div>

            {/* 五指标卡：调用/费用/Token/成功率/平均延迟，与 Total 同卡片画风。 */}
            <div className="grid grid-cols-2 @xl/home:grid-cols-3 @3xl/home:grid-cols-5 gap-4">
                {cards.map((card) => (
                    <div key={card.label} className="flex items-center gap-3 rounded-2xl border border-border/50 p-3">
                        <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 text-primary ${card.bg}`}>
                            <card.icon className="w-5 h-5" />
                        </div>
                        <div className="flex flex-col min-w-0">
                            <span className="text-xs text-muted-foreground">{card.label}</span>
                            <div className="flex items-baseline gap-1">
                                <span className="text-xl">
                                    <AnimatedNumber value={String(card.value.formatted.value)} />
                                </span>
                                {Boolean(card.value.formatted.unit) && (
                                    <span className="text-sm text-muted-foreground">{card.value.formatted.unit}</span>
                                )}
                            </div>
                        </div>
                    </div>
                ))}
            </div>

            {/* 模型健康行：成功率着色徽标，语义照 new-api 截图，样式用本仓文字色。 */}
            <div>
                <h4 className="mb-2 text-sm font-medium">{t('health')}</h4>
                <div className="flex flex-wrap gap-2">
                    {topRows.map((row) => (
                        <span
                            key={row.id}
                            title={`${row.modelName} · ${row.successRate.toFixed(1)}%`}
                            className="inline-flex max-w-55 items-center gap-1.5 truncate rounded-full border border-border/60 px-2.5 py-1 text-xs"
                        >
                            <span className={`size-1.5 shrink-0 rounded-full ${row.successRate >= 95 ? 'bg-emerald-500' : row.successRate >= 80 ? 'bg-amber-500' : 'bg-rose-500'}`} />
                            <span className="truncate">{row.modelName}</span>
                            <span className={`font-medium tabular-nums ${successTone(row.successRate)}`}>
                                {row.successRate.toFixed(1)}%
                            </span>
                        </span>
                    ))}
                </div>
            </div>

            <div className="grid grid-cols-1 @3xl/home:grid-cols-2 gap-4">
                {/* 分模型明细表：模型 × 渠道 → 调用/成功率/延迟/Token/费用，渠道名支持模糊。 */}
                <div className="rounded-2xl border border-border/50 overflow-hidden">
                    <h4 className="px-4 pt-3 text-sm font-medium">{t('topModels')}</h4>
                    <div className="max-h-75 overflow-y-auto px-4 pb-3">
                        <table className="w-full text-sm">
                            <thead className="sticky top-0 bg-card">
                                <tr className="text-left text-xs text-muted-foreground">
                                    <th className="py-2 pr-2 font-medium">{t('columns.model')}</th>
                                    <th className="py-2 pr-2 font-medium text-right">{t('columns.count')}</th>
                                    <th className="py-2 pr-2 font-medium text-right">{t('columns.success')}</th>
                                    <th className="py-2 font-medium text-right">{t('columns.cost')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {topRows.map((row) => (
                                    <tr key={row.id} className="border-t border-border/40">
                                        <td className="py-2 pr-2 min-w-0">
                                            <p className="truncate font-medium text-[13px]">{row.modelName}</p>
                                            <p className={`truncate text-xs text-muted-foreground ${isChannelNameHidden ? 'select-none blur-[3px]' : ''}`}>
                                                {row.channelName}
                                            </p>
                                        </td>
                                        <td className="py-2 pr-2 text-right tabular-nums">{formatCount(row.count).formatted.value}</td>
                                        <td className={`py-2 pr-2 text-right tabular-nums ${successTone(row.successRate)}`}>
                                            {row.successRate.toFixed(1)}%
                                        </td>
                                        <td className="py-2 text-right tabular-nums">{formatMoney(row.totalCost).formatted.value}</td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    </div>
                </div>

                {/* 消耗分布：按费用分色的横向条形，与趋势图同 recharts 栈、同 --chart-* 色系。 */}
                <div className="rounded-2xl border border-border/50 px-2 pt-3">
                    <h4 className="px-2 text-sm font-medium">{t('distribution')}</h4>
                    <ChartContainer config={{}} className="h-75 w-full">
                        <BarChart data={distribution} layout="vertical" margin={{ left: 8, right: 12 }}>
                            <CartesianGrid horizontal={false} strokeDasharray="3 3" />
                            <XAxis type="number" hide />
                            <YAxis type="category" dataKey="modelName" width={110} tick={{ fontSize: 11 }} tickLine={false} axisLine={false} />
                            <ChartTooltip content={<ChartTooltipContent />} />
                            <Bar dataKey="totalCost" radius={[4, 4, 4, 4]}>
                                {distribution.map((row, index) => (
                                    <Cell key={row.id} fill={chartColors[index] ?? '#888'} />
                                ))}
                            </Bar>
                        </BarChart>
                    </ChartContainer>
                </div>
            </div>
        </section>
    );
}
