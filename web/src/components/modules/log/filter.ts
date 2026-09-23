import type { FaultKind, RelayLogOverview, RequestState } from '@/api/log';

// LogFaultFilter 是失败归因的筛选值: all 表示不过滤, none 表示只看未分类。
export type LogFaultFilter = FaultKind | 'all' | 'none';

export interface LogMemoryFilter {
    status: RequestState | 'all'; // 状态筛选, all 表示不过滤。
    faultKind: LogFaultFilter; // 失败归因筛选, all 表示不过滤。
    query: string; // 关键字, 在模型/渠道/Key/错误信息四列匹配。
}

// matchLogMemoryFilter 判断单条日志是否通过内存筛选。
// 归因筛选只在"失败"这一维上有意义, 但它不强制 status=failed: 用户可能想连成功一起看自己筛出来的样本。
export function matchLogMemoryFilter(log: RelayLogOverview, filter: LogMemoryFilter): boolean {
    if (filter.status !== 'all' && log.status !== filter.status) return false;
    if (filter.faultKind !== 'all') {
        // 空串与 undefined 必须归为同一档: 后端对未分类下发空串(omitempty 也可能整个字段缺席),
        // 用 falsy 判断而不是 === undefined, 否则「只看未分类」会漏掉带空串的那批。
        const kind = log.fault_kind || '';
        if (filter.faultKind === 'none' ? kind !== '' : kind !== filter.faultKind) return false;
    }
    const query = filter.query.trim().toLowerCase();
    if (!query) return true;
    return (
        log.model.toLowerCase().includes(query) ||
        log.target_channel.toLowerCase().includes(query) ||
        log.api_key_name.toLowerCase().includes(query) ||
        (log.error ?? '').toLowerCase().includes(query)
    );
}
