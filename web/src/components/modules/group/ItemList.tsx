import { useEffect, useId, useRef, useState } from 'react';
import { Layers, GripVertical, X, Trash2 } from 'lucide-react';
import {
    DragDropContext,
    Draggable,
    Droppable,
    type DraggableProvided,
    type DropResult,
} from '@hello-pangea/dnd';
import { motion, AnimatePresence } from 'motion/react';
import { cn } from '@/lib/utils';
import { getModelIcon } from '@/lib/model-icons';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { useTranslations } from 'use-intl';
import type { Group } from '@/api/group';
import { MemberStatus } from './MemberStatus';

// SMART_TIER_CYCLE 是成员档位的切换顺序：未声明（按顺序自动切分）-> 决策引擎 -> 执行引擎 -> 未声明。
const SMART_TIER_CYCLE = ['', 'decision', 'execution'] as const;

// nextSmartTier 返回点击一次之后的档位；未知取值一律回落到「未声明」，不扩散脏值。
function nextSmartTier(tier?: string): string {
    const index = SMART_TIER_CYCLE.indexOf((tier ?? '') as (typeof SMART_TIER_CYCLE)[number]);
    return SMART_TIER_CYCLE[index < 0 ? 0 : (index + 1) % SMART_TIER_CYCLE.length];
}

export interface SelectedMember {
    id: string;
    kind: 'grant' | 'child';
    channel_grant_id: number;
    child_group_id: number;
    name: string;
    enabled: boolean;
    channel_id: number;
    channel_name: string;
    key_name: string;
    protocols: number;
    // smart_tier 是智能路由的显式档位：空/缺省 = 按成员顺序自动切分（既有行为），
    // 'decision'/'execution' = 显式指定该成员属于哪一档。
    smart_tier?: string;
    item_id?: number;
}

function reorderList<T>(list: T[], startIndex: number, endIndex: number): T[] {
    const result = [...list];
    const [removed] = result.splice(startIndex, 1);
    result.splice(endIndex, 0, removed);
    return result;
}

type MemberItemDnd = {
    innerRef: DraggableProvided['innerRef'];
    draggableProps: DraggableProvided['draggableProps'];
    dragHandleProps: DraggableProvided['dragHandleProps'];
    isDragging: boolean;
};

// MemberItem 渲染可拖拽成员及其删除确认状态。
function MemberItem({
    member,
    onRemove,
    onActivate,
    isActive,
    group,
    now,
    isRemoving,
    showConfirmDelete = true,
    layoutScope,
    dnd,
    smartMode,
    onTierChange,
}: {
    member: SelectedMember;
    onRemove: (id: string) => void;
    onActivate?: (itemId: number) => void;
    isActive?: boolean;
    group?: Group; // group 提供成员当前的冷却和亲和时间。
    now: number; // now 是成员列表共享的当前 Unix 毫秒时间。
    isRemoving?: boolean;
    showConfirmDelete?: boolean;
    layoutScope?: string;
    dnd: MemberItemDnd;
    smartMode?: boolean; // 智能路由模式下才显示档位按钮：其它模式不读这个字段，显示了只会让人分心。
    onTierChange?: (id: string, tier: string) => void;
}) {
    const t = useTranslations('group');
    const { Icon, className: iconClassName } = getModelIcon(member.name);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const isDisabled = member.enabled === false;
    const isChild = member.kind === 'child';

    return (
        <div
            // DnD libraries provide imperative refs/props; the hook lint rule (`react-hooks/refs`)
            // flags this pattern, but it's safe and required for correct drag behavior.
            // eslint-disable-next-line react-hooks/refs
            ref={dnd.innerRef}
            // eslint-disable-next-line react-hooks/refs
            {...dnd.draggableProps}
            className={cn('rounded-lg grid transition-[grid-template-rows] duration-200', isRemoving ? 'grid-rows-[0fr]' : 'grid-rows-[1fr]')}
            // eslint-disable-next-line react-hooks/refs
            style={{
                /* eslint-disable-next-line react-hooks/refs */
                ...(dnd.draggableProps?.style ?? {}),
                /* eslint-disable-next-line react-hooks/refs */
                ...(dnd.isDragging ? { zIndex: 50, boxShadow: '0 8px 32px rgba(0,0,0,0.15)' } : null),
            }}
        >
            <div className={cn(
                'flex items-center gap-2 rounded-lg bg-background px-2.5 py-2 select-none transition-[background-color,opacity] duration-200 relative overflow-hidden',
                isRemoving && 'opacity-0',
                isDisabled && 'opacity-60 grayscale',
                onActivate && member.item_id !== undefined && 'cursor-pointer'
            )}
                onClick={() => member.item_id !== undefined && onActivate?.(member.item_id)}
                onKeyDown={(event) => {
                    if (event.target !== event.currentTarget || member.item_id === undefined || !onActivate || (event.key !== 'Enter' && event.key !== ' ')) return;
                    event.preventDefault();
                    onActivate(member.item_id);
                }}
                role={onActivate && member.item_id !== undefined ? 'button' : undefined}
                tabIndex={onActivate && member.item_id !== undefined ? 0 : undefined}
            >
                <div
                    className={cn(
                        'p-0.5 rounded touch-none transition-colors',
                        isDisabled
                            ? 'cursor-grab active:cursor-grabbing hover:bg-muted/60'
                            : 'cursor-grab active:cursor-grabbing hover:bg-muted'
                    )}
                    // eslint-disable-next-line react-hooks/refs
                    {...dnd.dragHandleProps}
                    onClick={(event) => event.stopPropagation()}
                >
                    <GripVertical className="size-3.5 text-muted-foreground" />
                </div>

                <span className={cn(isDisabled && 'opacity-70')}>
                    <Icon aria-hidden="true" className={iconClassName} width={18} height={18} />
                </span>

                <div className="flex flex-col min-w-0 flex-1">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <span className={cn(
                                'w-fit max-w-full text-sm font-medium truncate leading-tight',
                                isDisabled && 'text-muted-foreground'
                            )}>
                                {member.name}
                            </span>
                        </TooltipTrigger>
                        <TooltipContent key={member.name} side="top" sideOffset={10} align="center">
                            {member.name}
                        </TooltipContent>
                    </Tooltip>
                    <span className="text-[10px] text-muted-foreground truncate leading-tight">
                        {isChild
                            ? member.channel_name || t('form.childGroupFallback')
                            : member.key_name ? `${member.channel_name} · ${member.key_name}` : member.channel_name}
                    </span>
                </div>

                {/* 子分组成员挂分组徽标，与授权成员的协议展示区分：子分组本身不讲协议，展平后才有。 */}
                {isChild && (
                    <span className="shrink-0 inline-flex items-center gap-1 rounded border border-border/60 px-1 text-[10px] leading-4 text-muted-foreground">
                        {t('form.childBadge')}
                    </span>
                )}

                {/* 智能路由档位：留空=按成员顺序自动对半切分，点一下在 自动/决策/执行 之间轮换。 */}
                {smartMode && onTierChange && (
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <button
                                type="button"
                                onClick={(event) => {
                                    event.stopPropagation();
                                    onTierChange(member.id, nextSmartTier(member.smart_tier));
                                }}
                                className={cn(
                                    'shrink-0 rounded border px-1 text-[10px] leading-4 transition-colors',
                                    (member.smart_tier ?? '') === 'decision' && 'border-violet-500/60 text-violet-600 dark:text-violet-400',
                                    (member.smart_tier ?? '') === 'execution' && 'border-emerald-500/60 text-emerald-600 dark:text-emerald-400',
                                    (member.smart_tier ?? '') === '' && 'border-border/60 text-muted-foreground'
                                )}
                            >
                                {(member.smart_tier ?? '') === 'decision'
                                    ? t('form.tierDecision')
                                    : (member.smart_tier ?? '') === 'execution'
                                        ? t('form.tierExecution')
                                        : t('form.tierAuto')}
                            </button>
                        </TooltipTrigger>
                        <TooltipContent side="top">{t('form.tierHint')}</TooltipContent>
                    </Tooltip>
                )}

                {group && <MemberStatus group={group} itemId={member.item_id} now={now} active={isActive} activeClassName="p-1" />}

                {(!showConfirmDelete || !confirmDelete) && (
                    <motion.button
                        layoutId={`delete-btn-member-${layoutScope ?? 'default'}-${member.id}`}
                        type="button"
                        onClick={(event) => {
                            event.stopPropagation();
                            if (showConfirmDelete) setConfirmDelete(true);
                            else onRemove(member.id);
                        }}
                        className="p-1 rounded hover:bg-destructive/10 hover:text-destructive transition-colors"
                        transition={{ duration: 0.15 }}
                        style={{ pointerEvents: 'auto' }}
                    >
                        <X className="size-3" />
                    </motion.button>
                )}

                <AnimatePresence>
                    {showConfirmDelete && confirmDelete && (
                        <motion.div
                            layoutId={`delete-btn-member-${layoutScope ?? 'default'}-${member.id}`}
                            className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-1.5 rounded-lg"
                            onClick={(event) => event.stopPropagation()}
                            transition={{ type: 'spring', stiffness: 400, damping: 30 }}
                        >
                            <button
                                type="button"
                                onClick={(event) => {
                                    event.stopPropagation();
                                    setConfirmDelete(false);
                                }}
                                className="flex h-6 w-6 items-center justify-center rounded-md bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95"
                            >
                                <X className="h-3 w-3" />
                            </button>
                            <button
                                type="button"
                                onClick={(event) => {
                                    event.stopPropagation();
                                    onRemove(member.id);
                                }}
                                className="flex-1 h-6 flex items-center justify-center gap-1.5 rounded-md bg-destructive-foreground text-destructive text-xs font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98]"
                            >
                                <Trash2 className="h-3 w-3" />
                            </button>
                        </motion.div>
                    )}
                </AnimatePresence>
            </div>
        </div>
    );
}

interface MemberListProps {
    members: SelectedMember[];
    onReorder: (members: SelectedMember[]) => void;
    onRemove: (id: string) => void;
    onActivate?: (itemId: number) => void;
    activeItemId?: number;
    group?: Group; // group 提供当前模式和成员运行状态。
    now?: number; // now 是页面共享的当前 Unix 毫秒时间，仅展示运行态时需要。
    /**
     * When true, auto-scroll the list to bottom when a *new visible* member appears
     * (i.e. a new member id is added). Useful in "editor" flows. Defaults to true.
     */
    autoScrollOnAdd?: boolean;
    onDragStart?: () => void;
    /**
     * Called only when a drop results in a different order (i.e. commit reorder).
     * Useful for persisting the new order.
     */
    onDrop?: (members: SelectedMember[]) => void;
    /**
     * Called whenever a drag ends (including cancel / same-index drop).
     * Useful for lifecycle cleanup (e.g. clearing "isDragging" flags).
     */
    onDragFinish?: () => void;
    removingIds?: Set<string>;
    /**
     * When true, show a confirmation overlay before removing an item.
     * When false, clicking the delete button removes the item immediately.
     * Defaults to true.
     */
    showConfirmDelete?: boolean;
    layoutScope?: string;
    /** 智能路由模式下显示成员档位按钮；其它模式不显示。 */
    smartMode?: boolean;
    /** 档位变更回调：id 为成员键，tier 取 '' | 'decision' | 'execution'。 */
    onTierChange?: (id: string, tier: string) => void;
}

export function MemberList({
    members,
    onReorder,
    onRemove,
    onActivate,
    activeItemId,
    group,
    now = 0,
    autoScrollOnAdd = true,
    onDragStart,
    onDrop,
    onDragFinish,
    removingIds = new Set(),
    showConfirmDelete = true,
    smartMode,
    onTierChange,
    layoutScope: externalLayoutScope,
}: MemberListProps) {
    const internalLayoutScope = useId();
    const layoutScope = externalLayoutScope ?? internalLayoutScope;
    const scrollContainerRef = useRef<HTMLDivElement | null>(null);
    const prevMemberCountRef = useRef<number>(0);
    const hasMountedRef = useRef(false);

    const visibleCount = members.filter((m) => !removingIds.has(m.id)).length;
    const isEmpty = visibleCount === 0;
    const t = useTranslations('group');

    useEffect(() => {
        // Skip the initial mount so we don't auto-scroll on first render / initial data load.
        if (!hasMountedRef.current) {
            hasMountedRef.current = true;
            prevMemberCountRef.current = members.length;
            return;
        }

        if (!autoScrollOnAdd) {
            prevMemberCountRef.current = members.length;
            return;
        }

        const hasNewMember = members.length > prevMemberCountRef.current;

        // Auto-scroll only when member count increases (i.e. added; not reorder / not "unhide").
        if (hasNewMember) {
            // Wait a tick for DOM/placeholder/layout to settle.
            requestAnimationFrame(() => {
                const el = scrollContainerRef.current;
                if (!el) return;
                el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' });
            });
        }

        prevMemberCountRef.current = members.length;
    }, [members.length, autoScrollOnAdd]);

    const handleDragEnd = (result: DropResult) => {
        try {
            const { destination, source } = result;
            if (!destination) return;
            if (destination.index === source.index) return;

            const next = reorderList(members, source.index, destination.index);
            onReorder(next);
            onDrop?.(next);
        } finally {
            // Ensure drag lifecycle always finishes, even when drop is canceled.
            onDragFinish?.();
        }
    };

    return (
        <div className="relative h-full min-h-0">
            <div
                className={cn(
                    'absolute inset-0 flex flex-col items-center justify-center gap-2 text-muted-foreground',
                    'transition-opacity duration-200 ease-out',
                    isEmpty ? 'opacity-100' : 'opacity-0 pointer-events-none'
                )}
            >
                <Layers className="size-10 opacity-40" />
                <span className="text-sm">{t('card.empty')}</span>
            </div>

            <div
                className={cn(
                    'h-full overflow-y-auto transition-opacity duration-200',
                    isEmpty ? 'opacity-0' : 'opacity-100'
                )}
                ref={scrollContainerRef}
            >
                <DragDropContext
                    onDragStart={() => onDragStart?.()}
                    onDragEnd={handleDragEnd}
                >
                    <Droppable
                        droppableId={`members-${layoutScope}`}
                        renderClone={(draggableProvided, snapshot, rubric) => (
                            <MemberItem
                                member={members[rubric.source.index]}
                                onRemove={onRemove}
                                onActivate={onActivate}
                                isActive={members[rubric.source.index].item_id === activeItemId}
                                group={group}
                                now={now}
                                isRemoving={false}
                                showConfirmDelete={showConfirmDelete}
                                layoutScope={layoutScope}
                                smartMode={smartMode}
                                onTierChange={onTierChange}
                                dnd={{
                                    innerRef: draggableProvided.innerRef,
                                    draggableProps: draggableProvided.draggableProps,
                                    dragHandleProps: draggableProvided.dragHandleProps,
                                    isDragging: snapshot.isDragging,
                                }}
                            />
                        )}
                    >
                        {(droppableProvided) => (
                            <div
                                ref={droppableProvided.innerRef}
                                {...droppableProvided.droppableProps}
                                className="p-2 flex flex-col space-y-1.5"
                            >
                                {members.map((member, index) => (
                                    <Draggable
                                        key={member.id}
                                        draggableId={member.id}
                                        index={index}
                                        isDragDisabled={removingIds.has(member.id)}
                                    >
                                        {(draggableProvided, snapshot) => (
                                            <MemberItem
                                                member={member}
                                                onRemove={onRemove}
                                                onActivate={onActivate}
                                                isActive={member.item_id === activeItemId}
                                                group={group}
                                                now={now}
                                                isRemoving={removingIds.has(member.id)}
                                                showConfirmDelete={showConfirmDelete}
                                                layoutScope={layoutScope}
                                    smartMode={smartMode}
                                    onTierChange={onTierChange}
                                                dnd={{
                                                    innerRef: draggableProvided.innerRef,
                                                    draggableProps: draggableProvided.draggableProps,
                                                    dragHandleProps: draggableProvided.dragHandleProps,
                                                    isDragging: snapshot.isDragging,
                                                }}
                                            />
                                        )}
                                    </Draggable>
                                ))}
                                {droppableProvided.placeholder}
                            </div>
                        )}
                    </Droppable>
                </DragDropContext>
            </div>
        </div>
    );
}
