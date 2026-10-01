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

    // ---- 统一标准（需求9）----
    /** 站点计费方式：metered 按量 / per_call 按次 / 空串未知 */
    billing_mode: string;
    /** 折成人民币后的每 1M token 价（已含站点倍率）；汇率为 0 时三项全为 0 */
    cny_input_price: number;
    cny_output_price: number;
    cny_cache_read_price: number;
    /** 按次计费的原始单价（每次调用多少美元），仅 per_call 有值 */
    per_call_price: number;
    /**
     * 折算所用的分母：本实例平均每次请求的 token 数。
     *
     * 必须显示在界面上（分母可见约定）：按次价折成每 token 价完全依赖它，
     * 不给出来用户就无法判断结论可不可信。
     */
    avg_tokens_per_request: number;
    /** 这一行是怎么折的（如"按次 $0.01 ÷ 平均 3200 token/次"）；空串=无需说明 */
    basis: string;
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

/** 价格对比支持排序的列（与后端 op.priceSortColumns 白名单一一对应）。 */
export type PriceSortColumn =
    | 'site'
    | 'group'
    | 'multiplier'
    | 'model'
    | 'input'
    | 'output'
    | 'cache_read'
    | 'cny_input'
    | 'cny_output'
    | 'per_call'
    | 'official_in'
    | 'official_out';

export const priceModelsQuery = (model: string, sort?: { column: PriceSortColumn; desc: boolean }) =>
    queryOptions({
        queryKey: ['price-models', model, sort?.column ?? '', sort?.desc ?? true],
        queryFn: () => {
            const params = new URLSearchParams();
            if (model) params.set('model', model);
            // 排序由后端做：数据量可达上千行，前端排会把"看到的"与"导出的"分成两套。
            if (sort?.column) {
                params.set('sort', sort.column);
                params.set('order', sort.desc ? 'desc' : 'asc');
            }
            const query = params.toString();
            return apiRequest<{ items: PriceModelRow[]; total: number }>(
                `/api/v1/price/models${query ? `?${query}` : ''}`,
            );
        },
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
