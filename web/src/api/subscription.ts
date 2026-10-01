import { useMutation, useQuery } from '@tanstack/react-query';
import { apiRequest, queryClient } from './client';

/**
 * 手动订阅（R-acct-004 / T-acct-005）
 *
 * 用途：上游没有余额接口的站点，由人录一条套餐余额与有效期，它按与自动读数**同一口径**
 * （balance_points_per_unit，可按行覆盖）折算后并入总余额。
 *
 * 三条口径（与后端 op.BalanceSummaryGet 一致，面板只负责如实展示）：
 * - 绑定了渠道且该渠道没有自动读数 → 这条渠道由「未知」变成「已知（手动）」，金额计入总额；
 * - 绑定了渠道但该渠道已有自动读数 → 自动读数是更可信的那一份，本条不再计入（counted=false）；
 * - 已过期或已停用 → 不计入总额，只在明细里显示到期状态（面板用 counted / expired 解释清楚）。
 */
export interface ManualSubscription {
    id: number;
    channel_id: number;
    name: string;
    note?: string;
    balance_points: number;
    points_per_unit: number;
    expire_at: number;
    enabled: boolean;
    created_at?: number;
    updated_at?: number;
}

/** ManualSubscriptionRow 是余额快照里的明细（含折算结果、到期状态与「是否计入总额」）。 */
export interface ManualSubscriptionRow extends ManualSubscription {
    channel_name?: string;
    balance: number;
    days_left: number;
    expired: boolean;
    counted: boolean;
}

export interface ManualSubscriptionInput {
    id?: number;
    channel_id: number;
    name: string;
    note?: string;
    balance_points: number;
    points_per_unit: number;
    expire_at: number;
    enabled: boolean;
}

export const manualSubscriptionQueryOptions = {
    queryKey: ['subscription', 'list'],
    queryFn: () => apiRequest<ManualSubscription[]>('/api/v1/subscription/list'),
};

export function useManualSubscriptions(enabled = true) {
    return useQuery({ ...manualSubscriptionQueryOptions, enabled });
}

/** 写操作统一失效余额快照与订阅列表：总额会随记录变化，两个视图必须同时刷新。 */
function invalidate() {
    queryClient.invalidateQueries({ queryKey: ['subscription', 'list'] });
    queryClient.invalidateQueries({ queryKey: ['balance', 'summary'] });
}

export function useCreateManualSubscription() {
    return useMutation({
        mutationFn: (input: ManualSubscriptionInput) =>
            apiRequest<ManualSubscription>('/api/v1/subscription/create', { method: 'POST', body: input }),
        onSuccess: invalidate,
    });
}

export function useUpdateManualSubscription() {
    return useMutation({
        mutationFn: (input: ManualSubscriptionInput) =>
            apiRequest<ManualSubscription>('/api/v1/subscription/update', { method: 'POST', body: input }),
        onSuccess: invalidate,
    });
}

export function useDeleteManualSubscription() {
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<unknown>(`/api/v1/subscription/delete/${id}`, { method: 'DELETE' }),
        onSuccess: invalidate,
    });
}
