import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
    Activity,
    AlertTriangle,
    ChevronDown,
    ChevronLeft,
    ChevronRight,
    ChevronUp,
    Download,
    Loader2,
    Power,
    RefreshCw,
    RotateCw,
    Search,
    Boxes,
} from 'lucide-react';
import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import {
    poolEntriesQueryOptions,
    poolExportURL,
    poolKindsQueryOptions,
    poolProbeEntry,
    poolRefreshEntry,
    poolSetEntryEnabled,
    poolSummaryQueryOptions,
    poolSyncKind,
    type PoolEntry,
    type PoolKindInfo,
} from '@/api/pool';

// Pool 是号池统一视图页面。
//
// 它刻意不假设池子里有哪几种后端：页面先问 /pool/kinds 拿到"有哪些后端、各自声明了什么能力位"，
// 再按能力位决定按钮是否出现。以后接入新的反代工具包，这一页不用改 —— 这是接口面按适配器
// 自描述设计的意义所在（页面是第一个吃自己狗粮的调用方）。
export function Pool() {
    const t = useTranslations('unifiedPool');
    const queryClient = useQueryClient();

    const [kind, setKind] = useState(''); // 空串=全部后端
    const [search, setSearch] = useState('');
    const [query, setQuery] = useState(''); // 防抖后的关键字
    const [status, setStatus] = useState(''); // '' | 'enabled' | 'disabled' | 'unhealthy'
    const [sort, setSort] = useState('kind');
    const [desc, setDesc] = useState(false);
    const [limit, setLimit] = useState(20);
    const [offset, setOffset] = useState(0);
    const [busy, setBusy] = useState(''); // 正在进行的动作标识，避免重复点击
    const [notice, setNotice] = useState<{ tone: 'ok' | 'bad'; text: string } | null>(null);
    const [expanded, setExpanded] = useState('');

    // 关键字防抖：输入停顿 300ms 再发请求，避免每敲一个字打一次后端。
    // 关键字真的变了才归零页码（用 ref 比较，别把 setOffset 塞进 setState 更新函数里）。
    const committedQuery = useRef('');
    useEffect(() => {
        const timer = setTimeout(() => {
            const next = search.trim();
            if (next === committedQuery.current) return;
            committedQuery.current = next;
            setQuery(next);
            setOffset(0);
        }, 300);
        return () => clearTimeout(timer);
    }, [search]);

    const kinds = useQuery(poolKindsQueryOptions);
    const summary = useQuery(poolSummaryQueryOptions);
    const filter = useMemo(() => {
        const next: Parameters<typeof poolEntriesQueryOptions>[0] = { kind, q: query, sort, desc, limit, offset };
        if (status === 'enabled') next.enabled = true;
        if (status === 'disabled') next.enabled = false;
        if (status === 'unhealthy') next.healthy = false;
        return next;
    }, [kind, query, status, sort, desc, limit, offset]);
    const entries = useQuery(poolEntriesQueryOptions(filter));

    const kindInfos = useMemo(() => kinds.data?.items ?? [], [kinds.data]);
    const kindByID = useMemo(() => {
        const map = new Map<string, PoolKindInfo>();
        for (const info of kindInfos) map.set(info.kind, info);
        return map;
    }, [kindInfos]);
    const currentKind = kind ? kindByID.get(kind) : undefined;
    const canSync = Boolean(currentKind?.capabilities.includes('sync'));
    const items = entries.data?.items ?? [];
    const warnings = entries.data?.warnings ?? [];
    const kindErrors = summary.data?.kind_errors ?? [];

    // invalidate 让列表与汇总一起刷新：启停/同步/探活都会改变计数，只刷一半会前后不一致。
    const invalidate = () => {
        void queryClient.invalidateQueries({ queryKey: ['pool', 'entries'] });
        void queryClient.invalidateQueries({ queryKey: ['pool', 'summary'] });
        void queryClient.invalidateQueries({ queryKey: ['pool', 'stats'] });
    };

    const runAction = async (key: string, action: () => Promise<string>) => {
        if (busy) return;
        setBusy(key);
        setNotice(null);
        try {
            const text = await action();
            setNotice({ tone: 'ok', text });
            invalidate();
        } catch (error) {
            setNotice({ tone: 'bad', text: error instanceof Error ? error.message : String(error) });
        } finally {
            setBusy('');
        }
    };

    const toggleSort = (field: string) => {
        if (sort === field) {
            setDesc(!desc);
            return;
        }
        setSort(field);
        setDesc(false);
    };

    const sortMark = (field: string) => (sort === field ? (desc ? <ChevronDown size={12} /> : <ChevronUp size={12} />) : null);

    const formatTime = (value?: string) => {
        if (!value) return '—';
        const date = new Date(value);
        if (Number.isNaN(date.getTime())) return value;
        return date.toLocaleString();
    };

    const entryKey = (entry: PoolEntry) => `${entry.kind}:${entry.id}`;

    const cards: Array<{ label: string; value: number | undefined; tone?: 'bad' | 'warn' }> = [
        { label: t('total'), value: summary.data?.total },
        { label: t('enabled'), value: summary.data?.enabled },
        { label: t('disabled'), value: summary.data?.disabled },
        { label: t('healthy'), value: summary.data?.healthy },
        { label: t('withError'), value: summary.data?.with_error, tone: 'bad' },
        { label: t('expiring'), value: summary.data?.expiring_soon, tone: 'warn' },
    ];

    return (
        <div className="flex h-full min-h-0 flex-col gap-4">
            <header className="flex flex-wrap items-start justify-between gap-3">
                <div>
                    <h1 className="flex items-center gap-2 text-xl font-semibold">
                        <Boxes size={20} />
                        {t('title')}
                    </h1>
                    <p className="mt-1 max-w-3xl text-xs leading-relaxed text-muted-foreground">{t('subtitle')}</p>
                </div>
                <div className="flex items-center gap-2">
                    <a
                        className="flex items-center gap-1 rounded-xl border border-border px-3 py-2 text-xs hover:bg-muted"
                        href={poolExportURL('csv', kind)}
                    >
                        <Download size={14} />
                        {t('exportCsv')}
                    </a>
                    <button
                        className="flex items-center gap-1 rounded-xl border border-border px-3 py-2 text-xs hover:bg-muted"
                        disabled={entries.isFetching}
                        onClick={() => invalidate()}
                        type="button"
                    >
                        {entries.isFetching ? <Loader2 className="animate-spin" size={14} /> : <RefreshCw size={14} />}
                        {t('refresh')}
                    </button>
                </div>
            </header>

            <section className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
                {cards.map((card) => (
                    <article className="rounded-2xl border border-border bg-card p-3 text-card-foreground" key={card.label}>
                        <div className="text-xs text-muted-foreground">{card.label}</div>
                        <div
                            className={`mt-1 text-lg font-semibold ${
                                card.tone === 'bad' && (card.value ?? 0) > 0
                                    ? 'text-destructive'
                                    : card.tone === 'warn' && (card.value ?? 0) > 0
                                      ? 'text-accent'
                                      : ''
                            }`}
                        >
                            {card.value ?? '—'}
                        </div>
                    </article>
                ))}
            </section>

            <section className="flex flex-wrap items-center gap-2">
                <div className="flex flex-wrap items-center gap-1 rounded-2xl border border-border bg-card p-1 text-card-foreground">
                    <button
                        className={`rounded-xl px-3 py-1.5 text-xs ${kind === '' ? 'bg-primary text-primary-foreground' : 'hover:bg-muted'}`}
                        onClick={() => {
                            setKind('');
                            setOffset(0);
                        }}
                        type="button"
                    >
                        {t('allKinds')}
                    </button>
                    {kindInfos.map((info) => (
                        <button
                            className={`rounded-xl px-3 py-1.5 text-xs ${
                                kind === info.kind ? 'bg-primary text-primary-foreground' : 'hover:bg-muted'
                            }`}
                            key={info.kind}
                            onClick={() => {
                                setKind(info.kind);
                                setOffset(0);
                            }}
                            title={info.capabilities.join(' / ')}
                            type="button"
                        >
                            {info.title}
                        </button>
                    ))}
                    {kinds.isLoading && <Loader2 className="mx-2 animate-spin" size={14} />}
                </div>

                <label className="flex items-center gap-1 rounded-xl border border-border bg-card px-3 py-2 text-xs text-card-foreground">
                    <Search size={14} className="text-muted-foreground" />
                    <input
                        className="w-40 bg-transparent outline-none placeholder:text-muted-foreground"
                        onChange={(event) => setSearch(event.target.value)}
                        placeholder={t('searchPlaceholder')}
                        value={search}
                    />
                </label>

                <select
                    className="rounded-xl border border-border bg-card px-3 py-2 text-xs text-card-foreground"
                    onChange={(event) => {
                        setStatus(event.target.value);
                        setOffset(0);
                    }}
                    value={status}
                >
                    <option value="">{t('statusAll')}</option>
                    <option value="enabled">{t('statusEnabled')}</option>
                    <option value="disabled">{t('statusDisabled')}</option>
                    <option value="unhealthy">{t('statusUnhealthy')}</option>
                </select>

                {kind && (
                    <button
                        className="flex items-center gap-1 rounded-xl border border-border bg-card px-3 py-2 text-xs text-card-foreground hover:bg-muted disabled:opacity-50"
                        disabled={!canSync || busy !== ''}
                        onClick={() =>
                            runAction(`sync:${kind}`, async () => {
                                const report = await poolSyncKind(kind);
                                return `${t('syncDone')}: ${report.entries}`;
                            })
                        }
                        title={canSync ? t('sync') : t('notSupported')}
                        type="button"
                    >
                        {busy === `sync:${kind}` ? <Loader2 className="animate-spin" size={14} /> : <RotateCw size={14} />}
                        {t('sync')}
                    </button>
                )}
            </section>

            {notice && (
                <div
                    className={`rounded-xl border px-3 py-2 text-xs ${
                        notice.tone === 'ok' ? 'border-border text-muted-foreground' : 'border-destructive text-destructive'
                    }`}
                >
                    {notice.text}
                </div>
            )}

            {(warnings.length > 0 || kindErrors.length > 0) && (
                <div className="flex items-start gap-2 rounded-xl border border-destructive/40 px-3 py-2 text-xs text-destructive">
                    <AlertTriangle size={14} className="mt-0.5 shrink-0" />
                    <div className="flex flex-col gap-0.5">
                        {[...kindErrors, ...warnings].map((warning) => (
                            <span key={`${warning.kind}:${warning.error}`}>
                                {warning.kind}: {warning.error}
                            </span>
                        ))}
                    </div>
                </div>
            )}

            <section className="min-h-0 flex-1 overflow-auto rounded-2xl border border-border bg-card text-card-foreground">
                <table className="w-full text-left text-xs">
                    <thead className="sticky top-0 z-10 bg-card text-muted-foreground">
                        <tr className="border-b border-border">
                            <th className="cursor-pointer px-3 py-2 font-medium" onClick={() => toggleSort('name')}>
                                <span className="inline-flex items-center gap-1">
                                    {t('name')}
                                    {sortMark('name')}
                                </span>
                            </th>
                            <th className="cursor-pointer px-3 py-2 font-medium" onClick={() => toggleSort('kind')}>
                                <span className="inline-flex items-center gap-1">
                                    {t('kind')}
                                    {sortMark('kind')}
                                </span>
                            </th>
                            <th className="px-3 py-2 font-medium">{t('provider')}</th>
                            <th className="cursor-pointer px-3 py-2 font-medium" onClick={() => toggleSort('status')}>
                                <span className="inline-flex items-center gap-1">
                                    {t('status')}
                                    {sortMark('status')}
                                </span>
                            </th>
                            <th className="px-3 py-2 font-medium">{t('health')}</th>
                            <th className="cursor-pointer px-3 py-2 font-medium" onClick={() => toggleSort('expires')}>
                                <span className="inline-flex items-center gap-1">
                                    {t('expires')}
                                    {sortMark('expires')}
                                </span>
                            </th>
                            <th className="px-3 py-2 font-medium">{t('actions')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {entries.isLoading && (
                            <tr>
                                <td className="px-3 py-6 text-center text-muted-foreground" colSpan={7}>
                                    <Loader2 className="mx-auto animate-spin" size={16} />
                                </td>
                            </tr>
                        )}
                        {!entries.isLoading && items.length === 0 && (
                            <tr>
                                <td className="px-3 py-6 text-center text-muted-foreground" colSpan={7}>
                                    <div>{t('empty')}</div>
                                    <div className="mt-1">{t('emptyHint')}</div>
                                </td>
                            </tr>
                        )}
                        {items.map((entry) => {
                            const key = entryKey(entry);
                            const info = kindByID.get(entry.kind);
                            const canToggle = Boolean(info?.capabilities.includes('toggle'));
                            const canProbe = Boolean(info?.capabilities.includes('probe'));
                            const canRefresh = Boolean(info?.capabilities.includes('refresh'));
                            const isOpen = expanded === key;
                            return [
                                <tr className="border-b border-border/60 align-middle" key={key}>
                                    <td className="px-3 py-2">
                                        <div className="flex items-center gap-1">
                                            <button
                                                className="text-muted-foreground hover:text-card-foreground"
                                                onClick={() => setExpanded(isOpen ? '' : key)}
                                                type="button"
                                            >
                                                {isOpen ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                                            </button>
                                            <div className="min-w-0">
                                                <div className="truncate font-medium">{entry.name || entry.id}</div>
                                                <div className="truncate text-muted-foreground">{entry.id}</div>
                                            </div>
                                        </div>
                                    </td>
                                    <td className="px-3 py-2">
                                        <span className="rounded-lg border border-border px-2 py-0.5">{info?.title ?? entry.kind}</span>
                                    </td>
                                    <td className="px-3 py-2 text-muted-foreground">{entry.provider || '—'}</td>
                                    <td className="px-3 py-2">
                                        <span className={entry.status === 'active' ? '' : 'text-accent'}>{entry.status}</span>
                                    </td>
                                    <td className="px-3 py-2">
                                        <span className={entry.healthy ? 'inline-flex items-center gap-1' : 'inline-flex items-center gap-1 text-destructive'}>
                                            <Activity size={12} />
                                            {entry.healthy ? t('healthyYes') : t('healthyNo')}
                                        </span>
                                        {entry.last_error && (
                                            <div className="mt-0.5 max-w-[16rem] truncate text-destructive" title={entry.last_error}>
                                                {entry.last_error}
                                            </div>
                                        )}
                                    </td>
                                    <td className="px-3 py-2 text-muted-foreground">{formatTime(entry.expires_at)}</td>
                                    <td className="px-3 py-2">
                                        <div className="flex items-center gap-1">
                                            <button
                                                className={`flex items-center gap-1 rounded-lg border px-2 py-1 ${
                                                    entry.enabled ? 'border-primary text-primary' : 'border-border text-muted-foreground'
                                                } disabled:opacity-40`}
                                                disabled={!canToggle || busy !== ''}
                                                onClick={() =>
                                                    runAction(`${entry.kind}:${entry.id}:toggle`, async () => {
                                                        await poolSetEntryEnabled(entry.kind, entry.id, !entry.enabled);
                                                        return entry.enabled ? t('disableOk') : t('enableOk');
                                                    })
                                                }
                                                title={canToggle ? (entry.enabled ? t('disable') : t('enable')) : t('notSupported')}
                                                type="button"
                                            >
                                                {busy === `${entry.kind}:${entry.id}:toggle` ? (
                                                    <Loader2 className="animate-spin" size={12} />
                                                ) : (
                                                    <Power size={12} />
                                                )}
                                                {entry.enabled ? t('disable') : t('enable')}
                                            </button>
                                            <button
                                                className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-muted-foreground hover:bg-muted disabled:opacity-40"
                                                disabled={!canProbe || busy !== ''}
                                                onClick={() =>
                                                    runAction(`${entry.kind}:${entry.id}:probe`, async () => {
                                                        await poolProbeEntry(entry.kind, entry.id);
                                                        return t('probeOk');
                                                    })
                                                }
                                                title={canProbe ? t('probe') : t('notSupported')}
                                                type="button"
                                            >
                                                {busy === `${entry.kind}:${entry.id}:probe` ? (
                                                    <Loader2 className="animate-spin" size={12} />
                                                ) : (
                                                    <Activity size={12} />
                                                )}
                                                {t('probe')}
                                            </button>
                                            <button
                                                className="flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-muted-foreground hover:bg-muted disabled:opacity-40"
                                                disabled={!canRefresh || busy !== ''}
                                                onClick={() =>
                                                    runAction(`${entry.kind}:${entry.id}:refresh`, async () => {
                                                        await poolRefreshEntry(entry.kind, entry.id);
                                                        return t('refreshOk');
                                                    })
                                                }
                                                title={canRefresh ? t('refreshOne') : t('notSupported')}
                                                type="button"
                                            >
                                                {busy === `${entry.kind}:${entry.id}:refresh` ? (
                                                    <Loader2 className="animate-spin" size={12} />
                                                ) : (
                                                    <RotateCw size={12} />
                                                )}
                                            </button>
                                        </div>
                                    </td>
                                </tr>,
                                isOpen && (
                                    <tr className="border-b border-border/60 bg-muted/30" key={`${key}:detail`}>
                                        <td className="px-3 py-3" colSpan={7}>
                                            <div className="grid gap-3 md:grid-cols-2">
                                                <div>
                                                    <div className="mb-1 text-muted-foreground">{t('detail')}</div>
                                                    <dl className="grid grid-cols-[8rem_1fr] gap-x-2 gap-y-1">
                                                        {Object.entries(entry.detail ?? {}).map(([field, value]) => (
                                                            <div className="col-span-2 grid grid-cols-[8rem_1fr] gap-x-2" key={field}>
                                                                <dt className="truncate text-muted-foreground">{field}</dt>
                                                                <dd className="break-all">{String(value)}</dd>
                                                            </div>
                                                        ))}
                                                        {Object.keys(entry.detail ?? {}).length === 0 && (
                                                            <div className="text-muted-foreground">—</div>
                                                        )}
                                                    </dl>
                                                </div>
                                                <div>
                                                    <div className="mb-1 text-muted-foreground">{t('labels')}</div>
                                                    <dl className="grid grid-cols-[8rem_1fr] gap-x-2 gap-y-1">
                                                        {Object.entries(entry.labels ?? {}).map(([field, value]) => (
                                                            <div className="col-span-2 grid grid-cols-[8rem_1fr] gap-x-2" key={field}>
                                                                <dt className="truncate text-muted-foreground">{field}</dt>
                                                                <dd className="break-all">{value}</dd>
                                                            </div>
                                                        ))}
                                                        {Object.keys(entry.labels ?? {}).length === 0 && (
                                                            <div className="text-muted-foreground">—</div>
                                                        )}
                                                    </dl>
                                                </div>
                                            </div>
                                        </td>
                                    </tr>
                                ),
                            ];
                        })}
                    </tbody>
                </table>
            </section>

            <footer className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                <div>
                    {t('pageInfo')} {entries.data?.total ?? 0} · {t('scanned')} {entries.data?.scanned ?? 0} ·{' '}
                    {t('returned')} {entries.data?.returned ?? 0}
                </div>
                <div className="flex items-center gap-2">
                    <select
                        className="rounded-xl border border-border bg-card px-2 py-1 text-xs text-card-foreground"
                        onChange={(event) => {
                            setLimit(Number(event.target.value));
                            setOffset(0);
                        }}
                        value={limit}
                    >
                        {[20, 50, 100, 200].map((size) => (
                            <option key={size} value={size}>
                                {size}
                            </option>
                        ))}
                    </select>
                    <button
                        className="flex items-center gap-1 rounded-xl border border-border px-2 py-1 disabled:opacity-40"
                        disabled={offset === 0}
                        onClick={() => setOffset(Math.max(0, offset - limit))}
                        type="button"
                    >
                        <ChevronLeft size={14} />
                        {t('prev')}
                    </button>
                    <button
                        className="flex items-center gap-1 rounded-xl border border-border px-2 py-1 disabled:opacity-40"
                        disabled={(entries.data?.returned ?? 0) < limit}
                        onClick={() => setOffset(offset + limit)}
                        type="button"
                    >
                        {t('next')}
                        <ChevronRight size={14} />
                    </button>
                </div>
            </footer>
        </div>
    );
}
