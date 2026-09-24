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

// T-insight-004 全局延迟分布。
//
// ## 与渠道/分组耗时画像的分工（三者都要有）
//
//   ChannelLatencyStats / GroupLatencyStats 按维度切开 —— 回答"谁快谁慢"
//   本接口整体统计                          —— 回答"现在整体有多慢"
//
// 后者的判据与前两者不同：挑渠道看**中位数**（典型体验），
// 看整体健康看**尾部分位**（p95/p99）。一个 p50 漂亮但 p95 巨大的系统，
// 体验是"大多数时候快、偶尔卡死"，这与"一直不快"是两种病、处置也不同。

/** 一组固定分位数。样本为 0 时全部给 0（界面按 samples===0 显示「—」）。 */
export interface LatencyQuantiles {
    samples: number;
    min_ms: number;
    max_ms: number;
    p50_ms: number;
    p90_ms: number;
    p95_ms: number;
    p99_ms: number;
}

/** 分布的尾部：慢到什么程度、有多少。 */
export interface LatencyTail {
    /** 判定"慢"的阈值，随结果返回——界面上的"慢"字必须与它对应。 */
    threshold_ms: number;
    slow_count: number;
    slow_ratio: number;
    /** 超过 60 秒的样本数：跨过这条心理线后，请求即使成功也已不可用。 */
    over_one_minute_count: number;
}

export interface LatencyHistogramBucket {
    /** 区间上界（毫秒，不含）；**0 表示"及以上"**（最后那个桶）。 */
    upper_ms: number;
    count: number;
    ratio: number;
}

export interface LatencyDistribution {
    /** 扫描到的日志条数（含没走到渠道的）。 */
    window: number;
    slow_threshold_ms: number;
    /** 只统计真的记到首字节的请求（first_byte_ms >= 0）。 */
    first_byte: LatencyQuantiles;
    /** 统计所有耗时 > 0 的请求，**含失败**——一次 60 秒超时正是最该被看见的慢。 */
    duration: LatencyQuantiles;
    first_byte_tail: LatencyTail;
    duration_tail: LatencyTail;
    /** 固定区间的直方图（空桶也会给，共 bounds+1 个）。 */
    duration_histogram: LatencyHistogramBucket[];
    /** 日志条数达到 window 上限：统计只覆盖了最近的一部分。 */
    truncated: boolean;
}

/** 取全局延迟分布。 */
export function useAnalyticsLatency(window = 500, enabled = true) {
    return useQuery({
        queryKey: ['analytics', 'latency', window],
        queryFn: () => apiRequest<LatencyDistribution>(`/api/v1/analytics/latency?window=${window}`),
        enabled,
    });
}
