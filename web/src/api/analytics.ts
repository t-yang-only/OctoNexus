import { useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

// T-insight-002 请求窗口总览（读 relay_logs 明细行）。
//
// ## 与 useModelMonitor 的分工（两者刻意不同，都要有）
//
//   useModelMonitor  读 stats 快照 —— **累计画像**：这个模型/渠道总体怎么样
//   useAnalyticsOverview 读 relay_logs —— **窗口切片**：最近 N 条请求里发生了什么
//
// 关键差异：stats 快照在落库时就把明细聚合掉了，因此它**没有时间线、没有失败归因、
// 没有 RPM/TPM**——要回答"最近这几十条是不是变慢了、失败都归谁"只能回到明细行。
// 两者数据源不同，不是同一份数据的两种画法。

/** 单个模型在窗口内的用量画像。 */
export interface ModelUsageStat {
    model: string;
    /** 打向该模型的请求条数（含失败）。 */
    requests: number;
    success: number;
    /** 成功 / 全部，百分比；分母为 0 时给 0。 */
    success_rate: number;
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
    cached_tokens: number;
    /** 上游回报的思考 token 合计（不报的行贡献 0，绝不反推）。 */
    reasoning_tokens: number;
    cost: number;
    avg_duration_ms: number;
}

/** 时间线上的一个分桶。 */
export interface AnalyticsBucket {
    /** 桶标签：同一天为 "HH:00"，跨天为 "MM/DD"。 */
    bucket: string;
    bucket_at: number;
    requests: number;
    /** 桶内成功条数——只给总量的话，图上"量涨了"分不清是好事还是故障。 */
    success: number;
    tokens: number;
    cost: number;
    /** 桶内按模型拆分的 token，堆叠图每个色块一项；尾部模型并进 "__other__"。 */
    by_model: Record<string, number>;
}

export interface AnalyticsOverview {
    /** 实际参与统计的条数（库里不够时会少于请求的 window）。 */
    window: number;
    /** 首尾请求的实际时间差，即 RPM/TPM 的分母。 */
    span_seconds: number;
    request_count: number;
    success_count: number;
    success_rate: number;
    /** 排除「请求本身非法」，回答"上游本身健康吗"（口径同 fault-stats）。 */
    channel_rate: number;
    canceled: number;
    request_fault: number;
    member_fault: number;
    transient_fault: number;
    unclassified: number;
    prompt_tokens: number;
    completion_tokens: number;
    total_tokens: number;
    cached_tokens: number;
    reasoning_tokens: number;
    cache_hit_rate: number;
    total_cost: number;
    avg_cost_per_request: number;
    avg_duration_ms: number;
    /** 输出 token ÷ 耗时之和（不是窗口跨度，否则会被不发请求的空档稀释）。 */
    throughput_tps: number;
    avg_rpm: number;
    avg_tpm: number;
    models: ModelUsageStat[];
    /** 按时间升序。 */
    series: AnalyticsBucket[];
    /** 取满 window 条 —— 此时"总数"是最近 N 条而非全部历史，界面必须说明。 */
    truncated: boolean;
}

/** 取窗口总览。window 用条数而不是天数（口径同 fault-stats）。 */
export function useAnalyticsOverview(window = 500, enabled = true) {
    return useQuery({
        queryKey: ['analytics', 'overview', window],
        queryFn: () => apiRequest<AnalyticsOverview>(`/api/v1/analytics/overview?window=${window}`),
        enabled,
    });
}
