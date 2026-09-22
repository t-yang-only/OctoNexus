import { useMutation, useQuery } from '@tanstack/react-query';
import { apiRequest, queryClient } from './client';

/**
 * 用量报告（吸收上游 lingyuins/octopus 的 Usage Reports）
 *
 * 口径（与后端 op.UsageReportBuild 一致，面板只负责如实展示）：
 * - 报告覆盖**上一个完整周期**（日报=昨天、周报=上一自然周、月报=上一自然月），
 *   这样发出来的数字不会再变；
 * - 只统计**已落库的统计**（落库周期由「统计保存周期」决定），所以面板会显示数据截止时间；
 * - 同一个周期只发一次；面板上的「立即发送」不占用周期名额，可以随便试。
 */
export type UsageReportPeriod = 'daily' | 'weekly' | 'monthly';

export interface UsageReportTop {
    name: string;
    count: number;
    cost: number;
    success: number;
    failed: number;
}

export interface UsageReport {
    period: UsageReportPeriod;
    period_key: string;
    label: string;
    from: string;
    to: string;
    requests: number;
    success: number;
    failed: number;
    success_rate: number;
    input_token: number;
    output_token: number;
    cost: number;
    wait_time_ms: number;
    top_models: UsageReportTop[] | null;
    top_channels: UsageReportTop[] | null;
    balance: number;
    currency: string;
    data_through: string;
    empty: boolean;
    text: string;
}

export interface UsageReportHistoryItem {
    period_key: string;
    period: string;
    sent_at: string;
    delivered: number;
    failed: number;
    summary: string;
}

export function usePreviewUsageReport() {
    return useMutation({
        mutationFn: (period: UsageReportPeriod) =>
            apiRequest<UsageReport>('/api/v1/usage-report/preview', { method: 'POST', body: { period } }),
    });
}

export function useSendUsageReport() {
    return useMutation({
        mutationFn: (period: UsageReportPeriod) =>
            apiRequest<{ report: UsageReport; results: unknown[] }>('/api/v1/usage-report/send', {
                method: 'POST',
                body: { period },
            }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['usage-report', 'history'] }),
    });
}

export function useUsageReportHistory(enabled = true) {
    return useQuery({
        queryKey: ['usage-report', 'history'],
        queryFn: () => apiRequest<UsageReportHistoryItem[]>('/api/v1/usage-report/history'),
        enabled,
    });
}
