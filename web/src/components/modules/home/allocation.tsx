import { useMemo } from 'react';
import { Activity, AlertTriangle, Ban, Gauge, Layers, Snail, ThumbsDown, TimerOff, Zap } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useAllocationMonitor, type AllocationRow } from '@/api/allocmon';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { formatCount, formatTime } from '@/lib/utils';

// AllocationMonitor 是首页的「分压与限流监控」区块（T-monitor-001）。
//
// 用户口径："给前端页面升级更加详细的监控页面"。这一块回答四个问题：
//  1. 每把钥匙/每个成员**还能干多少次活**（剩余请求数, 含口径来源：包月余量 / 余额折算 / 未知）；
//  2. 它现在**能分到多少流量**（分压权重 + 健康系数）——这就是"按剩余请求数最优分配"的可视化；
//  3. 它为什么此刻分不到流量（冷却中 / 上游限流中 / 自限流让开 / 不可用）；
//  4. 它最近一分钟**已经被用了多少**（RPM/TPM 的我们自己这一侧计数）。
//
// 口径全部来自后端 /api/v1/monitor/allocation（选路层同一函数算出来的），本组件只展示：
// 未知剩余请求数一律显示「未知」而不是 0（0 会被读成"没钱了"，那是另一个意思）。
function stateOf(row: AllocationRow) {
    if (!row.available) return 'unavailable';
    if (row.throttled) return 'throttled';
    if (row.cooling) return 'cooling';
    if (row.limited) return 'limited';
    return 'ok';
}

const STATE_TONE: Record<string, string> = {
    ok: 'bg-emerald-500/10 text-emerald-600',
    cooling: 'bg-amber-500/10 text-amber-600',
    throttled: 'bg-rose-500/10 text-rose-600',
    limited: 'bg-sky-500/10 text-sky-600',
    unavailable: 'bg-muted text-muted-foreground',
};

export function AllocationMonitor() {
    const t = useTranslations('home.allocation');
    const { data, isLoading } = useAllocationMonitor();
    // rows 用 useMemo 固定引用: 下面两处派生计算都依赖它, 每次渲染新数组会让 useMemo 每次都失效。
    const rows = useMemo(() => data?.rows ?? [], [data]);
    const summary = data?.summary;
    const settings = data?.settings;

    // 权重条以本分组内的最大权重为满格：分压是"组内相对比例"，跨组比较没有意义。
    const maxWeightByGroup = useMemo(() => {
        const map = new Map<number, number>();
        for (const row of rows) {
            const current = map.get(row.group_id) ?? 0;
            if (row.weight > current) map.set(row.group_id, row.weight);
        }
        return map;
    }, [rows]);

    if (isLoading && rows.length === 0) {
        return (
            <section className="rounded-3xl bg-card border-border border p-5 text-card-foreground">
                <h3 className="font-semibold text-base">{t('title')}</h3>
                <p className="mt-1 text-xs text-muted-foreground">{t('subtitle')}</p>
                <p className="py-8 text-center text-sm text-muted-foreground">{t('loading')}</p>
            </section>
        );
    }

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
        { label: t('cards.members'), value: String(summary?.member_count ?? 0), icon: Layers, bg: 'bg-primary/10' },
        { label: t('cards.known'), value: String(summary?.known_count ?? 0), icon: Activity, bg: 'bg-chart-1/10' },
        { label: t('cards.unknown'), value: String(summary?.unknown_count ?? 0), icon: AlertTriangle, bg: 'bg-muted' },
        { label: t('cards.slow'), value: String(summary?.slow_count ?? 0), icon: Snail, bg: 'bg-amber-500/10' },
        { label: t('cards.cooling'), value: String(summary?.cooling_count ?? 0), icon: TimerOff, bg: 'bg-amber-500/10' },
        { label: t('cards.throttled'), value: String(summary?.throttled_count ?? 0), icon: Gauge, bg: 'bg-rose-500/10' },
        { label: t('cards.limited'), value: String(summary?.limited_count ?? 0), icon: Ban, bg: 'bg-sky-500/10' },
    ];

    // 排序：先把"分不到流量"的成员（不可用/限流/冷却）顶上来，其余按权重从高到低。
    const sorted = [...rows].sort((a, b) => {
        const rank = (row: AllocationRow) => {
            const state = stateOf(row);
            if (state === 'unavailable') return 0;
            if (state === 'throttled') return 1;
            if (state === 'cooling') return 2;
            if (state === 'limited') return 3;
            return 4;
        };
        const diff = rank(a) - rank(b);
        if (diff !== 0) return diff;
        if (a.weight !== b.weight) return b.weight - a.weight;
        return a.item_id - b.item_id;
    });

    const limitText = (value: number) => (value > 0 ? String(value) : t('settings.off'));

    return (
        <section className="rounded-3xl bg-card border-border border p-5 text-card-foreground space-y-5">
            <div className="flex flex-wrap items-start justify-between gap-2">
                <div>
                    <h3 className="font-semibold text-base">{t('title')}</h3>
                    <p className="mt-1 text-xs text-muted-foreground">{t('subtitle')}</p>
                </div>
                <div className="flex items-center gap-2 rounded-2xl bg-muted/40 px-3 py-2 text-xs">
                    <Zap className="size-3.5 text-muted-foreground" />
                    <span className="text-muted-foreground">{t('cards.remaining')}</span>
                    <span className="font-medium tabular-nums">
                        <AnimatedNumber value={Math.round(summary?.total_requests ?? 0).toLocaleString()} />
                    </span>
                </div>
            </div>

            {/* 七指标卡：成员数 / 剩余已知 / 剩余未知 / 慢 / 冷却 / 限流 / 自限流让开 */}
            <div className="grid grid-cols-2 @xl/home:grid-cols-4 @3xl/home:grid-cols-7 gap-4">
                {cards.map((card) => (
                    <div key={card.label} className="flex items-center gap-3 rounded-2xl border border-border/50 p-3">
                        <div className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 text-primary ${card.bg}`}>
                            <card.icon className="w-5 h-5" />
                        </div>
                        <div className="flex flex-col min-w-0">
                            <span className="text-xs text-muted-foreground">{card.label}</span>
                            <span className="text-xl tabular-nums">
                                <AnimatedNumber value={card.value} />
                            </span>
                        </div>
                    </div>
                ))}
            </div>

            {/* 生效口径：设置项当前值，避免"我明明改了设置为什么没效果"这种疑问。 */}
            {settings && (
                <div className="rounded-2xl border border-border/50 px-4 py-3">
                    <h4 className="text-sm font-medium">{t('settings.title')}</h4>
                    <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs text-muted-foreground">
                        <span>{t('settings.tokens')}: <span className="tabular-nums text-foreground">{settings.estimate_tokens}</span></span>
                        <span>{t('settings.health')}: <span className="tabular-nums text-foreground">{settings.health_weight}</span></span>
                        <span>{t('settings.slow')}: <span className="tabular-nums text-foreground">{settings.slow_latency_ms > 0 ? `${settings.slow_latency_ms}ms` : t('settings.off')}</span></span>
                        <span>{t('settings.minRequests')}: <span className="tabular-nums text-foreground">{settings.min_requests}</span></span>
                        {/* 速度口径（T-speed-001）：折扣强度、两个"慢"阈值、自适应首帧看门狗 */}
                        <span>{t('settings.speedWeight')}: <span className="tabular-nums text-foreground">{settings.speed_weight > 0 ? settings.speed_weight : t('settings.off')}</span></span>
                        <span>{t('settings.slowTtfb')}: <span className="tabular-nums text-foreground">{settings.slow_ttfb_ms > 0 ? `${settings.slow_ttfb_ms}ms` : t('settings.off')}</span></span>
                        <span>{t('settings.slowTps')}: <span className="tabular-nums text-foreground">{settings.slow_tokens_per_sec > 0 ? `${settings.slow_tokens_per_sec} tok/s` : t('settings.off')}</span></span>
                        <span>{t('settings.feMultiple')}: <span className="tabular-nums text-foreground">{settings.first_event_multiple > 0 ? `${settings.first_event_multiple}× / ${Math.round(settings.first_event_floor_ms / 1000)}s` : t('settings.off')}</span></span>
                        <span>{t('settings.rpm')}: <span className="tabular-nums text-foreground">{limitText(settings.member_rpm_limit)}</span></span>
                        <span>{t('settings.tpm')}: <span className="tabular-nums text-foreground">{limitText(settings.member_tpm_limit)}</span></span>
                        <span>{t('settings.throttleCap')}: <span className="tabular-nums text-foreground">{settings.throttle_cap_seconds}s</span></span>
                    </div>
                </div>
            )}

            <div className="rounded-2xl border border-border/50 overflow-hidden">
                <h4 className="px-4 pt-3 text-sm font-medium">{t('table.title')}</h4>
                <div className="max-h-100 overflow-y-auto overflow-x-auto px-4 pb-3">
                    <table className="w-full min-w-160 text-sm">
                        <thead className="sticky top-0 bg-card">
                            <tr className="text-left text-xs text-muted-foreground">
                                <th className="py-2 pr-2 font-medium">{t('columns.member')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.remaining')}</th>
                                <th className="py-2 pr-2 font-medium">{t('columns.weight')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.health')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.recent')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.success')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.latency')}</th>
                                <th className="py-2 pr-2 font-medium text-right">{t('columns.speed')}</th>
                                <th className="py-2 font-medium">{t('columns.state')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {sorted.map((row) => {
                                const state = stateOf(row);
                                const groupMax = maxWeightByGroup.get(row.group_id) ?? 100;
                                const percent = groupMax > 0 ? Math.max(4, Math.round((row.weight / groupMax) * 100)) : 0;
                                return (
                                    <tr key={row.item_id} className="border-t border-border/40">
                                        <td className="py-2 pr-2 min-w-0">
                                            <p className="truncate font-medium text-[13px]">{row.model_name || t('unknownModel')}</p>
                                            <p className="truncate text-xs text-muted-foreground">
                                                {row.channel_name}
                                                {row.key_name ? ` · ${row.key_name}` : ''}
                                                {' · '}
                                                {row.group_name}
                                            </p>
                                        </td>
                                        <td className="py-2 pr-2 text-right tabular-nums whitespace-nowrap">
                                            {row.known ? (
                                                <span title={t(`source.${row.source || 'balance'}`)}>
                                                    {formatCount(Math.round(row.requests)).formatted.value}
                                                    <span className="ml-1 text-[11px] text-muted-foreground">
                                                        {t(`source.${row.source === 'monthly' ? 'monthly' : 'balance'}`)}
                                                    </span>
                                                </span>
                                            ) : (
                                                <span className="text-muted-foreground" title={t('unknownHint')}>{t('unknownValue')}</span>
                                            )}
                                        </td>
                                        <td className="py-2 pr-2">
                                            {row.weight > 0 ? (
                                                <div className="flex items-center gap-2">
                                                    <div className="h-1.5 w-16 shrink-0 overflow-hidden rounded-full bg-muted">
                                                        <div className="h-full rounded-full bg-primary" style={{ width: `${percent}%` }} />
                                                    </div>
                                                    <span className="tabular-nums text-xs text-muted-foreground">{row.weight}</span>
                                                </div>
                                            ) : (
                                                // 权重 0 表示这次分配里没有它（不可用/余量用尽或在冷却/让开）:
                                                // 显示 0 会被读成"权重最低", 与"根本没参与"是两回事。
                                                <span className="text-xs text-muted-foreground" title={t('notCountedHint')}>—</span>
                                            )}
                                        </td>
                                        <td className="py-2 pr-2 text-right tabular-nums">
                                            {Math.round(row.health_factor * 100)}%
                                        </td>
                                        <td className="py-2 pr-2 text-right tabular-nums text-xs whitespace-nowrap">
                                            {row.recent_reqs}
                                            <span className="text-muted-foreground"> / </span>
                                            {formatCount(row.recent_tokens).formatted.value}
                                        </td>
                                        <td className="py-2 pr-2 text-right tabular-nums">
                                            {row.has_samples ? `${(row.success_rate * 100).toFixed(1)}%` : <span className="text-muted-foreground">—</span>}
                                        </td>
                                        <td className="py-2 pr-2 text-right tabular-nums">
                                            {row.latency_ms > 0 ? formatTime(row.latency_ms).formatted.value + formatTime(row.latency_ms).formatted.unit : <span className="text-muted-foreground">—</span>}
                                        </td>
                                        {/* 速度（T-speed-001）：首帧 + 吞吐。样本不足时显示"量测中"而不是 0——
                                            0 会被读成"一秒吐不出字"，那是另一个意思。 */}
                                        <td className="py-2 pr-2 text-right tabular-nums text-xs whitespace-nowrap">
                                            {row.speed_samples > 0 ? (
                                                <span title={t('speedHint', { count: row.speed_samples })}>
                                                    {row.ttfb_ms > 0 ? `${formatTime(row.ttfb_ms).formatted.value}${formatTime(row.ttfb_ms).formatted.unit}` : '—'}
                                                    <span className="text-muted-foreground"> / </span>
                                                    {row.tokens_per_sec > 0 ? `${row.tokens_per_sec.toFixed(1)} tok/s` : '—'}
                                                    {row.slow && <Snail className="ml-1 inline size-3.5 align-[-2px] text-amber-600" />}
                                                </span>
                                            ) : (
                                                <span className="text-muted-foreground" title={t('speedNoSamples')}>{t('speedPending')}</span>
                                            )}
                                        </td>
                                        <td className="py-2">
                                            <div className="flex flex-wrap items-center gap-1">
                                                <span className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${STATE_TONE[state]}`}>
                                                    {t(`state.${state}`)}
                                                </span>
                                                {row.cooling && row.cooldown_ms > 0 && (
                                                    <span className="text-[11px] text-muted-foreground tabular-nums">{Math.ceil(row.cooldown_ms / 1000)}s</span>
                                                )}
                                                {row.throttled && row.throttle_ms > 0 && (
                                                    <span className="text-[11px] text-muted-foreground tabular-nums">{Math.ceil(row.throttle_ms / 1000)}s</span>
                                                )}
                                                {row.throttle_hits > 0 && (
                                                    <span className="text-[11px] text-muted-foreground">
                                                        {t('throttleHits', { count: row.throttle_hits })}
                                                    </span>
                                                )}
                                                {row.smart_tier && (
                                                    <span className="rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
                                                        {t(`tier.${row.smart_tier}`)}
                                                    </span>
                                                )}
                                            </div>
                                        </td>
                                    </tr>
                                );
                            })}
                        </tbody>
                    </table>
                </div>
            </div>

            <p className="flex items-start gap-2 text-xs text-muted-foreground">
                <ThumbsDown className="mt-0.5 size-3.5 shrink-0" />
                {t('unknownHint')}
            </p>
        </section>
    );
}
