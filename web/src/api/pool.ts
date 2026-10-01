import { queryOptions } from '@tanstack/react-query';
import { apiRequest } from './client';

// R-pool-ext-001 号池统一视图的前端客户端。
// 后端按"统一视图 + 适配器自描述"给接口：页面不硬编码有哪些后端，
// 而是先问 /pool/kinds 拿能力位，再按能力位决定按钮是否可见可点。
// 这样以后接入新的反代工具包，这个页面不需要改。

// PoolCapability 是适配器对外声明支持的能力位，与后端 pool.Capability 一致。
export type PoolCapability = 'list' | 'get' | 'probe' | 'refresh' | 'toggle' | 'provision' | 'revoke' | 'sync';

// PoolFieldSpec 描述适配器自己的字段；secret=true 表示凭据类字段，任何接口都不回显。
export type PoolFieldSpec = {
    name: string;
    type: string;
    label: string;
    required: boolean;
    secret: boolean;
};

// PoolOperation 是从能力位推导出来的操作清单，页面用它显示"这个后端能做什么"。
export type PoolOperation = {
    capability: PoolCapability;
    method: string;
    path: string;
    description: string;
};

// PoolKindInfo 是一种号池后端的自描述。
export type PoolKindInfo = {
    kind: string;
    title: string;
    capabilities: PoolCapability[];
    fields?: PoolFieldSpec[];
    builtin: boolean;
    since?: string;
    operations?: PoolOperation[];
};

// PoolEntry 是统一视图里的一条条目：只有元数据，永远不含凭据。
export type PoolEntry = {
    kind: string;
    id: string;
    name: string;
    provider?: string;
    status: string;
    enabled: boolean;
    healthy: boolean;
    plan_tier?: string;
    expires_at?: string;
    last_error?: string;
    labels?: Record<string, string>;
    detail?: Record<string, unknown>;
};

// PoolKindError 表示某个后端这次没取到数据（接口仍 200，错误进 warnings）。
export type PoolKindError = {
    kind: string;
    error: string;
};

export type PoolEntryList = {
    items: PoolEntry[];
    total: number;
    returned: number;
    scanned: number;
    kind?: string;
    warnings?: PoolKindError[];
};

export type PoolKindStat = {
    entries: number;
    enabled: number;
    healthy: number;
};

export type PoolStats = {
    total: number;
    enabled: number;
    healthy: number;
    by_kind: Record<string, PoolKindStat>;
};

export type PoolSummary = {
    total: number;
    enabled: number;
    disabled: number;
    healthy: number;
    unhealthy: number;
    with_error: number;
    expiring_soon: number;
    expired: number;
    by_kind: Record<string, PoolKindStat>;
    by_provider: Record<string, number>;
    by_status: Record<string, number>;
    kind_errors?: PoolKindError[];
};

export type PoolSyncReport = {
    kind: string;
    entries: number;
    notes?: string[];
};

export type PoolBatchItemResult = {
    kind: string;
    id: string;
    ok: boolean;
    entry?: PoolEntry;
    error?: string;
};

export type PoolBatchResult = {
    action: string;
    total: number;
    ok: number;
    failed: number;
    skipped: number;
    items: PoolBatchItemResult[];
};

// PoolEntryFilter 与后端 pool.Filter 的查询参数一一对应（参数名不可改，是接口契约）。
export type PoolEntryFilter = {
    kind?: string;
    q?: string;
    provider?: string;
    status?: string;
    enabled?: boolean;
    healthy?: boolean;
    hasExpiry?: boolean;
    expiringWithin?: number;
    sort?: string;
    desc?: boolean;
    limit?: number;
    offset?: number;
};

// poolQueryString 把筛选条件拼成查询串；空值不下发，避免把"没筛"变成"筛空"。
export function poolQueryString(filter: PoolEntryFilter): string {
    const params = new URLSearchParams();
    const set = (key: string, value: string | number | boolean | undefined) => {
        if (value === undefined || value === '') return;
        params.set(key, String(value));
    };
    set('kind', filter.kind);
    set('q', filter.q);
    set('provider', filter.provider);
    set('status', filter.status);
    set('enabled', filter.enabled);
    set('healthy', filter.healthy);
    set('has_expiry', filter.hasExpiry);
    set('expiring_within', filter.expiringWithin);
    set('sort', filter.sort);
    set('desc', filter.desc);
    set('limit', filter.limit);
    set('offset', filter.offset);
    return params.toString();
}

export const poolKindsQueryOptions = queryOptions({
    queryKey: ['pool', 'kinds'],
    queryFn: () => apiRequest<{ items: PoolKindInfo[]; total: number }>('/api/v1/pool/kinds'),
});

export const poolStatsQueryOptions = queryOptions({
    queryKey: ['pool', 'stats'],
    queryFn: () => apiRequest<PoolStats>('/api/v1/pool/stats'),
});

export const poolSummaryQueryOptions = queryOptions({
    queryKey: ['pool', 'summary'],
    queryFn: () => apiRequest<PoolSummary>('/api/v1/pool/summary'),
});

// poolEntriesQueryOptions 按当前筛选条件取统一视图。
export function poolEntriesQueryOptions(filter: PoolEntryFilter) {
    const query = poolQueryString(filter);
    return queryOptions({
        queryKey: ['pool', 'entries', query],
        queryFn: () => apiRequest<PoolEntryList>(`/api/v1/pool/entries${query ? `?${query}` : ''}`),
    });
}

// poolEntryPath 拼单条条目的路径；kind 与 id 都可能含 provider:account 这类分隔符，必须转义。
export function poolEntryPath(kind: string, id: string): string {
    return `/api/v1/pool/entries/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`;
}

// poolProbeEntry 探活一条条目：后端不可用是正常业务状态，失败也会带回当前快照与原因。
export function poolProbeEntry(kind: string, id: string) {
    return apiRequest<PoolEntry>(`${poolEntryPath(kind, id)}/probe`, { method: 'POST' });
}

// poolRefreshEntry 刷新一条条目（按需拉一次该后端的最新状态）。
export function poolRefreshEntry(kind: string, id: string) {
    return apiRequest<PoolEntry>(`${poolEntryPath(kind, id)}/refresh`, { method: 'POST' });
}

// poolSetEntryEnabled 人工启停一条条目。
// 停用会记下"人工决定"，号池同步不会把它重新打开；条目状态不允许时后端回 409。
export function poolSetEntryEnabled(kind: string, id: string, enabled: boolean) {
    return apiRequest<PoolEntry>(`${poolEntryPath(kind, id)}/${enabled ? 'enable' : 'disable'}`, { method: 'POST' });
}

// poolSyncKind 同步一种后端（把账号/配置物化成统一视图里的条目）。
export function poolSyncKind(kind: string) {
    return apiRequest<PoolSyncReport>(`/api/v1/pool/kinds/${encodeURIComponent(kind)}/sync`, { method: 'POST' });
}

// poolBatch 批量动作：逐条独立，一条失败不影响其余；必须给 ids 或 filter（不允许隐式全量）。
export function poolBatch(request: { action: string; kind?: string; ids?: string[]; filter?: Record<string, string>; max?: number }) {
    return apiRequest<PoolBatchResult>('/api/v1/pool/entries/batch', { method: 'POST', body: request });
}

// poolExportURL 导出统一视图的地址（json 给工具，csv 给人）；用链接直接下载，走浏览器的登录态。
// 带上当前筛选条件：导出与屏幕上的列表必须是同一口径（后端与 /pool/entries 共用同一份 Filter 解析）。
// limit/offset 故意不下发：导出是"把当前筛选结果整个拿走"，分页只属于列表页。
export function poolExportURL(format: 'json' | 'csv', filter?: PoolEntryFilter): string {
    // 显式白名单：只带筛选与排序，不带分页（limit/offset 不属于导出）。
    const { kind, q, provider, status, enabled, healthy, hasExpiry, expiringWithin, sort, desc } = filter ?? {};
    const params = new URLSearchParams(
        poolQueryString({ kind, q, provider, status, enabled, healthy, hasExpiry, expiringWithin, sort, desc }),
    );
    params.set('format', format);
    return `/api/v1/pool/export?${params.toString()}`;
}
