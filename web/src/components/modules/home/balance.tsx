import { Wallet, Layers, Coins, AlertCircle } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useBalanceSummary } from '@/api/balance';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';

// Balance 展示总余额（T-balance-001）: 各渠道剩余额度按统一口径折算后的合计 + 逐渠道明细。
//
// 口径（与后端 /api/v1/balance/summary 一致）:
// - 总额只累加「读到过余额」的渠道; 没扫到的渠道单独计数（unknown_channels）,
//   把「还没读到」显示成 0 会让人以为账号已经空了。
// - 剩余次数是包月口径（包月额度 - 已用）; 没配包月的渠道不计入。
// - 换算口径 points_per_unit 由设置项 balance_points_per_unit 决定, 默认 500000 点 = 1 单位。
export function Balance() {
    const { data } = useBalanceSummary();
    const t = useTranslations('home.balance');

    const total = data?.total ?? 0;
    const currency = data?.currency ?? 'USD';
    const pointsPerUnit = data?.points_per_unit ?? 500000;
    const channels = data?.channels ?? [];
    const knownChannels = channels.filter((item) => item.known);
    const unknown = data?.unknown_channels ?? 0;

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

            {channels.length > 0 && (
                <ul className="space-y-2">
                    {channels.map((channel) => (
                        <li
                            key={channel.channel_id}
                            className="flex items-center justify-between gap-3 rounded-2xl border border-border/60 px-3 py-2 text-sm"
                        >
                            <div className="flex flex-col min-w-0">
                                <span className="truncate">{channel.channel_name}</span>
                                <span className="text-xs text-muted-foreground">
                                    {t('keys', { enabled: channel.key_enabled, total: channel.key_count })}
                                    {channel.monthly_quota > 0 &&
                                        ` · ${t('monthly', { left: channel.monthly_remaining.toLocaleString(), total: channel.monthly_quota.toLocaleString() })}`}
                                </span>
                            </div>
                            <span className="shrink-0 font-medium">
                                {channel.known
                                    ? `${channel.balance.toFixed(2)} ${currency}`
                                    : t('unknown')}
                            </span>
                        </li>
                    ))}
                </ul>
            )}

            {channels.length === 0 && (
                <p className="text-xs text-muted-foreground">{t('empty')}</p>
            )}
        </section>
    );
}
