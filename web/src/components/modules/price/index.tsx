import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { AlertTriangle, ArrowDown, ArrowUp, Search } from 'lucide-react';
import { priceModelsQuery, priceUsageQuery, type PriceModelRow, type PriceSortColumn, type PriceUsageRow } from '@/api/price';
import { EMPTY_LIST } from '@/lib/empty';

/** 价格与用量对比（T-price-001 / T-price-002 / 需求9 统一标准） */
function money(value: number) {
    if (!value) {
        return '0';
    }
    return Number(value.toFixed(4)).toString();
}

function compact(value: number) {
    if (!value) {
        return '0';
    }
    if (value >= 1e9) {
        return `${(value / 1e9).toFixed(2)}B`;
    }
    if (value >= 1e6) {
        return `${(value / 1e6).toFixed(2)}M`;
    }
    if (value >= 1e3) {
        return `${(value / 1e3).toFixed(1)}K`;
    }
    return String(value);
}

// SORTABLE 描述每个可排序列：key 是对外列名（传给后端白名单），
// labelKey 是三语文案键，numeric 决定比较方式（数字列按数值比，文本列按字符串比）。
export function Price() {
    const t = useTranslations('price');
    const [tab, setTab] = useState<'models' | 'usage'>('models');
    const [keyword, setKeyword] = useState('');
    // 排序状态：默认按人民币输入价从大到小 —— 用户要的就是"哪个最贵/最便宜一眼看出"。
    // 未配汇率时该列全为 0，后端会自动回落默认序（见 op.resolvePriceSort）。
    const [sort, setSort] = useState<{ column: PriceSortColumn; desc: boolean }>({ column: 'cny_input', desc: true });

    const models = useQuery(priceModelsQuery(keyword.trim(), sort));
    const usage = useQuery(priceUsageQuery());

    const rows = models.data?.items ?? EMPTY_LIST;
    const usages = usage.data?.items ?? EMPTY_LIST;
    const anomalyCount = useMemo(() => usages.filter((item) => item.anomaly).length, [usages]);
    // 同一模型在各站的人民币最低价，用来标出"这家更便宜"——用户要的就是这个对比。
    // 用人民币价而不是原币种价：统一标准之后这才是"我实际付出的钱"。
    const cheapest = useMemo(() => {
        const map = new Map<string, number>();
        rows.forEach((row) => {
            const price = row.cny_input_price || row.input_price;
            const key = row.model;
            const current = map.get(key);
            if (current === undefined || price < current) {
                map.set(key, price);
            }
        });
        return map;
    }, [rows]);

    const toggleSort = (column: PriceSortColumn) => {
        setSort((prev) => (prev.column === column ? { column, desc: !prev.desc } : { column, desc: true }));
    };

    const header = (key: PriceSortColumn, labelKey: string) => {
        const active = sort.column === key;
        return (
            <th
                key={key}
                className="cursor-pointer select-none p-2 text-right transition-colors hover:text-foreground"
                onClick={() => toggleSort(key)}
                aria-sort={active ? (sort.desc ? 'descending' : 'ascending') : 'none'}
            >
                <span className="inline-flex items-center gap-1">
                    {t(labelKey)}
                    {active &&
                        (sort.desc ? <ArrowDown className="size-3" /> : <ArrowUp className="size-3" />)}
                </span>
            </th>
        );
    };

    return (
        <div className="flex h-full min-h-0 flex-col gap-3 p-4">
            <div className="flex flex-wrap items-center gap-2">
                <button
                    type="button"
                    onClick={() => setTab('models')}
                    className={`rounded-md px-3 py-1.5 text-sm ${tab === 'models' ? 'bg-primary text-primary-foreground' : 'bg-muted'}`}
                >
                    {t('tabModels')}
                </button>
                <button
                    type="button"
                    onClick={() => setTab('usage')}
                    className={`rounded-md px-3 py-1.5 text-sm ${tab === 'usage' ? 'bg-primary text-primary-foreground' : 'bg-muted'}`}
                >
                    {t('tabUsage')}
                    {anomalyCount > 0 && (
                        <span className="ml-2 rounded bg-destructive px-1.5 py-0.5 text-xs text-destructive-foreground">
                            {anomalyCount}
                        </span>
                    )}
                </button>
                {tab === 'models' && (
                    <div className="ml-auto flex items-center gap-2 rounded-md border px-2 py-1">
                        <Search className="size-4 opacity-60" />
                        <input
                            value={keyword}
                            onChange={(event) => setKeyword(event.target.value)}
                            placeholder={t('searchPlaceholder')}
                            className="w-48 bg-transparent text-sm outline-none"
                        />
                    </div>
                )}
            </div>

            {tab === 'models' && (
                <div className="min-h-0 flex-1 overflow-auto rounded-md border">
                    {models.isLoading ? (
                        <p className="p-6 text-sm opacity-70">{t('loading')}</p>
                    ) : rows.length === 0 ? (
                        <p className="p-6 text-sm opacity-70">{t('empty')}</p>
                    ) : (
                        <table className="w-full border-collapse text-sm">
                            <thead className="sticky top-0 bg-muted">
                                <tr>
                                    <th className="p-2 text-left">{t('colSite')}</th>
                                    <th className="p-2 text-left">{t('colGroup')}</th>
                                    {header('multiplier', 'colMultiplier')}
                                    <th className="p-2 text-left">{t('colModel')}</th>
                                    <th className="p-2 text-left">{t('colTier')}</th>
                                    <th className="p-2 text-left">{t('colBilling')}</th>
                                    {header('input', 'colActualInput')}
                                    {header('output', 'colActualOutput')}
                                    {header('cache_read', 'colCacheRead')}
                                    {header('cny_input', 'colCnyInput')}
                                    {header('cny_output', 'colCnyOutput')}
                                    {header('per_call', 'colPerCall')}
                                    {header('official_in', 'colOfficialInput')}
                                    {header('official_out', 'colOfficialOutput')}
                                </tr>
                            </thead>
                            <tbody>
                                {rows.map((row: PriceModelRow, index) => {
                                    const comparable = row.cny_input_price || row.input_price;
                                    const isCheapest = cheapest.get(row.model) === comparable;
                                    return (
                                        <tr key={`${row.site}-${row.group_name}-${row.model}-${index}`} className="border-t align-top">
                                            <td className="p-2">{row.site.replace(/^https?:\/\//, '')}</td>
                                            <td className="p-2">{row.group_name}</td>
                                            <td className="p-2 text-right">{row.multiplier}x</td>
                                            <td className="p-2 font-medium">{row.model}</td>
                                            <td className="p-2 opacity-70">{row.tier || '-'}</td>
                                            <td className="p-2 opacity-70">
                                                {row.billing_mode === 'per_call' ? t('billingPerCall') : row.billing_mode === 'metered' ? t('billingMetered') : row.billing_mode === 'token' ? t('billingMetered') : row.billing_mode === 'subscription' ? t('billingSubscription') : t('billingUnknown')}
                                            </td>
                                            <td className={`p-2 text-right ${isCheapest ? 'font-semibold text-primary' : ''}`}>
                                                {money(row.input_price)}
                                            </td>
                                            <td className="p-2 text-right">{money(row.output_price)}</td>
                                            <td className="p-2 text-right opacity-70">{money(row.cache_read_price)}</td>
                                            {/* 人民币三列是"统一标准"的落点：汇率未配置时全为 0，
                                                此时显示「—」而不是 0 —— 0 会被读成"免费"。 */}
                                            <td className="p-2 text-right">{row.cny_input_price ? money(row.cny_input_price) : '—'}</td>
                                            <td className="p-2 text-right">{row.cny_output_price ? money(row.cny_output_price) : '—'}</td>
                                            <td className="p-2 text-right">{row.per_call_price ? `$${money(row.per_call_price)}` : '—'}</td>
                                            <td className="p-2 text-right opacity-70">{money(row.official_input)}</td>
                                            <td className="p-2 text-right opacity-70">{money(row.official_output)}</td>
                                        </tr>
                                    );
                                })}
                            </tbody>
                        </table>
                    )}
                    {/* 折算说明：按次行的 basis 说明它怎么折的，未配汇率时给出配置指引。
                        这不是装饰 —— 用户看到一列人民币价时，第一个问题就是"这数怎么来的"。 */}
                    {rows.length > 0 && (
                        <div className="border-t bg-muted/30 p-3 text-xs text-muted-foreground">
                            {rows.some((row) => row.basis) ? (
                                <ul className="grid gap-1">
                                    {rows
                                        .filter((row) => row.basis)
                                        .slice(0, 3)
                                        .map((row, index) => (
                                            <li key={`${row.site}-${row.model}-${index}`}>
                                                <span className="font-medium text-foreground">
                                                    {row.site.replace(/^https?:\/\//, '')} / {row.model}
                                                </span>
                                                ：{row.basis}
                                                {row.avg_tokens_per_request > 0 && `（分母 ${row.avg_tokens_per_request} token/次）`}
                                            </li>
                                        ))}
                                </ul>
                            ) : (
                                <p>{t('unifyHint')}</p>
                            )}
                        </div>
                    )}
                </div>
            )}

            {tab === 'usage' && (
                <div className="min-h-0 flex-1 overflow-auto rounded-md border">
                    {usage.isLoading ? (
                        <p className="p-6 text-sm opacity-70">{t('loading')}</p>
                    ) : usages.length === 0 ? (
                        <p className="p-6 text-sm opacity-70">{t('empty')}</p>
                    ) : (
                        <table className="w-full border-collapse text-sm">
                            <thead className="sticky top-0 bg-muted">
                                <tr>
                                    <th className="p-2 text-left">{t('colSite')}</th>
                                    <th className="p-2 text-right">{t('colRequests')}</th>
                                    <th className="p-2 text-right">{t('colInput')}</th>
                                    <th className="p-2 text-right">{t('colOutput')}</th>
                                    <th className="p-2 text-right">{t('colCacheRead')}</th>
                                    <th className="p-2 text-right">{t('colCacheWrite')}</th>
                                    <th className="p-2 text-right">{t('colTotal')}</th>
                                    <th className="p-2 text-right">{t('colAvgCost')}</th>
                                    <th className="p-2 text-right">{t('colBalance')}</th>
                                    <th className="p-2 text-left">{t('colAnomaly')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {usages.map((row: PriceUsageRow) => (
                                    <tr key={row.site} className="border-t">
                                        <td className="p-2">{row.site.replace(/^https?:\/\//, '')}</td>
                                        <td className="p-2 text-right">{compact(row.requests)}</td>
                                        <td className="p-2 text-right">{compact(row.input_tokens)}</td>
                                        <td className="p-2 text-right">{compact(row.output_tokens)}</td>
                                        <td className="p-2 text-right">{compact(row.cache_read_tokens)}</td>
                                        <td className="p-2 text-right">{compact(row.cache_write_tokens)}</td>
                                        <td className="p-2 text-right">{compact(row.total_tokens)}</td>
                                        <td className="p-2 text-right">${row.avg_cost_per_request.toFixed(5)}</td>
                                        <td className="p-2 text-right">${row.balance.toFixed(2)}</td>
                                        <td className="p-2">
                                            {row.anomaly ? (
                                                <span className="inline-flex items-center gap-1 text-destructive">
                                                    <AlertTriangle className="size-3.5" />
                                                    {row.anomaly}
                                                </span>
                                            ) : (
                                                <span className="opacity-50">-</span>
                                            )}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    )}
                </div>
            )}
        </div>
    );
}
