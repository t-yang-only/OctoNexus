import { useState } from 'react';
import { Wallet, Layers, Coins, AlertCircle, RefreshCw, PencilLine } from 'lucide-react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { useBalanceSummary, rescanBalances, setChannelManualBalance, type ChannelBalanceRow } from '@/api/balance';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { ManualSubscriptions } from './manual-subscription';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';

// Balance 展示总余额（T-balance-001）: 各渠道剩余额度按统一口径折算后的合计 + 逐渠道明细。
//
// 口径（与后端 /api/v1/balance/summary 一致）:
// - 总额只累加「读到过余额」的渠道; 没扫到的渠道单独计数（unknown_channels）,
//   把「还没读到」显示成 0 会让人以为账号已经空了。
// - 剩余次数是包月口径（包月额度 - 已用）; 没配包月的渠道不计入。
// - 换算口径 points_per_unit 由设置项 balance_points_per_unit 决定, 默认 500000 点 = 1 单位。
// - 读不到的渠道**必须说清为什么**（reason_text）并允许人工录入：
//   实测 16 个渠道里 14 个站点根本没有 new-api 的余额接口，只说"未读到"用户无从下手。
export function Balance() {
    const { data } = useBalanceSummary();
    const t = useTranslations('home.balance');
    const queryClient = useQueryClient();
    const [editing, setEditing] = useState<number | null>(null);
    const [draft, setDraft] = useState({ points: '', note: '' });

    const total = data?.total ?? 0;
    const currency = data?.currency ?? 'USD';
    const pointsPerUnit = data?.points_per_unit ?? 500000;
    const channels = data?.channels ?? [];
    const knownChannels = channels.filter((item) => item.known);
    const unknown = data?.unknown_channels ?? 0;
    const reasons = data?.reason_counts ?? {};

    const rescan = useMutation({
        mutationFn: () => rescanBalances(),
        onSuccess: (summary) => {
            // 扫描结果直接写回缓存，用户不必等 30 秒的下一次轮询。
            queryClient.setQueryData(['balance', 'summary'], summary);
            toast.success(t('rescanDone', { known: summary.known_channels, unknown: summary.unknown_channels }));
        },
        onError: (error: Error) => toast.error(error.message),
    });

    const saveManual = useMutation({
        mutationFn: (row: ChannelBalanceRow) => setChannelManualBalance(row.channel_id, Number(draft.points) || 0, draft.note),
        onSuccess: (summary) => {
            queryClient.setQueryData(['balance', 'summary'], summary);
            setEditing(null);
            setDraft({ points: '', note: '' });
            toast.success(t('savedManual'));
        },
        onError: (error: Error) => toast.error(error.message),
    });

    return (
        <section className="rounded-3xl bg-card border-border border p-5 text-card-foreground space-y-4">
            <header className="flex flex-wrap items-center justify-between gap-3">
                <div className="flex items-center gap-3">
                    <div className="w-10 h-10 rounded-xl flex items-center justify-center bg-chart-2/10 text-primary">
                        <Wallet className="w-5 h-5" />
                    </div>
                    <div className="flex flex-col">
                        <h3 className="font-medium text-sm">{t('title')}</h3>
                        <span className="text-xs text-muted-foreground">{t('subtitle')}</span>
                    </div>
                </div>
                <div className="flex items-center gap-4">
                    <div className="text-right">
                        <div className="flex items-baseline gap-1 justify-end">
                            <span className="text-2xl">
                                <AnimatedNumber value={total.toFixed(2)} />
                            </span>
                            <span className="text-sm text-muted-foreground">{currency}</span>
                        </div>
                        <span className="text-xs text-muted-foreground">
                            {t('unitHint', { points: pointsPerUnit.toLocaleString() })}
                        </span>
                    </div>
                    <Button
                        size="sm"
                        variant="outline"
                        disabled={rescan.isPending}
                        onClick={() => rescan.mutate()}
                    >
                        <RefreshCw className={`size-4 ${rescan.isPending ? 'animate-spin' : ''}`} />
                        {t('rescan')}
                    </Button>
                </div>
            </header>

            <div className="flex flex-wrap gap-3">
                <div className="flex items-center gap-2 rounded-2xl bg-muted/40 px-3 py-2 text-xs">
                    <Layers className="w-4 h-4 text-muted-foreground" />
                    <span className="text-muted-foreground">{t('knownChannels')}</span>
                    <span className="font-medium">{knownChannels.length}</span>
                </div>
                <div className="flex items-center gap-2 rounded-2xl bg-muted/40 px-3 py-2 text-xs">
                    <Coins className="w-4 h-4 text-muted-foreground" />
                    <span className="text-muted-foreground">{t('monthlyRemaining')}</span>
                    <span className="font-medium">{(data?.total_monthly_remaining ?? 0).toLocaleString()}</span>
                </div>
                {unknown > 0 && (
                    <div className="flex items-center gap-2 rounded-2xl bg-muted/40 px-3 py-2 text-xs">
                        <AlertCircle className="w-4 h-4 text-muted-foreground" />
                        <span className="text-muted-foreground">{t('unknownChannels')}</span>
                        <span className="font-medium">{unknown}</span>
                    </div>
                )}
            </div>

            {/* 一句总览：把"未读到"的原因摊开，用户才知道下一步该做什么。 */}
            {unknown > 0 && (
                <div className="rounded-2xl border border-border/60 px-3 py-2 text-xs text-muted-foreground space-y-1">
                    <p>{t('reasonSummary')}</p>
                    <ul className="flex flex-wrap gap-x-4 gap-y-1">
                        {Object.entries(reasons).map(([code, count]) => (
                            <li key={code}>{t(`reason.${code}` as never)}：{count}</li>
                        ))}
                    </ul>
                </div>
            )}

            {channels.length > 0 && (
                <ul className="space-y-2">
                    {channels.map((channel) => (
                        <li
                            key={channel.channel_id}
                            className="rounded-2xl border border-border/60 px-3 py-2 text-sm"
                        >
                            <div className="flex items-center justify-between gap-3">
                                <div className="flex flex-col min-w-0">
                                    <span className="truncate">{channel.channel_name}</span>
                                    <span className="text-xs text-muted-foreground">
                                        {t('keys', { enabled: channel.key_enabled, total: channel.key_count })}
                                        {channel.monthly_quota > 0 &&
                                            ` · ${t('monthly', { left: channel.monthly_remaining.toLocaleString(), total: channel.monthly_quota.toLocaleString() })}`}
                                    </span>
                                </div>
                                <div className="flex shrink-0 items-center gap-2">
                                    <span className="font-medium">
                                        {channel.known
                                            ? `${channel.balance.toFixed(2)} ${currency}`
                                            : t('unknown')}
                                        {channel.balance_source === 'manual' && (
                                            <span className="ml-1 text-xs text-muted-foreground">{t('manualSource')}</span>
                                        )}
                                    </span>
                                    <Button
                                        size="sm"
                                        variant="ghost"
                                        title={t('manualHint')}
                                        onClick={() => {
                                            setEditing(channel.channel_id);
                                            setDraft({ points: channel.remaining > 0 ? String(channel.remaining) : '', note: channel.note ?? '' });
                                        }}
                                    >
                                        <PencilLine className="size-4" />
                                    </Button>
                                </div>
                            </div>
                            {/* 读不到就说清为什么；能读到的站点不显示这一行。 */}
                            {!channel.known && channel.reason_text && (
                                <p className="mt-1 text-xs text-destructive/80">{channel.reason_text}</p>
                            )}
                            {!channel.known && !channel.reason_text && channel.note && (
                                <p className="mt-1 text-xs text-muted-foreground">{channel.note}</p>
                            )}
                            {channel.known && channel.manual_at && (
                                <p className="mt-1 text-xs text-muted-foreground">{t('manualAt', { at: channel.manual_at })}</p>
                            )}

                            {editing === channel.channel_id && (
                                <div className="mt-2 flex flex-wrap items-end gap-2">
                                    <div className="space-y-1">
                                        <label className="text-xs text-muted-foreground" htmlFor={`points-${channel.channel_id}`}>
                                            {t('manualPoints')}
                                        </label>
                                        <Input
                                            id={`points-${channel.channel_id}`}
                                            value={draft.points}
                                            onChange={(event) => setDraft({ ...draft, points: event.target.value })}
                                            placeholder="0"
                                            className="rounded-xl h-9 w-32"
                                        />
                                    </div>
                                    <div className="space-y-1 flex-1 min-w-[12rem]">
                                        <label className="text-xs text-muted-foreground" htmlFor={`note-${channel.channel_id}`}>
                                            {t('manualNote')}
                                        </label>
                                        <Input
                                            id={`note-${channel.channel_id}`}
                                            value={draft.note}
                                            onChange={(event) => setDraft({ ...draft, note: event.target.value })}
                                            placeholder={t('manualNotePlaceholder')}
                                            className="rounded-xl h-9"
                                        />
                                    </div>
                                    <Button size="sm" disabled={saveManual.isPending} onClick={() => saveManual.mutate(channel)}>
                                        {t('save')}
                                    </Button>
                                    <Button size="sm" variant="ghost" onClick={() => setEditing(null)}>
                                        {t('cancel')}
                                    </Button>
                                    <span className="text-xs text-muted-foreground">{t('manualUnitHint', { points: pointsPerUnit.toLocaleString() })}</span>
                                </div>
                            )}
                        </li>
                    ))}
                </ul>
            )}

            {channels.length === 0 && (
                <p className="text-xs text-muted-foreground">{t('empty')}</p>
            )}

            {/* 手动订阅（R-acct-004）：无接口站点的余额来源，人录一条即并入总额。 */}
            <ManualSubscriptions
                rows={data?.manual_subscriptions ?? []}
                channels={channels}
                currency={currency}
                globalPointsPerUnit={pointsPerUnit}
                manualTotal={data?.manual_total ?? 0}
                expired={data?.manual_expired ?? 0}
            />
        </section>
    );
}
