import type { FaultKind, LogDisplaySource, RequestState } from '@/api/log';
import { resolveLogDisplay } from './display';

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
    // from/to 是创建日期范围（需求4「按天查询」），YYYY-MM-DD，空串=不设这一侧。
    //
    // 与 query 的分工：query 匹配的是**内容**（模型/渠道/Key/错误），from/to 限定的是**时间**。
    // 内存列表按 started_at 本地过滤，导出时原样传给后端 —— 同一套条件，
    // 所以"屏幕上看到的"与"导出去的"不会对不上账。
    from: string;
    to: string;
    // apikey 是客户端 Key 名（需求4「按 api 查询」），空串=不过滤。
    //
    // 与 query 的分工：query 在四列里做**包含**匹配，apikey 只盯 api_key_name 一列做**全等**。
    // 想精确看"某一把 Key 发了什么"时，包含匹配会被同名子串干扰（如 Key 名 "test" 会命中 "test2"）。
    apikey: string;
}

// dayStart 把 YYYY-MM-DD 解析成当天 00:00 的毫秒数；非法或空串返回 NaN。
// 用本地时区而不是 UTC：日志卡片上显示的是本地时间，筛选必须跟它同一把尺子，
// 否则用户选"9月24日"会看到时区偏移掉的一批。
function dayStart(value: string): number {
    if (!value) return Number.NaN;
    const parsed = new Date(`${value}T00:00:00`);
    return Number.isNaN(parsed.getTime()) ? Number.NaN : parsed.getTime();
}

// matchLogMemoryFilter 判断单条日志是否通过内存筛选。
// 归因筛选只在"失败"这一维上有意义, 但它不强制 status=failed: 用户可能想连成功一起看自己筛出来的样本。
// matchLogMemoryFilter 对一条日志做本地过滤。
//
// 入参是联合类型（LogDisplaySource）：列表初始一页持久化历史 + SSE 实时增量。
// 因此**不能直接读 log.status / log.fault_kind** —— 两种来源形状不同
// （实时快照的 status 是 RequestState 联合、历史的 status 是 string），
// 直接比会让「按状态筛选」在历史行上全部落空。统一走同构层 resolveLogDisplay。
export function matchLogMemoryFilter(log: LogDisplaySource, filter: LogMemoryFilter): boolean {
  const display = resolveLogDisplay(log);
    // 日期范围（需求4）。半开区间 [from 00:00, to 次日 00:00)：
    // "查 9月24日"不含 9月25日 00:00 之后的数据，相邻两天的边界也不重复计入。
    // 非法日期不过滤也不报错 —— 输入框自身是 type="date"，浏览器已挡住绝大部分错值；
    // 真出现非法值时不过滤比"永远查不到东西"更接近用户意图。
    if (filter.from) {
        const from = dayStart(filter.from);
        if (!Number.isNaN(from) && new Date(display.startedAt).getTime() < from) return false;
    }
    if (filter.to) {
        const to = dayStart(filter.to);
        if (!Number.isNaN(to)) {
            const toExclusive = to + 24 * 60 * 60 * 1000;
            if (new Date(display.startedAt).getTime() >= toExclusive) return false;
        }
    }
    // 按 API Key 精确匹配（需求4）。空串=不过滤；全等而不是包含，
    // 与 query 的四列包含匹配分工（见 LogMemoryFilter.apikey 注释）。
    if (filter.apikey && display.apiKeyName !== filter.apikey) return false;
    if (filter.status !== 'all' && display.status !== filter.status) return false;
    if (filter.faultKind !== 'all') {
        // 空串与 undefined 必须归为同一档: 后端对未分类下发空串(omitempty 也可能整个字段缺席),
        // 用 falsy 判断而不是 === undefined, 否则「只看未分类」会漏掉带空串的那批。
        const kind = display.faultKind || '';
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
