import { queryOptions } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * 上游价格与用量快照（T-price-001 / T-price-002）
 *
 * 数据来源：各中转站自己的 Web API（登录换 token 后读 /api/v1/model-plaza 与
 * /api/v1/usage/dashboard/stats），由抓取端 POST /api/v1/price/import 入库。
 * 面板只读快照，不自己去抓——抓取要登录态、出口和验证码，属于采集侧的事。
 */
export interface PriceModelRow {
    site: string;
    group_name: string;
    multiplier: number;
    model: string;
    tier: string;
    /** 实付价（已乘分组倍率），USD / 1M token */
    input_price: number;
    output_price: number;
    cache_read_price: number;
    /** 官方价（未乘倍率），用于看"省了多少" */
    official_input: number;
    official_output: number;
    discount: number;
    captured_at: string;
}

export interface PriceUsageRow {
    site: string;
    requests: number;
    input_tokens: number;
    output_tokens: number;
    cache_read_tokens: number;
    cache_write_tokens: number;
    total_tokens: number;
    actual_cost: number;
    balance: number;
    captured_at: string;
    avg_cost_per_request: number;
    cache_read_ratio: number;
    /** 非空即为后端判定的异常（面板只展示结论，不各自重算） */
    anomaly: string;
}

export const priceModelsQuery = (model: string) =>
    queryOptions({
        queryKey: ['price-models', model],
        queryFn: () =>
            apiRequest<{ items: PriceModelRow[]; total: number }>(
                `/api/v1/price/models${model ? `?model=${encodeURIComponent(model)}` : ''}`,
            ),
    });

export const priceUsageQuery = () =>
    queryOptions({
        queryKey: ['price-usage'],
        queryFn: () => apiRequest<{ items: PriceUsageRow[]; total: number }>('/api/v1/price/usage'),
    });

export async function importPriceSnapshot(payload: unknown) {
    return apiRequest<{ price_rows: number; usage_rows: number }>('/api/v1/price/import', {
        method: 'POST',
        body: payload,
    });
}
