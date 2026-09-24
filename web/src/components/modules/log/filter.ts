import type { FaultKind, RelayLogOverview, RequestState } from '@/api/log';

// LogFaultFilter 是失败归因的筛选值: all 表示不过滤, none 表示只看未分类。
export type LogFaultFilter = FaultKind | 'all' | 'none';

// LogTestFilter 是测试请求的筛选值（T-trace-006）。
//
// 三态而不是布尔: 布尔只能表达"只看测试/只看真实"里的一种，一旦要"两种都看"
// 就得再加一个开关，两个开关会产生都开(矛盾)/都关(等于全关)的组合歧义。
export type LogTestFilter = 'all' | 'test' | 'real';

export interface LogMemoryFilter {
    status: RequestState | 'all'; // 状态筛选, all 表示不过滤。
    faultKind: LogFaultFilter; // 失败归因筛选, all 表示不过滤。
    isTest: LogTestFilter; // 测试请求筛选, all 表示不过滤。
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
    if (filter.isTest !== 'all') {
        // 只有显式 true 才算测试请求，与 display.ts 的判定同口径（`=== true`）。
        // 缺字段的行（升级前的存量、实时流里 omitempty 省掉它的真实流量）必须落到"真实"这一档：
        // 把 undefined 算成测试，等于凭空从画面里挖掉一批真实请求。
        const isTest = log.is_test === true;
        if (filter.isTest === 'test' ? !isTest : isTest) return false;
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
