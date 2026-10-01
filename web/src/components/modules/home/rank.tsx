import { useState } from 'react';
import { Search, TrendingUp } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useChannelStats } from '@/api/channel';
import { Input } from '@/components/ui/input';
import { useHomeViewStore, type MetricKey } from './store';
import { MetricTabs } from './metric-tabs';
import type { StatsMetricsFormatted } from '@/api/stats';

// 榜单默认只列前几名。生产实测模型榜 358 条，其中 179 条零活动 ——
// 全铺进 300px 的滚动框里, 真正有用的头部几名会被一屏根本看不到的尾巴淹没。
const RANK_PREVIEW = 10;

// 榜单只展示这几项, 渠道可直接复用 api/channel 已算好的 formatted。
type RankMetrics = Pick<
    StatsMetricsFormatted,
    'total_cost' | 'total_token' | 'request_count' | 'request_success' | 'request_failed'
>;

// 榜单中的一个条目, 渠道和模型共用。
interface RankItem {
    id: string;
    name: string; // 渠道榜为渠道名, 模型榜为模型名。
    channelName?: string; // 仅模型榜有值; 有值则 name 是模型名, 模糊渠道名时只糊此项。
    formatted: RankMetrics;
}

// RankCard 渲染单个排行榜: 标题, 维度切换和榜单列表。
function RankCard({
    title,
    items,
    sortMode,
    onSortModeChange,
    hideChannelName,
}: {
    title: string;
    items: RankItem[];
    sortMode: MetricKey;
    onSortModeChange: (value: MetricKey) => void;
    hideChannelName?: boolean;
}) {
    const t = useTranslations('home.rank');
    const [filter, setFilter] = useState('');
    const [expanded, setExpanded] = useState(false);
    const sortField = sortMode === 'cost' ? 'total_cost' : sortMode === 'count' ? 'request_count' : 'total_token';
    const ranked = [...items].sort((a, b) => b.formatted[sortField].raw - a.formatted[sortField].raw);

    // 名次按完整榜单先算好并记住。搜索/截断只改变"显示哪几行",
    // 不应改变"它是第几名"——否则过滤后第 200 名显示成"第 1 名"是假话。
    const rankOf = new Map(ranked.map((item, index) => [item.id, index + 1]));

    const keyword = filter.trim().toLowerCase();
    const matched = keyword
        ? ranked.filter(
              (item) =>
                  item.name.toLowerCase().includes(keyword) ||
                  (item.channelName ?? '').toLowerCase().includes(keyword)
          )
        : ranked;

    // 搜索时自动展开: 否则搜到的结果仍被前 N 条截掉, 会被读成"搜不到"。
    const visible = keyword || expanded ? matched : matched.slice(0, RANK_PREVIEW);
    const hiddenCount = matched.length - visible.length;

    return (
        <div className="rounded-3xl bg-card text-card-foreground border-border border pt-2 px-4">
            <div className="flex items-center justify-between">
                <h3 className="font-semibold text-base">{title}</h3>
                <MetricTabs value={sortMode} onChange={onSortModeChange} />
            </div>

            {ranked.length > 0 && (
                <div className="mt-2 flex items-center gap-2">
                    <div className="relative flex-1">
                        <Search className="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
                        <Input
                            value={filter}
                            onChange={(event) => setFilter(event.target.value)}
                            placeholder={t('filterPlaceholder')}
                            className="h-8 pl-8 text-xs"
                        />
                    </div>
                    {filter.trim() !== '' && (
                        <button
                            type="button"
                            onClick={() => setFilter('')}
                            className="shrink-0 text-xs text-muted-foreground hover:text-foreground"
                        >
                            {t('clear')}
                        </button>
                    )}
                </div>
            )}

            {ranked.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
                    <TrendingUp className="w-12 h-12 mb-3 opacity-30" />
                    <p className="text-sm">{t('noData')}</p>
                </div>
            ) : matched.length === 0 ? (
                <div className="flex flex-col items-center justify-center py-8 text-muted-foreground">
                    <Search className="w-10 h-10 mb-3 opacity-30" />
                    <p className="text-sm">{t('filterNoMatch')}</p>
                </div>
            ) : (
                <>
                    <p className="mt-2 text-xs text-muted-foreground">
                        {t('showing', { shown: visible.length, total: items.length })}
                    </p>
                    <div className="space-y-3 max-h-[300px] overflow-y-auto">
                        {visible.map((item) => {
                            const successCount = item.formatted.request_success.raw;
                            const totalCount = successCount + item.formatted.request_failed.raw;

                            return (
                                <div key={item.id} className="grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 py-3">
                                    <div className="flex items-center justify-center font-bold text-lg">{rankOf.get(item.id)}</div>

                                    <div className="min-w-0">
                                        <p className={`font-medium text-sm truncate ${hideChannelName && !item.channelName ? 'select-none blur-[3px]' : ''}`}>
                                            {item.name}
                                        </p>
                                        {item.channelName && (
                                            <p className={`mt-1 truncate text-xs text-muted-foreground ${hideChannelName ? 'select-none blur-[3px]' : ''}`}>
                                                {item.channelName}
                                            </p>
                                        )}
                                        {sortMode === 'count' && (
                                            <div className="flex items-center gap-1 text-xs text-muted-foreground mt-1">
                                                <span>{t('successRate')}:</span>
                                                <span>{(totalCount > 0 ? (successCount / totalCount) * 100 : 0).toFixed(1)}%</span>
                                            </div>
                                        )}
                                    </div>

                                    <div className="flex items-center gap-1 text-right">
                                        {sortMode === 'count' ? (
                                            <div className="flex items-center gap-1 text-sm font-medium tabular-nums">
                                                <span className="text-accent">
                                                    {item.formatted.request_success.formatted.value}
                                                    <span className="text-xs text-muted-foreground">
                                                        {item.formatted.request_success.formatted.unit}
                                                    </span>
                                                </span>
                                                <span className="text-muted-foreground/40 font-light">/</span>
                                                <span className="text-destructive">
                                                    {item.formatted.request_failed.formatted.value}
                                                    <span className="text-xs text-muted-foreground">
                                                        {item.formatted.request_failed.formatted.unit}
                                                    </span>
                                                </span>
                                            </div>
                                        ) : (
                                            <span className="font-semibold text-base">
                                                {item.formatted[sortField].formatted.value}
                                                <span className="text-xs text-muted-foreground">
                                                    {item.formatted[sortField].formatted.unit}
                                                </span>
                                            </span>
                                        )}
                                    </div>
                                </div>
                            );
                        })}
                    </div>

                    {hiddenCount > 0 && (
                        <button
                            type="button"
                            onClick={() => setExpanded(true)}
                            className="mt-3 w-full rounded-xl border border-border py-2 text-xs text-muted-foreground transition-colors hover:text-foreground"
                        >
                            {t('showMore', { count: hiddenCount })}
                        </button>
                    )}
                    {expanded && keyword === '' && (
                        <button
                            type="button"
                            onClick={() => setExpanded(false)}
                            className="mt-2 w-full rounded-xl py-2 text-xs text-muted-foreground transition-colors hover:text-foreground"
                        >
                            {t('collapse')}
                        </button>
                    )}
                </>
            )}
        </div>
    );
}

// Rank 并列渠道榜和模型榜, 两榜各自独立排序。
export function Rank() {
    const { data: channelStats } = useChannelStats();
    const t = useTranslations('home.rank');
    const channelSortMode = useHomeViewStore((state) => state.channelRankSortMode);
    const setChannelSortMode = useHomeViewStore((state) => state.setChannelRankSortMode);
    const modelSortMode = useHomeViewStore((state) => state.modelRankSortMode);
    const setModelSortMode = useHomeViewStore((state) => state.setModelRankSortMode);
    const isChannelNameHidden = useHomeViewStore((state) => state.isChannelNameHidden);

    const channelItems: RankItem[] = (channelStats ?? []).map((channel) => ({
        id: `channel-${channel.channel_id}`,
        name: channel.channel_name,
        formatted: channel.formatted,
    }));

    const modelItems: RankItem[] = (channelStats ?? []).flatMap((channel) =>
        channel.models.map((channelModel) => ({
            id: `model-${channelModel.model_id}`,
            name: channelModel.model_name,
            channelName: channel.channel_name,
            formatted: channelModel.formatted,
        }))
    );

    return (
        <div className="grid grid-cols-1 @3xl/home:grid-cols-2 gap-4">
            <RankCard
                title={t('channel')}
                items={channelItems}
                sortMode={channelSortMode}
                onSortModeChange={setChannelSortMode}
                hideChannelName={isChannelNameHidden}
            />
            <RankCard
                title={t('model')}
                items={modelItems}
                sortMode={modelSortMode}
                onSortModeChange={setModelSortMode}
                hideChannelName={isChannelNameHidden}
            />
        </div>
    );
}
