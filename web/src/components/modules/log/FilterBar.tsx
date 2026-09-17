import { useMemo, useState } from 'react';
import { Download, RotateCcw, Search, SlidersHorizontal, X } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { exportRelayLogs, type RelayLogOverview, type RequestState } from '@/api/log';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { buttonVariants } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import { matchLogMemoryFilter, type LogMemoryFilter } from './filter';
export type { LogMemoryFilter };
import {
    LOG_AUTO_REFRESH_OPTIONS,
    useLogAutoRefreshStore,
    useLogFieldVisibilityStore,
    type LogFieldName,
} from './store';

// LOG_FIELD_LABEL_KEYS 是十个可见性开关的文案键, 顺序即弹窗内排列顺序。
const LOG_FIELD_LABEL_KEYS: Array<{ field: LogFieldName; labelKey: string }> = [
    { field: 'time', labelKey: 'time' },
    { field: 'apiKey', labelKey: 'apiKey' },
    { field: 'duration', labelKey: 'duration' },
    { field: 'firstByte', labelKey: 'firstByte' },
        { field: 'attempts', labelKey: 'attempts' },
        { field: 'decision', labelKey: 'decision' },
    { field: 'cost', labelKey: 'cost' },
    { field: 'tps', labelKey: 'tps' },
    { field: 'cacheHitRate', labelKey: 'cacheHitRate' },
    { field: 'prompt', labelKey: 'prompt' },
    { field: 'cached', labelKey: 'cached' },
    { field: 'completion', labelKey: 'completion' },
];

interface LogToolbarProps {
    filter: LogMemoryFilter; // 当前内存筛选条件。
    onFilterChange: (filter: LogMemoryFilter) => void; // 更新内存筛选条件。
}

// useFilteredLogs 对 SSE 内存列表做本地过滤, 输入引用不变时返回同一数组引用以跳过重渲染。
export function useFilteredLogs(logs: RelayLogOverview[], filter: LogMemoryFilter): RelayLogOverview[] {
    return useMemo(() => {
        if (filter.status === 'all' && !filter.query.trim()) return logs;
        return logs.filter((log) => matchLogMemoryFilter(log, filter));
    }, [logs, filter]);
}

// LogToolbar 渲染日志页的内存筛选栏与字段可见性/自动刷新偏好弹窗。
// 筛选只在前端内存列表上执行, 不经过后端; 持久化筛选等 relay_logs 分页口径稳定后再接。
export function LogToolbar({ filter, onFilterChange }: LogToolbarProps) {
    const t = useTranslations('log.list');
    const tf = useTranslations('log.filter');
    const visibility = useLogFieldVisibilityStore((s) => s.visibility);
    const toggleField = useLogFieldVisibilityStore((s) => s.toggleField);
    const resetFields = useLogFieldVisibilityStore((s) => s.resetFields);
    const interval = useLogAutoRefreshStore((s) => s.interval);
    const setInterval = useLogAutoRefreshStore((s) => s.setInterval);
    const [searchExpanded, setSearchExpanded] = useState(false);
    const [exporting, setExporting] = useState(false);

    // runExport 导出持久化历史里的请求级明细（CSV）。内存筛选栏里的状态与关键字直接映射到后端查询参数,
    // 保证"屏幕上筛出来的"和"导出去的"是同一套条件。
    const runExport = () => {
        setExporting(true);
        exportRelayLogs({
            status: filter.status === 'all' ? undefined : filter.status,
            q: filter.query.trim() || undefined,
        })
            .then((size) => {
                if (size === 0) toast.info(t('exportEmpty'));
                else toast.success(t('exported'));
            })
            .catch((error) => toast.error(`${t('exportFailed')}: ${String(error)}`))
            .finally(() => setExporting(false));
    };

    const statuses: Array<RequestState | 'all'> = ['all', 'running', 'committed', 'success', 'failed', 'canceled'];

    return (
        <div className="flex shrink-0 items-center gap-2">
            <div className="relative h-9 w-9">
                <div
                    className={cn(
                        'absolute right-0 top-0 flex h-9 items-center gap-2 overflow-hidden rounded-xl border transition-[width,padding,border-color] duration-200 ease-linear',
                        searchExpanded ? 'w-39 border-border px-3' : 'w-9 border-transparent px-0'
                    )}
                >
                    <button
                        type="button"
                        aria-label={t('search')}
                        onClick={() => setSearchExpanded(true)}
                        className={cn(
                            'flex h-full shrink-0 items-center justify-center text-muted-foreground transition-colors hover:text-foreground',
                            searchExpanded ? 'w-4 cursor-default' : 'w-9'
                        )}
                    >
                        <Search className="size-4" />
                    </button>
                    <input
                        type="text"
                        value={filter.query}
                        onChange={(event) => onFilterChange({ ...filter, query: event.target.value })}
                        tabIndex={searchExpanded ? 0 : -1}
                        placeholder={t('searchPlaceholder')}
                        className="w-20 shrink-0 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
                    />
                    <button
                        type="button"
                        tabIndex={searchExpanded ? 0 : -1}
                        aria-label={t('clearSearch')}
                        onClick={() => {
                            onFilterChange({ ...filter, query: '' });
                            setSearchExpanded(false);
                        }}
                        className="shrink-0 rounded p-0.5 text-muted-foreground transition-colors hover:text-foreground"
                    >
                        <X className="size-3.5" />
                    </button>
                </div>
            </div>

            <button
                type="button"
                aria-label={t('export')}
                title={t('export')}
                disabled={exporting}
                onClick={runExport}
                className={buttonVariants({
                    variant: 'ghost',
                    size: 'icon',
                    className: 'rounded-xl transition-none hover:bg-transparent text-muted-foreground hover:text-foreground disabled:opacity-50',
                })}
            >
                <Download className={cn('size-4', exporting && 'animate-pulse')} />
            </button>

            <Popover>
                <PopoverTrigger asChild>
                    <button
                        type="button"
                        aria-label={t('options')}
                        className={buttonVariants({
                            variant: 'ghost',
                            size: 'icon',
                            className: 'rounded-xl transition-none hover:bg-transparent text-muted-foreground hover:text-foreground',
                        })}
                    >
                        <SlidersHorizontal className="size-4 transition-colors duration-300" />
                    </button>
                </PopoverTrigger>
                <PopoverContent
                    align="end"
                    side="bottom"
                    sideOffset={8}
                    className="w-72 rounded-2xl border border-border/60 bg-card p-3 shadow-xl"
                >
                    <div className="grid gap-3">
                        <div className="grid gap-2">
                            <p className="text-xs font-medium text-muted-foreground">{tf('status')}</p>
                            <div className="grid grid-cols-3 gap-2">
                                {statuses.map((status) => (
                                    <button
                                        key={status}
                                        type="button"
                                        onClick={() => onFilterChange({ ...filter, status })}
                                        className={cn(
                                            'h-8 rounded-lg border px-2 text-xs font-medium transition-colors',
                                            filter.status === status
                                                ? 'border-primary/30 bg-primary text-primary-foreground'
                                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                                        )}
                                    >
                                        {status === 'all' ? tf('all') : status}
                                    </button>
                                ))}
                            </div>
                        </div>

                        <div className="grid gap-2">
                            <div className="flex items-center justify-between">
                                <p className="text-xs font-medium text-muted-foreground">{tf('fields')}</p>
                                <button
                                    type="button"
                                    onClick={resetFields}
                                    className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
                                >
                                    <RotateCcw className="size-3" />
                                    {tf('reset')}
                                </button>
                            </div>
                            <div className="grid gap-1">
                                {LOG_FIELD_LABEL_KEYS.map(({ field, labelKey }) => (
                                    <label key={field} className="flex cursor-pointer items-center justify-between rounded-lg px-2 py-1.5 text-xs transition-colors hover:bg-muted/30">
                                        <span>{tf(labelKey)}</span>
                                        <Switch
                                            checked={visibility[field]}
                                            onCheckedChange={() => toggleField(field)}
                                        />
                                    </label>
                                ))}
                            </div>
                        </div>

                        <div className="grid gap-2">
                            <p className="text-xs font-medium text-muted-foreground">{tf('autoRefresh')}</p>
                            <div className="grid grid-cols-4 gap-2">
                                {LOG_AUTO_REFRESH_OPTIONS.map((option) => (
                                    <button
                                        key={option}
                                        type="button"
                                        onClick={() => setInterval(option)}
                                        className={cn(
                                            'h-8 rounded-lg border px-2 text-xs font-medium transition-colors',
                                            interval === option
                                                ? 'border-primary/30 bg-primary text-primary-foreground'
                                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                                        )}
                                    >
                                        {option === 0 ? tf('off') : `${option}s`}
                                    </button>
                                ))}
                            </div>
                        </div>
                    </div>
                </PopoverContent>
            </Popover>
        </div>
    );
}
