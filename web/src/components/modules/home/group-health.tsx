import { useMemo, useState } from 'react';
import { AlertTriangle, Ban, CircleSlash, HeartPulse, Layers, Timer } from 'lucide-react';
import { useTranslations } from 'use-intl';
import dayjs from 'dayjs';
import { useAnalyticsGroupHealth, type GroupHealthRow } from '@/api/analytics';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { formatCount } from '@/lib/utils';
import { useHomeViewStore } from './store';

// 状态外观表：颜色用本仓语义色（emerald/amber/rose），图标区分"没坏但空"与"真的坏"。
// 六种状态的含义由后端判定并随结果返回判定线，界面只做映射，不重复实现阈值。
const STATE_STYLE: Record<string, { tone: string; dot: string; icon: typeof HeartPulse }> = {
    healthy: { tone: 'text-emerald-500', dot: 'bg-emerald-500', icon: HeartPulse },
    degraded: { tone: 'text-amber-500', dot: 'bg-amber-500', icon: AlertTriangle },
    failing: { tone: 'text-rose-500', dot: 'bg-rose-500', icon: AlertTriangle },
    no_member: { tone: 'text-rose-500', dot: 'bg-rose-500', icon: Ban },
    idle: { tone: 'text-muted-foreground', dot: 'bg-muted-foreground', icon: Timer },
    empty: { tone: 'text-muted-foreground', dot: 'bg-muted-foreground', icon: CircleSlash },
};

// 后端枚举 -> 文案键。认不出的状态原样显示，不猜、不隐藏（新状态上线时不会显示成空白）。
const STATE_KEYS = ['healthy', 'degraded', 'failing', 'no_member', 'idle', 'empty'];

// GroupHealthPanel 是首页追加的"分组健康画像"区块：按分组看成功率与故障归因。
// 与既有区块的分工：模型调用分析看"模型 × 渠道"，请求窗口分析看"时间线/归因/RPM"，
// 延迟分布看"整体有多慢"；这一块回答的是另一类问题——「哪个分组坏了、坏在哪个上游、
// 是没成员还是成员在失败」。生产动机是验收时反复撞到的 `53HK/Auto-Model 无可用成员`：
// 那类失败没有 target_channel，渠道故障统计看不见它，只有在分组维度才现形。
export function GroupHealthPanel() {
    const t = useTranslations('home.groupHealth');
    const isChannelNameHidden = useHomeViewStore((state) => state.isChannelNameHidden);
    const [window, setWindow] = useState(500);
    const query = useAnalyticsGroupHealth(window);
    const data = query.data;

    // 需要关注的分组排前面，其余保持后端顺序（后端已按 状态/失败数/请求数/名称 排好）。
    const rows = useMemo(() => data?.groups ?? [], [data]);
    const counters = useMemo(() => {
        if (!data) return [];
        return [
            { key: 'failing', label: t('state.failing'), value: data.failing_count, tone: 'text-rose-500' },
            { key: 'no_member', label: t('state.no_member'), value: data.no_member_count, tone: 'text-rose-500' },
            { key: 'degraded', label: t('state.degraded'), value: data.degraded_count, tone: 'text-amber-500' },
            { key: 'healthy', label: t('state.healthy'), value: data.healthy_count, tone: 'text-emerald-500' },
            { key: 'idle', label: t('state.idle'), value: data.idle_count, tone: 'text-muted-foreground' },
            { key: 'empty', label: t('state.empty'), value: data.empty_count, tone: 'text-muted-foreground' },
        ];
    }, [data, t]);

    const stateLabel = (state: string) => (STATE_KEYS.includes(state) ? t(`state.${state}`) : state);
    const stateStyle = (state: string) => STATE_STYLE[state] ?? STATE_STYLE.idle;

    if (!data || rows.length === 0) {
        return (
            <section className="rounded-3xl bg-card border-border border p-5 text-card-foreground">
                <h3 className="font-semibold text-base">{t('title')}</h3>
                <p className="mt-1 text-xs text-muted-foreground">{t('subtitle')}</p>
                <p className="py-8 text-center text-sm text-muted-foreground">{t('noData')}</p>
            </section>
        );
    }

    return (
        <section className="rounded-3xl bg-card border-border border p-5 text-card-foreground space-y-5">
            <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                    <h3 className="font-semibold text-base">{t('title')}</h3>
                    <p className="mt-1 text-xs text-muted-foreground">{t('subtitle')}</p>
                    {/* 判定线随结果返回并写在界面上：用户看到的"故障/退化"必须与后端口径对应。 */}
                    <p className="mt-1 text-[11px] text-muted-foreground">
                        {t('thresholds', { failing: data.failing_threshold, degraded: data.degraded_threshold })}
                        {' · '}
                        {t('sampleHint', { samples: data.sample?.samples ?? 0, skipped: data.sample?.test_skipped ?? 0 })}
                    </p>
                </div>
                <div className="flex shrink-0 items-center gap-2">
                    <span className="text-xs text-muted-foreground">{t('windowLabel')}</span>
                    <div className="flex gap-1 rounded-xl bg-muted/50 p-1">
                        {[500, 2000, 5000].map((value) => (
                            <button
                                key={value}
                                type="button"
                                onClick={() => setWindow(value)}
                                className={`rounded-lg px-2 py-1 text-[11px] transition-colors ${window === value ? 'bg-background font-medium shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}
                            >
                                {value}
                            </button>
                        ))}
                    </div>
                </div>
            </div>

            {/* 六态计数：把"全库有多少分组处于什么状态"摊开，比只看表格更先看到问题规模。 */}
            <div className="grid grid-cols-3 @xl/home:grid-cols-6 gap-3">
                {counters.map((item) => (
                    <div key={item.key} className="rounded-2xl border border-border/50 p-3">
                        <span className="text-xs text-muted-foreground">{item.label}</span>
                        <div className={`mt-1 text-xl tabular-nums ${item.value > 0 ? item.tone : 'text-muted-foreground'}`}>
                            <AnimatedNumber value={String(item.value)} />
                        </div>
                    </div>
                ))}
            </div>

            <div className="rounded-2xl border border-border/50 overflow-hidden">
                <div className="flex items-center gap-2 px-4 pt-3">
                    <Layers className="size-4 text-muted-foreground" />
                    <h4 className="text-sm font-medium">{t('groupsTitle')}</h4>
                    <span className="text-xs text-muted-foreground">{t('groupsCount', { count: rows.length })}</span>
                </div>
                <div className="max-h-105 overflow-y-auto px-4 pb-3">
                    <table className="w-full text-sm">
                        <thead className="sticky top-0 bg-card">
                            <tr className="text-left text-xs text-muted-foreground">
                                <th className="py-2 pr-2 font-medium">{t('columns.group')}</th>
                                <th className="py-2 pr-2 font-medium">{t('columns.state')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.members')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.requests')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.rate')}</th>
                                <th className="py-2 font-medium">{t('columns.lastFailure')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {rows.map((row: GroupHealthRow) => {
                                const style = stateStyle(row.state);
                                const Icon = style.icon;
                                return (
                                    <tr key={row.group_id} className="border-t border-border/40 align-top">
                                        <td className="py-2 pr-2 min-w-0">
                                            <p className="truncate font-medium text-[13px]">
                                                {row.name}
                                                {row.deleted && <span className="ml-1 text-[10px] text-muted-foreground">({t('deleted')})</span>}
                                            </p>
                                            <p className="truncate text-[11px] text-muted-foreground">{row.mode}</p>
                                            {/* 失败上游下钻：只在这里能看到"坏在谁身上"，渠道名支持模糊。 */}
                                            {row.failing_channels.length > 0 && (
                                                <div className={`mt-1 flex flex-wrap gap-1 ${isChannelNameHidden ? 'select-none blur-[3px]' : ''}`}>
                                                    {row.failing_channels.map((ch) => (
                                                        <span
                                                            key={`${ch.channel}-${ch.model}`}
                                                            title={`${ch.channel || t('noChannel')} · ${ch.model}`}
                                                            className="inline-flex max-w-55 items-center gap-1 truncate rounded-full border border-border/60 px-2 py-0.5 text-[10px] text-muted-foreground"
                                                        >
                                                            <span className="truncate">{ch.channel || t('noChannel')}</span>
                                                            <span className="tabular-nums">{ch.failures}</span>
                                                        </span>
                                                    ))}
                                                </div>
                                            )}
                                        </td>
                                        <td className="py-2 pr-2">
                                            <span className={`inline-flex items-center gap-1.5 text-[12px] ${style.tone}`}>
                                                <Icon className="size-3.5 shrink-0" />
                                                {stateLabel(row.state)}
                                            </span>
                                        </td>
                                        <td className="py-2 pr-2 text-right tabular-nums">
                                            <span className={row.available_count === 0 ? 'text-rose-500' : ''}>
                                                {row.available_count}
                                            </span>
                                            <span className="text-muted-foreground">/{row.member_count}</span>
                                        </td>
                                        <td className="py-2 pr-2 text-right tabular-nums">
                                            {formatCount(row.requests).formatted.value}
                                            {row.canceled + row.request_fault > 0 && (
                                                <span className="ml-1 text-[10px] text-muted-foreground">
                                                    (+{formatCount(row.canceled + row.request_fault).formatted.value})
                                                </span>
                                            )}
                                        </td>
                                        {/* 分母是 success_samples：样本为 0 时显示 — 而不是 0%，否则会读成"全失败"。 */}
                                        <td className={`py-2 pr-2 text-right tabular-nums ${row.success_samples > 0 ? style.tone : 'text-muted-foreground'}`}>
                                            {row.success_samples > 0 ? `${row.success_rate.toFixed(1)}%` : '—'}
                                        </td>
                                        <td className="py-2 text-[11px] text-muted-foreground">
                                            {row.last_failure_at ? dayjs(row.last_failure_at).format('MM-DD HH:mm') : '—'}
                                        </td>
                                    </tr>
                                );
                            })}
                        </tbody>
                    </table>
                </div>
            </div>
        </section>
    );
}