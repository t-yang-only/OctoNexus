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
export interface DimensionUsageStat {
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
    /**
     * 桶内按模型拆分的花费，与 by_model 同构（同一套尾部收敛）。
     *
     * 与 token 分开是因为两者回答不同的问题：「谁在吃 token」看的是量，
     * 「钱花在哪」看的是账 —— 同一个模型可能量很大但因为缓存命中而便宜，
     * 也可能量很小但单价高。只给 token 拆分的话，成本只能看到一个总数。
     */
    by_model_cost: Record<string, number>;
}

/**
 * 一个渠道的「模型链路」一致性：客户端请求的模型名 → 渠道内目标模型名 → 上游自称回报的模型名。
 *
 * 为什么单独一档：这三段名字不放在一起看时，**「正常别名解析」和「上游偷换模型」长得一模一样**。
 * 「请求 High-flash、实际跑 glm-5.3-flash」是分组名被解析成渠道模型名（正确路由）；
 * 「请求 deepseek-v4.1-flash、上游回报 deepseek-v4-flash-0731」才是版本被换。
 * 把前者算进不匹配率，界面就会把正常路由报成故障。
 */
export interface ModelChainStat {
    channel: string;
    requests: number;
    /** 请求名 ≠ 目标名：分组名/别名被解析成渠道内的真实模型名。**正常路由，不是异常。** */
    alias_resolved: number;
    /** 上游**真的回报了**模型名的行数 —— 它是不匹配率的分母（不是 requests）。 */
    reported: number;
    /** matched + mismatched === reported。 */
    matched: number;
    /** 上游回报的名字与渠道内目标名不一致：模型被换了。 */
    mismatched: number;
    /** 上游没回报模型名：**既不算一致也不算不一致**，是"无法判定"。 */
    silent: number;
    /** mismatched / reported；分母为 0 时为 0。 */
    mismatch_rate: number;
}

/** 一条具体的「上游换了模型」记录，用于从汇总定位到请求。 */
export interface ModelMismatchSample {
    id: number;
    /** 客户端请求的（通常是分组名这类虚拟名）。 */
    requested: string;
    /** 渠道内实际要的目标模型名。 */
    target_model: string;
    /** 上游在响应里自称的模型名。 */
    reported_model: string;
    channel: string;
    created_at: string;
}

/**
 * RelayLogSample 是画像样本的来源账（T-trace-006）。
 *
 * 画像默认把客户端声明的测试请求剔除：否则每次发版验证的那几条请求都会扰动成功率
 * 与延迟分布，让「这一版到底变好没有」失去可比性。
 *
 * 但剔除必须**可见**：只给剩余条数，会让人以为样本凭空少了，白白去查一次丢数据。
 * window 是窗口内扫过的原始条数，samples 是参与统计的条数，test_skipped 是被剔除的条数。
 * 剔除在 Go 侧做、不用 SQL WHERE —— 否则窗口会被一路撑到"N 条非测试日志"，
 * 排除前后的样本范围就不可比了，而那正是这个功能要解决的问题。
 */
export interface RelayLogSample {
    window: number;
    samples: number;
    test_skipped: number;
    truncated: boolean;
}

export interface AnalyticsOverview {
    /** 实际参与统计的条数（库里不够时会少于请求的 window）。 */
    window: number;
    sample: RelayLogSample;
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
    models: DimensionUsageStat[];
    /**
     * 按客户端 Key 聚合的同一份用量（回答"哪个调用方在花我的钱"）。
     *
     * 与 models 是同一批请求的两种切法，不是两份数据：一个 Key 可以在很多模型上花钱，
     * 反之亦然。缺任一都答不出对方的问题。
     */
    api_keys: DimensionUsageStat[];
    /** 按实际上游渠道聚合的用量（真正花钱的地方；failover 后与客户端填的分组名并不相同）。 */
    channels: DimensionUsageStat[];
    /** 按渠道的模型链路一致性（不匹配多的渠道排在最前）。 */
    model_chain: ModelChainStat[];
    /** 最近的「上游换了模型」记录，按时间倒序，最多 20 条。 */
    mismatch_samples: ModelMismatchSample[];
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
    sample: RelayLogSample;
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


// T-insight-007 选路判定与终止原因画像。
//
// 与 overview / latency 的分工：
//   overview 回答"多少量、多少钱"      latency 回答"有多慢"
//   这里回答"**谁在做主**"与"**怎么停的**"
//
// decision 与 stop_reason 这两个字段落库很久了（T-decision-001 / T-trace-003），
// 日志页看得到、导出有列，但一直没有任何统计读过它们 —— 想知道
// "亲和是不是把请求全粘在一个成员上""失败是预算耗尽还是上游集体拒绝"，
// 只能一行一行翻日志。
//
// **三个比例的分母各不相同**，界面上任何一处都不能拿 window 当分母：
//   reasons/modes → decision_recorded
//   tiers         → smart_count
//   stops         → stop_recorded

/** 「哪个机制决定了这次选择」。取值来自后端，界面不枚举白名单。 */
export interface RoutingReasonStat {
    reason: string;
    count: number;
    /** 分母是 decision_recorded，不是 window。 */
    ratio: number;
    success: number;
    canceled: number;
    failed: number;
}

export interface RoutingModeStat {
    mode: string;
    count: number;
    ratio: number;
}

export interface RoutingTierStat {
    tier: string;
    count: number;
    /** 分母是 smart_count（非智能路由的请求结构上没有档位）。 */
    ratio: number;
}

export interface RoutingSlotStat {
    /** 组内顶层序号（1 起）。 */
    slot: number;
    count: number;
    ratio: number;
}

export interface RoutingStopStat {
    reason: string;
    /** 与 reason 成对才有意义：同一个 reason 来自不同 source 时处置动作不同。 */
    source: string;
    count: number;
    /** 分母是 stop_recorded。 */
    ratio: number;
    success: number;
    canceled: number;
    failed: number;
}

export interface RoutingProfile {
    window: number;
    sample: RelayLogSample;
    decision_recorded: number;
    decision_unrecorded: number;
    decision_malformed: number;
    reasons: RoutingReasonStat[];
    modes: RoutingModeStat[];
    smart_count: number;
    tiers: RoutingTierStat[];
    /** 有顶层序号的行数（序号算不出时为 0，那种行不进样本）。 */
    slot_samples: number;
    slot_avg: number;
    slot_distribution: RoutingSlotStat[];
    stop_recorded: number;
    stop_unrecorded: number;
    stops: RoutingStopStat[];
}

/** 取选路判定与终止原因画像。 */
export function useAnalyticsRouting(window = 500, enabled = true) {
    return useQuery({
        queryKey: ['analytics', 'routing', window],
        queryFn: () => apiRequest<RoutingProfile>(`/api/v1/analytics/routing?window=${window}`),
        enabled,
    });
}

// T-insight-008 分组健康画像。
//
// ## 与既有画像的分工
//
//   overview / latency / routing 都在回答「多快 / 多少 / 走了谁」
//   这里回答「**这个分组还能不能用**」
//
// 两者看的不是同一件事：分组延迟画像里「样本 3 条、中位 200ms」看着不错，
// 但那 3 条可能全是失败（失败同样有耗时）；用量分析里「请求 0」既可能是没人用，
// 也可能是刚坏掉没人敢用。生产动机是验收时反复撞到的
// `53HK/Auto-Model 无可用成员` —— 这类失败没有 target_channel，
// 渠道故障统计看不见它，只有在分组维度才现形。

/** 分组当前状态的取值（后端枚举，界面映射文案；认不出的原样显示）。 */
export type GroupHealthState =
    | 'failing'
    | 'no_member'
    | 'degraded'
    | 'empty'
    | 'idle'
    | 'healthy';

/** 失败落在哪个上游（下钻用）。Channel 可能为空：那类失败没走到渠道。 */
export interface GroupHealthChannel {
    channel: string;
    model: string;
    failures: number;
    /** 空串 = 从未失败（RFC3339）。 */
    last_failure_at: string;
}

export interface GroupHealthRow {
    group_id: number;
    name: string;
    mode: string;
    /** 分组已被删除、日志还在保留期内。 */
    deleted: boolean;
    member_count: number;
    /** 当前还能转发的成员数——必须与 member_count 一起看。 */
    available_count: number;
    requests: number;
    success: number;
    canceled: number;
    request_fault: number;
    member_fault: number;
    transient_fault: number;
    unclassified: number;
    /** 分母是 success_samples，不是 requests。 */
    success_rate: number;
    /** 与分组服务能力有关的样本数；为 0 时成功率的 0 无意义。 */
    success_samples: number;
    last_failure_at: string;
    state: GroupHealthState | string;
    /** 失败最多的上游（上限 5 条）。 */
    failing_channels: GroupHealthChannel[];
}

export interface GroupHealth {
    window: number;
    sample: RelayLogSample;
    /** 判定线随结果返回——界面上的「故障/退化」必须与它对应。 */
    failing_threshold: number;
    degraded_threshold: number;
    failing_count: number;
    no_member_count: number;
    degraded_count: number;
    empty_count: number;
    idle_count: number;
    healthy_count: number;
    groups: GroupHealthRow[];
}

/** 取分组健康画像。 */
export function useAnalyticsGroupHealth(window = 500, enabled = true) {
    return useQuery({
        queryKey: ['analytics', 'group-health', window],
        queryFn: () => apiRequest<GroupHealth>(`/api/v1/analytics/group-health?window=${window}`),
        enabled,
    });
}