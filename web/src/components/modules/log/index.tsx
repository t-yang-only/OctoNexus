import { Logs, Loader2 } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslations } from 'use-intl';
import { useLogs } from '@/api/log';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { AttemptStatsPanel } from './AttemptStatsPanel';
import { LogCard } from './Item';
import { LogToolbar, useFilteredLogs, type LogMemoryFilter } from './FilterBar';
import { useLogAutoRefreshStore } from './store';

// Log 展示进程内日志概览，并按 RequestID 实时更新卡片。
// 工具栏提供内存筛选（状态+关键字）与字段可见性/自动刷新偏好；筛选只在前端内存列表上执行。
export function Log() {
    const t = useTranslations('log');
    const { logs, isLoading, error, refresh } = useLogs();
    const interval = useLogAutoRefreshStore((s) => s.interval);
    const [filter, setFilter] = useState<LogMemoryFilter>({ status: 'all', faultKind: 'all', isTest: 'all', query: '' });
    const filtered = useFilteredLogs(logs, filter);
    const shownIsFiltered = filtered.length !== logs.length;

    // 自动刷新兜底: SSE 本就是实时推送, 该定时器仅在所选间隔下重建 SSE 连接以兜底断线/空闲。
    useEffect(() => {
        if (!interval) return;
        const timer = setInterval(refresh, interval * 1000);
        return () => clearInterval(timer);
    }, [interval, refresh]);

    if (isLoading) {
        return (
            <div className="flex h-full items-center justify-center">
                <Loader2 className="size-6 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (logs.length === 0) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-3 text-muted-foreground">
                {!error && <Logs className="size-8" />}
                <span className="text-sm">{error ? t('list.disconnected') : t('list.empty')}</span>
            </div>
        );
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-3">
            {error && (
                <div className="flex shrink-0 items-center justify-center px-1 pb-3 text-xs text-destructive">
                    <span>{t('list.disconnected')}</span>
                </div>
            )}
            <div className="flex shrink-0 items-center justify-between gap-2">
                <LogToolbar filter={filter} onFilterChange={setFilter} logs={logs} />
                {shownIsFiltered && (
                    <span className="truncate text-xs text-muted-foreground">
                        {t('list.filtered', { shown: filtered.length, total: logs.length })}
                    </span>
                )}
            </div>
            <div className="min-h-0 flex-1">
                <VirtualizedGrid
                    items={filtered}
                    layout="list"
                    columns={{ default: 1 }}
                    estimateItemHeight={104}
                    overscan={8}
                    getItemKey={(log) => `log-${log.id}`}
                    renderItem={(log) => <LogCard log={log} />}
                />
            </div>
            {/* 尝试链聚合（T-trace-002）：回答"谁在被反复试错"。
                放在列表**下方**而不是上方：它是背景诊断信息，
                列表才是这个页面要给人看的东西，不该被统计挤下去。
                面板自身在无数据/无异常时返回 null，正常时不占版面。 */}
            <div className="shrink-0">
                <AttemptStatsPanel />
            </div>
        </div>
    );
}
