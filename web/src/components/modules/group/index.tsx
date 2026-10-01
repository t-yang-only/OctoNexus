import { useMemo, useState } from 'react';
import { ArrowUpAZ, ChevronDown, ChevronRight, Layers } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { GroupCard } from './Card';
import { CreateDialogContent } from './Create';
import { QuickCreateDialogContent } from './QuickCreate';
import { useRuntimeClock } from './MemberStatus';
import { GroupLatencyPanel } from './GroupLatencyPanel';
import { GroupUsageBanner } from './UsageBanner';
import { useGroupList, groupChannelPrefix } from '@/api/group';
import type { Group } from '@/api/group';
import { PageActions, usePageActionsStore } from '@/components/common/PageActions';
import { VirtualizedGrid } from '@/components/common/VirtualizedGrid';
import { MorphingDialogTrigger } from '@/components/ui/morphing-dialog';

// GroupActions 向稳定顶栏提供分组页面的搜索、视图选项和创建入口。
export function GroupActions() {
    const t = useTranslations('toolbar');
    const tg = useTranslations('group');
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);
    const setSearchTerm = usePageActionsStore((state) => state.setSearchTerm);
    const setSort = usePageActionsStore((state) => state.setSort);
    const setFilter = usePageActionsStore((state) => state.setGroupFilter);

    return (
        <PageActions
            searchTerm={searchTerm}
            onSearchTermChange={(value) => setSearchTerm('group', value)}
            sortOptions={[
                { value: 'asc', label: t('popover.nameAsc'), icon: ArrowUpAZ },
                { value: 'desc', label: t('popover.nameDesc'), icon: ArrowUpAZ },
            ]}
            sortValue={sortOrder}
            onSortChange={(value) => {
                if (value === 'asc' || value === 'desc') setSort('group', value);
            }}
            filterOptions={[
                { value: 'all', label: t('popover.filter.group.all') },
                { value: 'with-members', label: t('popover.filter.group.withMembers') },
                { value: 'empty', label: t('popover.filter.group.empty') },
            ]}
            filterValue={filter}
            onFilterChange={(value) => {
                if (value === 'all' || value === 'with-members' || value === 'empty') setFilter(value);
            }}
        >
            {/* 快速建组放在常规建组左边：它是更高频的入口，而常规编辑器面向精细编排。 */}
            <MorphingDialogTrigger>
                <button
                    type="button"
                    className="flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs hover:bg-muted"
                >
                    <Layers className="h-3.5 w-3.5" />
                    {tg('quickCreate.button')}
                </button>
                <QuickCreateDialogContent />
            </MorphingDialogTrigger>
            <CreateDialogContent />
        </PageActions>
    );
}

// channelGroupsOf 把分组按 `渠道/模型` 的前缀聚成渠道组。
//
// 为什么按名字前缀而不是查库拿渠道 ID：分组与渠道在数据模型上没有外键
// （成员才指向渠道授权），而这个前缀是建组时我们自己写进去的约定，
// 用显示名就能折叠，不需要后端多带一份字段。
function channelGroupsOf(groups: Group[]) {
    const folding = new Map<string, Group[]>();
    const loose: Group[] = [];
    for (const g of groups) {
        const prefix = groupChannelPrefix(g.name);
        if (prefix === null) {
            loose.push(g);
            continue;
        }
        const list = folding.get(prefix) ?? [];
        list.push(g);
        folding.set(prefix, list);
    }
    return { folding: [...folding.entries()].sort((a, b) => a[0].localeCompare(b[0])), loose };
}

// Group 渲染分组列表正文。
export function Group() {
    const t = useTranslations('group');
    const { data: groups } = useGroupList(true, true);
    const runtimeNow = useRuntimeClock(groups);
    const searchTerm = usePageActionsStore((state) => state.searchTerms.group || '');
    const sortOrder = usePageActionsStore((state) => state.sortOrders.group === 'desc' ? 'desc' : 'asc');
    const filter = usePageActionsStore((state) => state.groupFilter);
    // 折叠状态：默认全部折叠，只显示渠道名 —— 一个渠道下几十个模型时，
    // 展开的列表会把这些渠道的同名条目混成一片，找什么都不方便。
    const [expanded, setExpanded] = useState<Record<string, boolean>>({});

    const visibleGroups = useMemo(() => {
        if (!groups) return [];
        const term = searchTerm.toLowerCase().trim();
        let list = !term ? [...groups] : groups.filter((g) => g.name.toLowerCase().includes(term));
        if (filter === 'with-members') list = list.filter((g) => (g.items?.length || 0) > 0);
        if (filter === 'empty') list = list.filter((g) => (g.items?.length || 0) === 0);
        return list.sort((a, b) =>
            sortOrder === 'asc' ? a.name.localeCompare(b.name) : b.name.localeCompare(a.name)
        );
    }, [groups, searchTerm, filter, sortOrder]);

    const { folding, loose } = useMemo(() => channelGroupsOf(visibleGroups), [visibleGroups]);

    // 空态：VirtualizedGrid 在 items 为空时什么都不渲染 —— 与渠道页同一类问题
    // （零分组或被搜索/筛选挡掉时，整页只剩标题，用户看不出该去哪儿建分组）。
    if (visibleGroups.length === 0) {
        const noGroupAtAll = (groups ?? []).length === 0;
        return (
            <div className="flex h-full min-h-0 flex-col items-center justify-center gap-2 p-8 text-center">
                <p className="text-sm font-medium">
                    {t(noGroupAtAll ? 'empty.title' : 'empty.filteredTitle')}
                </p>
                <p className="max-w-sm text-xs text-muted-foreground">
                    {t(noGroupAtAll ? 'empty.hint' : 'empty.filteredHint')}
                </p>
            </div>
        );
    }

    const renderGroup = (group: Group) => {
        let deadline = group.runtime.affinity_until;
        for (const cooldownUntil of Object.values(group.runtime.cooldowns)) {
            deadline = Math.max(deadline, cooldownUntil);
        }
        return <GroupCard group={group} now={deadline > runtimeNow ? runtimeNow : deadline} />;
    };

    // 使用情况横幅放在列表上方：它回答的是「我建的这些分组哪些在用」，
    // 而这个问题在条目列表里完全看不出来。用 flex 包一层让横幅固定、列表自己滚动。
    return (
        <div className="flex h-full min-h-0 flex-col">
            <GroupUsageBanner />
            {/* 分组维度的耗时：用户调用的是分组而不是渠道，
                而分组内部还要选路 —— 这个数据只有按分组统计才看得见。 */}
            <GroupLatencyPanel />
            <div className="min-h-0 flex-1 overflow-y-auto">
                {/* 折叠区：每个渠道一行标题，展开才渲染它的模型分组。 */}
                {folding.map(([channelName, list]) => {
                    const open = expanded[channelName] ?? false;
                    return (
                        <section key={channelName} className="border-b last:border-b-0">
                            <button
                                type="button"
                                onClick={() => setExpanded((prev) => ({ ...prev, [channelName]: !open }))}
                                className="flex w-full items-center gap-2 px-3 py-2 text-left hover:bg-muted/50"
                            >
                                {open ? (
                                    <ChevronDown className="h-4 w-4 shrink-0 text-muted-foreground" />
                                ) : (
                                    <ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground" />
                                )}
                                <span className="text-sm font-medium">{channelName}</span>
                                <span className="rounded bg-muted px-1.5 py-0.5 text-xs text-muted-foreground">
                                    {t('fold.count', { count: list.length })}
                                </span>
                            </button>
                            {open && (
                                <VirtualizedGrid
                                    items={list}
                                    columns={{ default: 1, md: 2, lg: 3 }}
                                    estimateItemHeight={520}
                                    getItemKey={(group) => group.id}
                                    renderItem={renderGroup}
                                />
                            )}
                        </section>
                    );
                })}

                {/* 非 `渠道/模型` 命名的手工分组照旧平铺 —— 它们不属任何渠道前缀。 */}
                {loose.length > 0 && (
                    <VirtualizedGrid
                        items={loose}
                        columns={{ default: 1, md: 2, lg: 3 }}
                        estimateItemHeight={520}
                        getItemKey={(group) => group.id}
                        renderItem={renderGroup}
                    />
                )}
            </div>
        </div>
    );
}