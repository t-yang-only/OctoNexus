import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { AlertTriangle, Search } from 'lucide-react';
import { priceModelsQuery, priceUsageQuery, type PriceModelRow, type PriceUsageRow } from '@/api/price';

/** 价格与用量对比（T-price-001 / T-price-002） */
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

export function Price() {
    const t = useTranslations('price');
    const [tab, setTab] = useState<'models' | 'usage'>('models');
    const [keyword, setKeyword] = useState('');
    const models = useQuery(priceModelsQuery(keyword.trim()));
    const usage = useQuery(priceUsageQuery());

    const rows = models.data?.items ?? [];
    const usages = usage.data?.items ?? [];
    const anomalyCount = useMemo(() => usages.filter((item) => item.anomaly).length, [usages]);
    // 同一模型在各站的最低价，用来标出"这家更便宜"——用户要的就是这个对比
    const cheapest = useMemo(() => {
        const map = new Map<string, number>();
        rows.forEach((row) => {
            const key = row.model;
            const current = map.get(key);
            if (current === undefined || row.input_price < current) {
                map.set(key, row.input_price);
            }
        });
        return map;
    }, [rows]);

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
                                    <th className="p-2 text-right">{t('colMultiplier')}</th>
                                    <th className="p-2 text-left">{t('colModel')}</th>
                                    <th className="p-2 text-left">{t('colTier')}</th>
                                    <th className="p-2 text-right">{t('colActualInput')}</th>
                                    <th className="p-2 text-right">{t('colActualOutput')}</th>
                                    <th className="p-2 text-right">{t('colOfficialInput')}</th>
                                    <th className="p-2 text-right">{t('colOfficialOutput')}</th>
                                    <th className="p-2 text-right">{t('colCacheRead')}</th>
                                </tr>
                            </thead>
                            <tbody>
                                {rows.map((row: PriceModelRow, index) => {
                                    const isCheapest = cheapest.get(row.model) === row.input_price;
                                    return (
                                        <tr key={`${row.site}-${row.group_name}-${row.model}-${index}`} className="border-t">
                                            <td className="p-2">{row.site.replace(/^https?:\/\//, '')}</td>
                                            <td className="p-2">{row.group_name}</td>
                                            <td className="p-2 text-right">{row.multiplier}x</td>
                                            <td className="p-2 font-medium">{row.model}</td>
                                            <td className="p-2 opacity-70">{row.tier || '-'}</td>
                                            <td className={`p-2 text-right ${isCheapest ? 'font-semibold text-primary' : ''}`}>
                                                {money(row.input_price)}
                                            </td>
                                            <td className="p-2 text-right">{money(row.output_price)}</td>
                                            <td className="p-2 text-right opacity-70">{money(row.official_input)}</td>
                                            <td className="p-2 text-right opacity-70">{money(row.official_output)}</td>
                                            <td className="p-2 text-right opacity-70">{money(row.cache_read_price)}</td>
                                        </tr>
                                    );
                                })}
                            </tbody>
                        </table>
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
