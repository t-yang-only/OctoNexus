import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';
import type { ManualSubscriptionRow } from './subscription';

/**
 * 总余额（T-balance-001）
 *
 * 口径：渠道余额 = 各渠道最近一次余额读到的剩余额度（上游点数）按 points_per_unit 折算成货币值；
 * 剩余次数 = 渠道配置的包月额度 - 已用。未知余额的渠道计入 unknown_channels、不计入总额，
 * 免得把「还没扫到」显示成「没钱了」。
 */
export interface ChannelBalanceRow {
    channel_id: number;
    channel_name: string;
    enabled: boolean;
    known: boolean;
    remaining: number;
    balance: number;
    billing_mode: string;
    multiplier: number;
    per_call_price: number;
    monthly_quota: number;
    monthly_used: number;
    monthly_remaining: number;
    /** balance_source: api = 从上游读到的；manual = 人在面板里录的（无接口站点，见手动订阅）。 */
    balance_source?: 'api' | 'manual';
    key_count: number;
    key_enabled: number;
}

export interface APIKeyBalanceRow {
    id: number;
    name: string;
    enabled: boolean;
    limit: number;
    used: number;
    remaining: number;
    unlimited: boolean;
    requests: number;
    tokens: number;
}

export interface BalanceSummary {
    total: number;
    total_monthly_remaining: number;
    currency: string;
    points_per_unit: number;
    known_channels: number;
    unknown_channels: number;
    channels: ChannelBalanceRow[];
    keys: APIKeyBalanceRow[];
    /** 手动订阅贡献的余额（已含在 total 里）。 */
    manual_total: number;
    manual_subscriptions: ManualSubscriptionRow[];
    /** 已过期的手动订阅条数（不计入总额，面板据此提醒续费）。 */
    manual_expired: number;
    generated_at: number;
}

// balanceSummaryQueryOptions 首页与设置页预取共用的余额查询定义。
export const balanceSummaryQueryOptions = queryOptions({
    queryKey: ['balance', 'summary'],
    queryFn: () => apiRequest<BalanceSummary>('/api/v1/balance/summary'),
});

/** 读取总余额快照 Hook：首页总览用，30 秒刷新一次（余额本身按分钟级扫描更新）。 */
export function useBalanceSummary() {
    return useQuery({
        ...balanceSummaryQueryOptions,
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

/**
 * 标准协议余额端点的地址（转发端口，用调用模型的那把 Key 直接查）。
 * 供设置页展示给用户复制：客户端（ChatGPT-Next-Web 等）按这些地址显示余额。
 */
export const BALANCE_PROTOCOL_PATHS = [
    '/v1/dashboard/billing/subscription',
    '/v1/dashboard/billing/usage',
    '/v1/balance',
];
