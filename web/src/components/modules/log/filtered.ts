import { useMemo } from 'react';
import type { LogDisplaySource } from '@/api/log';
import { matchLogMemoryFilter, type LogFaultFilter, type LogMemoryFilter, type LogTestFilter } from './filter';
import { resolveLogDisplay } from './display';

// 本文件只放**非组件**的筛选逻辑（hook 与计数帮手）。
// 与 FilterBar.tsx 分开是因为 react-refresh/only-export-components 要求：
// 一个文件里既有组件又有 hook 时，改 hook 会让组件的热更新失效。

// countTestStates 统计测试/真实各多少条。
//
// 与归因计数不同，这一维**不限于 failed**：归因分的是"失败算谁的账"，
// 而这一维分的是"流量从哪来"，成功与失败都要算 —— 否则"只看测试"这个入口
// 会在用户想确认"我刚发的测试请求到底有没有被标记"时给出 0。
export function countTestStates(logs: LogDisplaySource[]): Record<LogTestFilter, number> {
    const counts: Record<LogTestFilter, number> = { all: logs.length, test: 0, real: 0 };
for (const log of logs) {
        // 经同构层取：两种来源都有 is_test，但历史行是扁平字段、实时快照可能整个缺席该字段，
        // 统一交给 display 的 `=== true` 判定（与筛选器同口径，避免两处规则分叉）。
        if (resolveLogDisplay(log).isTest) counts.test += 1;
        else counts.real += 1;
    }
    return counts;
}

// countFaultKinds 统计各归因档位的当前条数。
// 只统计 **failed** 的记录: 归因回答的是"这次失败算谁的账", 取消/成功/进行中根本没有账可算。
// 若把 canceled 也算进「未分类」, 用户会把它读成「有一条失败但不知道算谁」——那是错的结论。
// 同样的理由, 「全部」档 = 当前失败总数, 它必须与 状态=failed 筛出来的条数一致。
export function countFaultKinds(logs: LogDisplaySource[]): Record<LogFaultFilter, number> {
    const counts: Record<LogFaultFilter, number> = { all: 0, request: 0, member: 0, transient: 0, none: 0 };
for (const log of logs) {
        // 经同构层取：历史行的 status 是 string、实时快照是 RequestState 联合，
        // 直接读会让历史行的归因统计全部落空（列表初始一页正是历史）。
        const display = resolveLogDisplay(log);
        if (display.status !== 'failed') continue;
        counts.all += 1;
        // 显式收窄成三档联合再索引：faultKind 的类型是 FaultKind | ''，
        // 不先收窄的话 TS 无法排除 ''，会按 any 索引（TS7053）。
        const kind: 'request' | 'member' | 'transient' | '' = display.faultKind;
        if (kind === 'request' || kind === 'member' || kind === 'transient') counts[kind] += 1;
        else counts.none += 1;
    }
    return counts;
}

// useFilteredLogs 对 SSE 内存列表做本地过滤, 输入引用不变时返回同一数组引用以跳过重渲染。
// 三个维度全为默认值时直接返回原数组 —— 新增维度必须同步这个短路条件, 否则会出现"筛选生效了但列表没变"。
// useFilteredLogs 对日志列表做本地过滤, 输入引用不变时返回同一数组引用以跳过重渲染。
// 入参是联合类型：列表初始一页持久化历史 + SSE 实时增量（见 api/log.ts 的 LogDisplaySource）。
export function useFilteredLogs(logs: LogDisplaySource[], filter: LogMemoryFilter): LogDisplaySource[] {
    return useMemo(() => {
        // 无筛选判断要把新字段算进去：只判旧四项时，设了日期/Key 也显示"未筛选"，
// 用户会以为筛选没生效（而列表其实已经少了东西）。
    if (
        filter.status === 'all' &&
        filter.faultKind === 'all' &&
        filter.isTest === 'all' &&
        !filter.query.trim() &&
        !filter.from &&
        !filter.to &&
        !filter.apikey.trim()
) {
        return logs;
    }
        return logs.filter((log) => matchLogMemoryFilter(log, filter));
    }, [logs, filter]);
}
