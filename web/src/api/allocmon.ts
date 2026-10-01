import { queryOptions, useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * 分压与限流监控（T-monitor-001）。
 *
 * 口径与后端 `GET /api/v1/monitor/allocation` 一一对应：剩余请求数、权重、健康系数全部由
 * 选路层自己算出来（与真正选路同一函数），这里只做类型声明与取数，不重复实现折算。
 */
export interface AllocationRow {
    item_id: number;
    group_id: number;
    group_name: string;
    channel_id: number;
    channel_name: string;
    model_name: string;
    key_name: string;
    priority: number;
    available: boolean;
    smart_tier: string;
    /** 剩余请求数是否已知；false 时 requests/source 无意义（面板显示"未知"而不是 0）。 */
    known: boolean;
    requests: number;
    /** monthly（包月余量）/ balance（余额折算）/ ""（未知）。 */
    source: string;
    weight: number;
    health_factor: number;
    counted: boolean;
    cooling: boolean;
    cooldown_ms: number;
    throttled: boolean;
    throttle_ms: number;
    throttle_hits: number;
    success_rate: number;
    has_samples: boolean;
    latency_ms: number;
    recent_reqs: number;
    recent_tokens: number;
    limited: boolean;
    // 速度（T-speed-001）：首帧与吞吐是"速度好不好"的两个可观测事实。
    speed_samples: number;
    ttfb_ms: number;
    tokens_per_sec: number;
    speed_ready: boolean;
    speed_factor: number;
    slow: boolean;
}

export interface AllocationSummary {
    group_count: number;
    member_count: number;
    known_count: number;
    unknown_count: number;
    cooling_count: number;
    throttled_count: number;
    limited_count: number;
    slow_count: number;
    total_requests: number;
    min_requests: number;
    max_requests: number;
    generated_at: number;
}

export interface AllocationSettingsView {
    estimate_tokens: number;
    health_weight: number;
    slow_latency_ms: number;
    min_requests: number;
    member_rpm_limit: number;
    member_tpm_limit: number;
    throttle_cap_seconds: number;
    points_per_unit: number;
    // 速度维度（T-speed-001）：面板展示"现在按什么标准判慢、折扣多强、看门狗收多少"。
    speed_weight: number;
    slow_ttfb_ms: number;
    slow_tokens_per_sec: number;
    first_event_multiple: number;
    first_event_floor_ms: number;
}

export interface AllocationSnapshot {
    summary: AllocationSummary;
    rows: AllocationRow[];
    mode_counts: Record<string, number>;
    settings: AllocationSettingsView;
}

// allocationQueryOptions 供首页监控区块使用：30 秒刷新一次，与渠道统计的刷新节奏一致。
export const allocationQueryOptions = queryOptions({
    queryKey: ['monitor', 'allocation'],
    queryFn: () => apiRequest<AllocationSnapshot>('/api/v1/monitor/allocation'),
    refetchInterval: 30000,
    refetchOnMount: 'always',
});

export function useAllocationMonitor() {
    return useQuery(allocationQueryOptions);
}
