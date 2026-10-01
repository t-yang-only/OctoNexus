import { useMemo, useState } from 'react';
import {
    Activity,
    AlertTriangle,
    Ban,
    ChevronDown,
    ChevronUp,
    Gauge,
    Layers,
    Search,
    Snail,
    ThumbsDown,
    TimerOff,
    Zap,
} from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useAllocationMonitor, type AllocationRow } from '@/api/allocmon';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';
import { Input } from '@/components/ui/input';
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

// 「分不到流量」的排前面：不可用 → 限流 → 冷却 → 自限流让开 → 正常。
const STATE_RANK: Record<string, number> = {
    unavailable: 0,
    throttled: 1,
    cooling: 2,
    limited: 3,
    ok: 4,
};

type SortKey =
    | 'state'
    | 'member'
    | 'requests'
    | 'weight'
    | 'health'
    | 'recent'
    | 'success'
    | 'latency'
    | 'speed';

type StateFilter = 'all' | 'problem' | 'ok';

function sortValueOf(row: AllocationRow, key: SortKey): number | string {
    switch (key) {
        case 'member':
            return (row.model_name || row.channel_name || '').toLowerCase();
        case 'requests':
            return row.requests;
        case 'weight':
            return row.weight;
        case 'health':
            return row.health_factor;
        case 'recent':
            return row.recent_reqs;
        case 'success':
            return row.success_rate;
        case 'latency':
            return row.latency_ms;
        case 'speed':
            return row.tokens_per_sec;
        default:
            return 0;
    }
}

// 这一列对这个成员**有没有值**。没有值的行恒排最后，且不随排序方向翻转——
// 「没有数据」不是「值很小」：升序时把它们顶到最前会被读成"最需要关注"，
// 而实际上我们只是还没测到（与面板里「未知 ≠ 0」是同一条纪律）。
function sortMissing(row: AllocationRow, key: SortKey): boolean {
    switch (key) {
        case 'requests':
            return !row.known;
        case 'success':
            return !row.has_samples;
        case 'latency':
            return row.latency_ms <= 0;
        case 'speed':
            return row.speed_samples <= 0;
        default:
            return false;
    }
}

function SortHeader({
    label,
    col,
    sortKey,
    sortAsc,
    onSort,
    alignRight,
}: {
    label: string;
    col: SortKey;
    sortKey: SortKey;
    sortAsc: boolean;
    onSort: (key: SortKey) => void;
    alignRight?: boolean;
}) {
    const active = sortKey === col;
    return (
        <th
            aria-sort={active ? (sortAsc ? 'ascending' : 'descending') : 'none'}
            className={`py-2 pr-2 font-medium ${alignRight ? 'text-right' : 'text-left'}`}
        >
            <button
                type="button"
                onClick={() => onSort(col)}
                className={`inline-flex items-center gap-0.5 transition-colors hover:text-foreground ${
                    active ? 'text-foreground' : ''
                }`}
            >
                {label}
                {active &&
                    (sortAsc ? (
                        <ChevronUp className="size-3 shrink-0" />
                    ) : (
                        <ChevronDown className="size-3 shrink-0" />
                    ))}
            </button>
        </th>
    );
}

export function AllocationMonitor() {
    const t = useTranslations('home.allocation');
    const { data, isLoading } = useAllocationMonitor();
    // rows 用 useMemo 固定引用: 下面两处派生计算都依赖它, 每次渲染新数组会让 useMemo 每次都失效。
    const rows = useMemo(() => data?.rows ?? [], [data]);
    const summary = data?.summary;
    const settings = data?.settings;

    // 782 个成员里绝大多数状态完全相同（实测生产 782 个全是"正常"），
    // 铺成一整列之后既找不到想看的那个、也看不出有没有问题。
    // 所以给三个入口：按名字找、按状态筛、按列排序。
    const [filter, setFilter] = useState('');
    const [stateFilter, setStateFilter] = useState<StateFilter>('all');
    const [sortKey, setSortKey] = useState<SortKey>('state');
    const [sortAsc, setSortAsc] = useState(true);

    // 权重条以本分组内的最大权重为满格：分压是"组内相对比例"，跨组比较没有意义。
    const maxWeightByGroup = useMemo(() => {
        const map = new Map<number, number>();
        for (const row of rows) {
            const current = map.get(row.group_id) ?? 0;
            if (row.weight > current) map.set(row.group_id, row.weight);
        }
        return map;
    }, [rows]);

    // 异常数本身就是结论：点开"异常"看到 0 条，比滚动 782 行去确认要快得多。
    const stateCounts = useMemo(() => {
        let ok = 0;
        for (const row of rows) {
            if (stateOf(row) === 'ok') ok += 1;
        }
        return { ok, problem: rows.length - ok };
    }, [rows]);

    const visible = useMemo(() => {
        const q = filter.trim().toLowerCase();
        const hit = (row: AllocationRow) => {
            const state = stateOf(row);
            if (stateFilter === 'problem' && state === 'ok') return false;
            if (stateFilter === 'ok' && state !== 'ok') return false;
            if (!q) return true;
            return [row.model_name, row.channel_name, row.key_name, row.group_name].some((v) =>
                (v || '').toLowerCase().includes(q),
            );
        };
        const dir = sortAsc ? 1 : -1;
        return rows.filter(hit).sort((a, b) => {
            const am = sortMissing(a, sortKey);
            const bm = sortMissing(b, sortKey);
            if (am !== bm) return am ? 1 : -1;
            if (am && bm) return a.item_id - b.item_id;

            let diff = 0;
            if (sortKey === 'state') {
                diff = STATE_RANK[stateOf(a)] - STATE_RANK[stateOf(b)];
            } else {
                const av = sortValueOf(a, sortKey);
                const bv = sortValueOf(b, sortKey);
                diff =
                    typeof av === 'string' || typeof bv === 'string'
                        ? String(av).localeCompare(String(bv))
                        : av - bv;
            }
            if (diff !== 0) return diff * dir;
            // 同值时按权重降序兜底，保证每次渲染顺序稳定（表格不会自己跳）。
            if (a.weight !== b.weight) return b.weight - a.weight;
            return a.item_id - b.item_id;
        });
    }, [rows, filter, stateFilter, sortKey, sortAsc]);

    const toggleSort = (key: SortKey) => {
        if (key === sortKey) {
            setSortAsc((v) => !v);
            return;
        }
        setSortKey(key);
        // 文本列 A→Z；「剩余请求数」默认升序（先看谁快用完了，这才是要行动的）；
        // 其余数值列默认降序（先看最大的）。
        setSortAsc(key === 'member' || key === 'requests');
    };

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

    const limitText = (value: number) => (value > 0 ? String(value) : t('settings.off'));

    const chips: { key: StateFilter; label: string; count: number }[] = [
        { key: 'all', label: t('filter.all'), count: rows.length },
        { key: 'problem', label: t('filter.problem'), count: stateCounts.problem },
        { key: 'ok', label: t('filter.ok'), count: stateCounts.ok },
    ];
    const filtering = filter.trim() !== '' || stateFilter !== 'all';

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
                <div className="flex flex-wrap items-center justify-between gap-2 px-4 pt-3">
                    <h4 className="text-sm font-medium">{t('table.title')}</h4>
                    <div className="flex flex-wrap items-center gap-1.5">
                        {chips.map((chip) => {
                            const active = stateFilter === chip.key;
                            return (
                                <button
                                    key={chip.key}
                                    type="button"
                                    onClick={() => setStateFilter(chip.key)}
                                    aria-pressed={active}
                                    className={`rounded-full border px-2.5 py-0.5 text-xs tabular-nums transition-colors ${
                                        active
                                            ? 'border-primary/40 bg-primary/10 text-foreground'
                                            : 'border-border/50 text-muted-foreground hover:text-foreground'
                                    }`}
                                >
                                    {chip.label} {chip.count}
                                </button>
                            );
                        })}
                        {filtering && (
                            <button
                                type="button"
                                onClick={() => {
                                    setFilter('');
                                    setStateFilter('all');
                                }}
                                className="rounded-full border border-border/50 px-2.5 py-0.5 text-xs text-muted-foreground transition-colors hover:text-foreground"
                            >
                                {t('filter.clear')}
                            </button>
                        )}
                    </div>
                </div>

                <div className="flex flex-wrap items-center gap-2 px-4 pt-2">
                    <div className="relative min-w-56 flex-1">
                        <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                        <Input
                            value={filter}
                            onChange={(e) => setFilter(e.target.value)}
                            placeholder={t('filter.placeholder')}
                            className="pl-9"
                        />
                    </div>
                    {filtering && (
                        <span className="text-xs text-muted-foreground tabular-nums">
                            {t('filter.showing', { shown: visible.length, total: rows.length })}
                        </span>
                    )}
                </div>

                <div className="max-h-100 overflow-y-auto overflow-x-auto px-4 pb-3 pt-2">
                    <table className="w-full min-w-160 text-sm">
                        <thead className="sticky top-0 bg-card">
                            <tr className="text-left text-xs text-muted-foreground">
                                <SortHeader label={t('columns.member')} col="member" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} />
                                <SortHeader label={t('columns.remaining')} col="requests" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} alignRight />
                                <SortHeader label={t('columns.weight')} col="weight" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} />
                                <SortHeader label={t('columns.health')} col="health" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} alignRight />
                                <SortHeader label={t('columns.recent')} col="recent" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} alignRight />
                                <SortHeader label={t('columns.success')} col="success" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} alignRight />
                                <SortHeader label={t('columns.latency')} col="latency" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} alignRight />
                                <SortHeader label={t('columns.speed')} col="speed" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} alignRight />
                                <SortHeader label={t('columns.state')} col="state" sortKey={sortKey} sortAsc={sortAsc} onSort={toggleSort} />
                            </tr>
                        </thead>
                        <tbody>
                            {visible.length === 0 ? (
                                // 筛到空要明说是"筛没了"，不能留白——留白会被读成"没有成员"。
                                <tr className="border-t border-border/40">
                                    <td colSpan={9} className="py-8 text-center text-sm text-muted-foreground">
                                        {t('filter.noMatch')}
                                    </td>
                                </tr>
                            ) : (
                                visible.map((row) => {
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
                                })
                            )}
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
