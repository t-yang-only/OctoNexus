import { useMemo } from 'react';
import { useChannelStats } from './channel';
import { formatStatsMetrics } from './stats';

// 这里曾经还有一份 modelMonitorQueryOptions + useModelMonitorQuery：
// 注释写"供将来 R-spec 落定后的独立聚合接口预留"，但它**复用了 channel/stats 的
// queryKey 与 URL**。零调用方，且一旦将来真被调用，会与 useChannelStats 抢同一个
// 缓存条目、两处的 refetchInterval 互相覆盖（实测：monitor 里写 30s，
// channel 里另一套，同一个 key 两份策略）。预留接口腐化 + 抢 key，已删除。
//
// 取数统一走 useChannelStats：它与渠道列表页共用同一条查询，天然合并请求。

// MonitorModelRow 是按"渠道/模型"聚合的一行：与榜单 RankItem 口径一致，另补成功率与平均延迟。
export interface MonitorModelRow {
    id: string;
    modelName: string;
    channelName: string;
    count: number;
    success: number;
    failed: number;
    successRate: number; // 0-100。
    avgWaitMs: number; // 平均每次请求的 wait_time（毫秒）。
    totalToken: number;
    totalCost: number;
}

export interface MonitorSummary {
    totalCount: number;
    totalCost: number;
    totalTokens: number;
    avgSuccessRate: number; // 全局成功率 0-100。
    avgWaitMs: number; // 全局平均延迟（毫秒）。
    modelCount: number;
}

// useModelMonitor 把渠道统计拍平成"模型调用分析"所需的汇总与明细。
// 成功率 = success / (success + failed)；RPM/TPM 由调用方按所选时间窗折算，
// 此处只给原始累计，避免把"时间窗"假设写死进 hook。
export function useModelMonitor() {
    const { data: channelStats } = useChannelStats();
    return useMemo(() => {
        const rows: MonitorModelRow[] = (channelStats ?? []).flatMap((channel) =>
            channel.models.map((channelModel) => {
                const success = channelModel.formatted.request_success.raw;
                const failed = channelModel.formatted.request_failed.raw;
                const count = success + failed;
                return {
                    id: `model-${channelModel.model_id}`,
                    modelName: channelModel.model_name,
                    channelName: channel.channel_name,
                    count,
                    success,
                    failed,
                    successRate: count > 0 ? (success / count) * 100 : 0,
                    avgWaitMs: count > 0 ? channelModel.formatted.wait_time.raw / count : 0,
                    totalToken: channelModel.formatted.total_token.raw,
                    totalCost: channelModel.formatted.total_cost.raw,
                } satisfies MonitorModelRow;
            })
        );
        // 汇总由明细行累加：与首页 Total 的口径（StatsMetrics 求和）一致，不再单独调 total 接口。
        const total = rows.reduce(
            (acc, row) => ({
                count: acc.count + row.count,
                success: acc.success + row.success,
                wait: acc.wait + row.avgWaitMs * row.count,
                tokens: acc.tokens + row.totalToken,
                cost: acc.cost + row.totalCost,
            }),
            { count: 0, success: 0, wait: 0, tokens: 0, cost: 0 }
        );
        const summary: MonitorSummary = {
            totalCount: total.count,
            totalCost: total.cost,
            totalTokens: total.tokens,
            avgSuccessRate: total.count > 0 ? (total.success / total.count) * 100 : 0,
            avgWaitMs: total.count > 0 ? total.wait / total.count : 0,
            modelCount: rows.length,
        };
        return { rows, summary };
    }, [channelStats]);
}

// formatMonitorMetrics 复用 formatStatsMetrics 的口径说明：
// 明细行的展示格式化在组件内按 formatCount/formatMoney/formatTime 就地处理，
// 不在此另建格式化层，避免与榜单/趋势两处口径分叉。
export { formatStatsMetrics };
