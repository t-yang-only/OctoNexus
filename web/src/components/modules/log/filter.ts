import type { RelayLogOverview, RequestState } from '@/api/log';

export interface LogMemoryFilter {
    status: RequestState | 'all'; // 状态筛选, all 表示不过滤。
    query: string; // 关键字, 在模型/渠道/Key/错误信息四列匹配。
}

// matchLogMemoryFilter 判断单条日志是否通过内存筛选。
export function matchLogMemoryFilter(log: RelayLogOverview, filter: LogMemoryFilter): boolean {
    if (filter.status !== 'all' && log.status !== filter.status) return false;
    const query = filter.query.trim().toLowerCase();
    if (!query) return true;
    return (
        log.model.toLowerCase().includes(query) ||
        log.target_channel.toLowerCase().includes(query) ||
        log.api_key_name.toLowerCase().includes(query) ||
        (log.error ?? '').toLowerCase().includes(query)
    );
}
