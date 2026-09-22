import { useMutation, useQuery } from '@tanstack/react-query';
import { apiRequest, queryClient } from './client';

/**
 * 告警规则（吸收上游 lingyuins/octopus 的 Alerts）
 *
 * 与既有通知的分工：本项目本来就会在**具体事件**上发通知（余额归零、探活恢复）。
 * 这里补的是**指标型规则**——"某个渠道最近 15 分钟错误率超过 30%"这类判断，
 * 靠事件驱动的通知覆盖不到（失败是散落的，没有单一事件可以挂钩）。
 *
 * 两条防噪音机制（后端 op.AlertRuleEvaluate 落地，面板只如实展示）：
 * - **样本量下限**：窗口内请求数低于 min_requests 一律不判定——一次偶然失败会被算成
 *   100% 错误率，这是告警系统最常见的噪音来源；
 * - **冷却**：同一规则同一渠道在 cooldown_minutes 内不重复触发。
 */
export type AlertMetric = 'error_rate' | 'latency';
export type AlertScope = 'all' | 'channel';

export interface AlertRule {
    id: number;
    name: string;
    metric: AlertMetric;
    scope: AlertScope;
    scope_value: string;
    threshold: number;
    window_minutes: number;
    min_requests: number;
    cooldown_minutes: number;
    enabled: boolean;
    last_fired_at: number;
    created_at?: string;
    updated_at?: string;
}

export interface AlertRuleInput {
    name: string;
    metric: AlertMetric;
    scope: AlertScope;
    scope_value: string;
    threshold: number;
    window_minutes: number;
    min_requests: number;
    cooldown_minutes: number;
    enabled: boolean;
}

/** 一次评估结果（面板的「试算」用它解释"会不会报、为什么"）。 */
export interface AlertEvaluation {
    rule_id: number;
    rule_name: string;
    channel: string;
    metric: string;
    value: number;
    threshold: number;
    requests: number;
    breached: boolean;
    fired: boolean;
    reason: string;
}

export interface AlertFire {
    id: number;
    rule_id: number;
    rule_name: string;
    channel: string;
    metric: string;
    value: number;
    threshold: number;
    requests: number;
    message: string;
    fired_at: string;
}

export const alertRuleQueryOptions = {
    queryKey: ['alert-rule', 'list'],
    queryFn: () => apiRequest<AlertRule[]>('/api/v1/alert-rule/list'),
};

export function useAlertRules(enabled = true) {
    return useQuery({ ...alertRuleQueryOptions, enabled });
}

export function useAlertFires(enabled = true) {
    return useQuery({
        queryKey: ['alert-rule', 'fires'],
        queryFn: () => apiRequest<AlertFire[]>('/api/v1/alert-rule/fires'),
        enabled,
    });
}

function invalidate() {
    queryClient.invalidateQueries({ queryKey: ['alert-rule'] });
}

export function useCreateAlertRule() {
    return useMutation({
        mutationFn: (input: AlertRuleInput) =>
            apiRequest<AlertRule>('/api/v1/alert-rule/create', { method: 'POST', body: input }),
        onSuccess: invalidate,
    });
}

export function useUpdateAlertRule() {
    return useMutation({
        mutationFn: ({ id, ...input }: Partial<AlertRuleInput> & { id: number }) =>
            apiRequest<AlertRule>(`/api/v1/alert-rule/update/${id}`, { method: 'POST', body: input }),
        onSuccess: invalidate,
    });
}

export function useDeleteAlertRule() {
    return useMutation({
        mutationFn: (id: number) => apiRequest<unknown>(`/api/v1/alert-rule/delete/${id}`, { method: 'DELETE' }),
        onSuccess: invalidate,
    });
}

/** 试算一条规则：不发送、不记账，把未触发的原因也带回来。 */
export function useEvaluateAlertRule() {
    return useMutation({
        mutationFn: (ruleId: number) =>
            apiRequest<AlertEvaluation[]>('/api/v1/alert-rule/evaluate', { method: 'POST', body: { rule_id: ruleId } }),
    });
}

/** 预演一轮（算但不发）：回答"按现在这套规则会报几条"，调阈值时不必先把自己刷一遍。 */
export function usePreviewAlertRules() {
    return useMutation({
        mutationFn: () => apiRequest<AlertEvaluation[]>('/api/v1/alert-rule/preview', { method: 'POST', body: {} }),
    });
}

/** 立刻跑一轮评估（会真的发送告警）。 */
export function useRunAlertRules() {
    return useMutation({
        mutationFn: () => apiRequest<{ fired: number }>('/api/v1/alert-rule/run', { method: 'POST', body: {} }),
        onSuccess: invalidate,
    });
}
