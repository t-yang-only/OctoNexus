import { useMemo, useState } from 'react';
import { Download, RotateCcw, Search, SlidersHorizontal, X } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { exportRelayLogs, type FaultKind, type RelayLogOverview, type RequestState } from '@/api/log';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { buttonVariants } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { cn } from '@/lib/utils';
import { matchLogMemoryFilter, type LogFaultFilter, type LogMemoryFilter } from './filter';
export type { LogMemoryFilter };
import {
    LOG_AUTO_REFRESH_OPTIONS,
    useLogAutoRefreshStore,
    useLogFieldVisibilityStore,
    type LogFieldName,
} from './store';

// FAULT_FILTERS 是失败归因的筛选档位, 顺序即弹窗内排列顺序。
// all/none 之外的三档与后端 model.RelayLog.FaultKind 一一对应, 前端不新增口径。
const FAULT_FILTERS: Array<{ value: LogFaultFilter; labelKey: string }> = [
    { value: 'all', labelKey: 'faultAll' },
    { value: 'request', labelKey: 'faultRequest' },
    { value: 'member', labelKey: 'faultMember' },
    { value: 'transient', labelKey: 'faultTransient' },
    { value: 'none', labelKey: 'faultNone' },
];

// countFaultKinds 统计各归因档位的当前条数。
// 只统计 **failed** 的记录: 归因回答的是"这次失败算谁的账", 取消/成功/进行中根本没有账可算。
// 若把 canceled 也算进「未分类」, 用户会把它读成「有一条失败但不知道算谁」——那是错的结论。
// 同样的理由, 「全部」档 = 当前失败总数, 它必须与 状态=failed 筛出来的条数一致。
function countFaultKinds(logs: RelayLogOverview[]): Record<LogFaultFilter, number> {
    const counts: Record<LogFaultFilter, number> = { all: 0, request: 0, member: 0, transient: 0, none: 0 };
    for (const log of logs) {
        if (log.status !== 'failed') continue;
        counts.all += 1;
        const kind = (log.fault_kind || '') as FaultKind | '';
        if (kind === 'request' || kind === 'member' || kind === 'transient') counts[kind] += 1;
        else counts.none += 1;
    }
    return counts;
}

// LOG_FIELD_LABEL_KEYS 是可见性开关的文案键, 顺序即弹窗内排列顺序。
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
    // 思考强度与思考 token（T-insight-001）排在 token 组之后。开关默认开着，
    // 但它们在卡片上仍常常看不见 —— 没指定强度、上游不回报思考 token 时不渲染。
    { field: 'reasoningEffort', labelKey: 'reasoningEffort' },
    { field: 'reasoningTokens', labelKey: 'reasoningTokens' },
];

interface LogToolbarProps {
    filter: LogMemoryFilter; // 当前内存筛选条件。
    onFilterChange: (filter: LogMemoryFilter) => void; // 更新内存筛选条件。
    logs: RelayLogOverview[]; // 全量内存日志, 供归因筛选块显示各档条数。
}

// useFilteredLogs 对 SSE 内存列表做本地过滤, 输入引用不变时返回同一数组引用以跳过重渲染。
// 三个维度全为默认值时直接返回原数组 —— 新增维度必须同步这个短路条件, 否则会出现"筛选生效了但列表没变"。
export function useFilteredLogs(logs: RelayLogOverview[], filter: LogMemoryFilter): RelayLogOverview[] {
    return useMemo(() => {
        if (filter.status === 'all' && filter.faultKind === 'all' && !filter.query.trim()) return logs;
        return logs.filter((log) => matchLogMemoryFilter(log, filter));
    }, [logs, filter]);
}

// LogToolbar 渲染日志页的内存筛选栏与字段可见性/自动刷新偏好弹窗。
// 筛选只在前端内存列表上执行, 不经过后端; 持久化筛选等 relay_logs 分页口径稳定后再接。
export function LogToolbar({ filter, onFilterChange, logs }: LogToolbarProps) {
    const t = useTranslations('log.list');
    const tf = useTranslations('log.filter');
    const faultCounts = useMemo(() => countFaultKinds(logs), [logs]);
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
                            <p className="text-xs font-medium text-muted-foreground">{tf('faultKind')}</p>
                            <div className="grid grid-cols-2 gap-2">
                                {FAULT_FILTERS.map((item) => (
                                    <button
                                        key={item.value}
                                        type="button"
                                        aria-pressed={filter.faultKind === item.value}
                                        onClick={() => onFilterChange({ ...filter, faultKind: item.value })}
                                        className={cn(
                                            'flex h-8 items-center justify-center gap-1 rounded-lg border px-2 text-xs font-medium transition-colors',
                                            filter.faultKind === item.value
                                                ? 'border-primary/30 bg-primary text-primary-foreground'
                                                : 'border-border bg-muted/20 text-foreground hover:bg-muted/30'
                                        )}
                                    >
                                        <span className="truncate">{tf(item.labelKey)}</span>
                                        <span className="shrink-0 tabular-nums opacity-70">{faultCounts[item.value]}</span>
                                    </button>
                                ))}
                            </div>
                            <p className="text-[10px] leading-snug text-muted-foreground/70">{tf('faultKindHint')}</p>
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
