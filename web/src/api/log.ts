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
    // reported_model 是上游响应体里回报的模型名（T-verify-001）。
    // 与 target_model 的区别是关键：后者是「我们请求了什么」，前者是「上游自称用了什么」。
    // 空串表示上游没回报该字段（常见现象），此时不判定。
    reported_model: string;
    // model_mismatch 标记上游回报的模型与请求的不一致。
    // 这是「上游偷换模型」的唯一可见证据——按高价模型收费却用低价模型出货，
    // 只看我们自己的记录永远发现不了。
    model_mismatch: boolean;
    started_at: string;
    first_byte_ms: number;
    duration_ms: number;
    // attempts 是本请求打向上游的轮次数: 1 = 第一次就出结果, >1 = 中途换过成员; 首字竞速的多路并算一轮。
    attempts: number;
    // decision 是这次请求的选路判定（后端 relay.Decision.Text()）: 形如
    // "mode=smart;tier=decision;reason=affinity;slot=1;attempt=2", 回答"为什么走了这个成员"。
    // 未发起上游请求就结束的请求为空串。
    decision: string;
    prompt_tokens: number;
    cached_tokens: number;
    completion_tokens: number;
    cost: number;
    // fault_kind 是失败的归因分类: request / member / transient，空串表示非失败或未分类。
    // 与 Status 的区别是关键: Status 只说"这次失败了"，FaultKind 说"这次失败该算在谁的账上"。
    // 后端在产生错误的那一刻定类并落库（历史）或随 SSE 下发（实时），前端只做展示与筛选，绝不重新分类。
    fault_kind?: FaultKind;
    error: string;
}

// FaultKind 是失败归因分类，与后端 model.RelayLog.FaultKind 一一对应。
// request=请求本身非法（不计渠道故障）；member=成员自身问题；transient=可恢复（超时/网络/5xx）。
// undefined 或空串表示未分类：可能非失败，也可能是升级前写入的历史行 —— 两者都不得猜测归类。
export type FaultKind = 'request' | 'member' | 'transient';

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
    // decision 是本轮的选路判定（后端 relay.Decision.Text()），每轮刷新；空串表示尚未走到选路。
    decision?: string;
    target_channel: string;
    target_model: string;
    target_protocol: number;
    // reported_model / model_mismatch 与 RelayHistoryItem 同义（T-verify-001）：
    // 前者是上游自称用了什么，后者标记它与请求的模型不一致。实时快照同样带这两个字段。
    reported_model?: string;
    model_mismatch?: boolean;
    sending: boolean;
    // fault_kind 同 RelayHistoryItem：失败归因分类，实时流由后端直接下发（RequestState 带该字段）。
    fault_kind?: FaultKind;
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

// T-usability-008 真实通过率（失败按归因分桶）。
//
// 两个通过率回答两个不同的问题，只给一个必然误导一半场景：
//   successRate  用户发起的请求有多少成功了 —— 体验视角
//   channelRate  这个渠道本身健康吗       —— 诊断视角（排除了「请求本身非法」）
//
// 实测：senseaudio 的 successRate 只有 31.6%，但 channelRate 接近 100% ——
// 那 25 次失败全是「用 chat 接口调 TTS/图像模型」造成的请求非法，跟渠道无关。
export interface RelayFaultCounts {
    success: number;
    /** 客户端主动断开：既不算成功也不算失败。 */
    canceled: number;
    /** 请求本身非法（400 一类）：任何成员都会同样拒绝，**不计入渠道健康度**。 */
    request_fault: number;
    /** 成员自身问题（凭据无效/无权限/模型不存在）：算渠道故障。 */
    member_fault: number;
    /** 可恢复失败（超时/限流/5xx/网络）：算渠道故障。 */
    transient_fault: number;
    /**
     * 失败但未归类（升级前的存量行没有 fault_kind）。
     *
     * 单独列出而不是并进某一类：**不知道的不能猜** ——
     * 猜成渠道故障会把历史账算到渠道头上，而原因根本不在它身上。
     */
    unclassified: number;
}

export interface ChannelFaults extends RelayFaultCounts {
    channel: string;
    success_rate: number;
    channel_rate: number;
}

export interface RelayFaultStats extends RelayFaultCounts {
    window: number;
    channels: ChannelFaults[];
}

// useRelayFaultStats 取真实通过率。
// window 用**条数**而不是天数：部署后流量差异极大，
// 按天取会让「最近一天只有 3 条」的渠道得出毫无意义的比例。
export function useRelayFaultStats(window = 500, enabled = true) {
    return useQuery({
        queryKey: ['logs', 'fault-stats', window],
        queryFn: () => apiRequest<RelayFaultStats>(`/api/v1/log/fault-stats?window=${window}`),
        enabled,
    });
}