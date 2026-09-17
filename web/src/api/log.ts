import { useMutation, useQuery } from '@tanstack/react-query';
import { useEffect, useState } from 'react';
import { apiRequest } from './client';

// RequestState 表示 Relay 请求的实时状态。
export type RequestState = 'running' | 'committed' | 'success' | 'failed' | 'canceled';

// RelayUsage 保存请求结束后确认的统一 Token 用量。
export interface RelayUsage {
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
    prompt_tokens_details: {
        cached_tokens: number;
        write_cached_tokens?: number;
    } | null;
}

// RelayHistoryItem 是历史日志接口返回的单条持久化快照。
export interface RelayHistoryItem {
    id: number;
    request_id: number;
    status: string;
    model: string;
    group_id: number;
    api_key_name: string;
    target_channel: string;
    target_model: string;
    target_protocol: number;
    started_at: string;
    first_byte_ms: number;
    duration_ms: number;
    // attempts 是本请求打向上游的轮次数: 1 = 第一次就出结果, >1 = 中途换过成员; 首字竞速的多路并算一轮。
    attempts: number;
    prompt_tokens: number;
    cached_tokens: number;
    completion_tokens: number;
    cost: number;
    error: string;
}

// RelayHistoryFilter 是历史查询的筛选条件, 空串表示不过滤。
export interface RelayHistoryFilter {
    status?: string;
    model?: string;
    channel?: string;
    apikey?: string;
    q?: string;
    limit?: number;
    offset?: number;
}

// useRelayHistory 按筛选条件查询持久化历史日志（分页）。
export function useRelayHistory(filter: RelayHistoryFilter) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(filter)) {
        if (value !== undefined && value !== '') params.set(key, String(value));
    }
    const queryKey = ['logs', 'history', params.toString()];
    return useQuery({
        queryKey,
        queryFn: () =>
            apiRequest<{ items: RelayHistoryItem[]; total: number }>(
                `/api/v1/log/history?${params.toString()}`
            ),
    });
}

// exportRelayLogs 把当前筛选条件下的请求级明细导出成 CSV（U-key-001 余项）。
// 与历史查询同一套查询参数，但不分页：后端逐行流式写出，前端下载为文件。
export async function exportRelayLogs(filter: RelayHistoryFilter) {
    const params = new URLSearchParams();
    for (const [key, value] of Object.entries(filter)) {
        // limit/offset 属于分页语义，导出不带它们（导出就是"当前筛选的全量"）。
        if (key === 'limit' || key === 'offset') continue;
        if (value !== undefined && value !== '') params.set(key, String(value));
    }
    const response = await fetch(`/api/v1/log/export?${params.toString()}`, { credentials: 'include' });
    if (!response.ok) {
        throw new Error(`export failed: HTTP ${response.status}`);
    }
    const blob = await response.blob();
    const now = new Date();
    const pad = (n: number) => String(n).padStart(2, '0');
    const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
    const url = URL.createObjectURL(blob);
    try {
        const anchor = document.createElement('a');
        anchor.href = url;
        anchor.download = `octopus-relay-logs-${stamp}.csv`;
        document.body.appendChild(anchor);
        anchor.click();
    } finally {
        URL.revokeObjectURL(url);
    }
    return blob.size;
}

// RelayLogOverview 是请求状态流发送的完整进程内请求状态。
export interface RelayLogOverview {
    id: number;
    status: RequestState;
    started_at: string;
    first_byte_at?: string; // 首字节写出时间, 未提交时为空; 供卡片派生"首字时间"。
    duration: number;
    model: string;
    protocol: number;
    group_id: number;
    api_key_name: string;
    usage: RelayUsage;
    cost: number;
    round: number;
    round_started_at: string;
    target_channel: string;
    target_model: string;
    target_protocol: number;
    sending: boolean;
    error?: string;
}

// useClearLogs 清空已完成的内存日志。
export function useClearLogs() {
    return useMutation({
        mutationFn: () => apiRequest<null>('/api/v1/log/clear', { method: 'DELETE' }),
    });
}

// useStopRound 中止指定请求当前轮次匹配的上游调用。
export function useStopRound() {
    return useMutation({
        mutationFn: ({ requestId, round }: { requestId: number; round: number }) =>
            apiRequest<null>(`/api/v1/log/${requestId}/${round}/stop`, { method: 'POST' }),
    });
}

// useLogs 订阅进程内日志概览，并按 RequestID 更新同一条记录。
// refresh 供自动刷新偏好调用: 通过 bump 连接键重建 SSE 兜底断线/空闲, SSE 正常时仅重建连接。
export function useLogs() {
    const [logs, setLogs] = useState<RelayLogOverview[]>([]);
    const [isLoading, setIsLoading] = useState(true);
    const [error, setError] = useState<Error | null>(null);
    const [connectKey, setConnectKey] = useState(0);

    const refresh = () => setConnectKey((key) => key + 1);

    useEffect(() => {
        const source = new EventSource('/api/v1/log/overview/stream', { withCredentials: true });

        source.onopen = () => {
            setError(null);
            setIsLoading(false);
        };
        source.addEventListener('log', (event) => {
            let next: RelayLogOverview;
            try {
                next = JSON.parse((event as MessageEvent<string>).data) as RelayLogOverview;
            } catch {
                setError(new Error('Invalid log update'));
                return;
            }
            setIsLoading(false);
            setError(null);
            // 列表始终按 ID 倒序: 命中已有记录时原地替换, 新记录插入到首个更小 ID 之前,
            // 由此避免每条更新重排整个列表, 并保留未变更记录的引用以跳过卡片重渲染。
            setLogs((current) => {
                const index = current.findIndex((item) => item.id === next.id);
                if (index >= 0) {
                    const updated = current.slice();
                    updated[index] = next;
                    return updated;
                }
                const position = current.findIndex((item) => item.id < next.id);
                if (position < 0) return [...current, next];
                return [...current.slice(0, position), next, ...current.slice(position)];
            });
        });
        source.onerror = () => {
            setIsLoading(false);
            setError(new Error('Log stream disconnected'));
        };

        return () => {
            source.close();
        };
    }, [connectKey]);

    return { logs, isLoading, error, refresh };
}

// useLogRequestBody 在调用方启用时按需获取指定日志的请求体。
export function useLogRequestBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'request-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/${id}/request-body`),
        enabled,
        staleTime: Infinity,
    });
}

// useLogResponseBody 在调用方启用时获取指定日志的最终响应体。
export function useLogResponseBody(id: number, startedAt: string, enabled: boolean) {
    return useQuery({
        queryKey: ['logs', id, startedAt, 'response-body'],
        queryFn: () => apiRequest<string>(`/api/v1/log/${id}/response-body`),
        enabled,
        staleTime: Infinity,
    });
}
