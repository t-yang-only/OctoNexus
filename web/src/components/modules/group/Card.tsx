import { memo, useState, useMemo, useCallback, useEffect, useRef } from 'react';
import { Trash2, X, Pencil } from 'lucide-react';
import { motion, AnimatePresence } from 'motion/react';
import { type Group, type GroupUpdateRequest, useDeleteGroup, useUpdateGroup } from '@/api/group';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { CopyIconButton } from '@/components/common/CopyButton';
import { IconButton } from '@/components/common/IconButton';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import type { SelectedMember } from './ItemList';
import { MemberList } from './ItemList';
import { GroupEditor, type GroupEditorValues } from './Editor';
import {
    MorphingDialog,
    MorphingDialogContainer,
    MorphingDialogContent,
    MorphingDialogDescription,
    MorphingDialogTrigger,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';

interface EditDialogContentProps {
    group: Group;
    displayMembers: SelectedMember[];
    isSubmitting: boolean;
    onSubmit: (values: GroupEditorValues, onDone?: () => void) => void;
}

function EditDialogContent({ group, displayMembers, isSubmitting, onSubmit }: EditDialogContentProps) {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('group');
    return (
        <MorphingDialogDescription className="flex-1 min-h-0 overflow-hidden">
            <GroupEditor
                key={`edit-group-${group.id}`}
                initial={{
                    groupId: group.id,
                    name: group.name,
                    mode: group.mode,
                    relay_config: group.relay_config,
                    members: displayMembers,
                }}
                submitText={t('detail.actions.save')}
                submittingText={t('create.submitting')}
                isSubmitting={isSubmitting}
                onCancel={() => setIsOpen(false)}
                onSubmit={(v) => onSubmit(v, () => setIsOpen(false))}
            />
        </MorphingDialogDescription>
    );
}

export const GroupCard = memo(function GroupCard({ group, now }: { group: Group; now: number }) {
    const t = useTranslations('group');
    const updateGroup = useUpdateGroup();
    const activateItem = useUpdateGroup(); // 与配置提交分开持有: 共用一个实例会让点选成员点亮编辑弹窗的提交态。
    const deleteGroup = useDeleteGroup();

    const [confirmDelete, setConfirmDelete] = useState(false);
    const [members, setMembers] = useState<SelectedMember[]>([]);
    const isDragging = useRef(false);

    // 成员的名称, 所属渠道与可用性由后端随分组给出, 此处只做展示形状的转换。
    // 不可用的成员同样列出: 否则用户看不到它的存在也就无法移除。
    // 子分组成员走 child 引用：名称取目标分组名，图标走默认，徽标与副标题由 ItemList 按 kind 渲染。
    const displayMembers = useMemo((): SelectedMember[] =>
        (group.items || []).map((item) => item.child_group_id
            ? {
                id: `c:${item.child_group_id}`,
                kind: 'child' as const,
                channel_grant_id: 0,
                child_group_id: item.child_group_id,
                name: item.child_group_name || item.model_name,
                enabled: item.available,
                channel_id: item.channel_id,
                channel_name: item.channel_name,
                key_name: '',
                protocols: 0,
                item_id: item.id,
            }
            : {
                id: `g:${item.channel_grant_id}`,
                kind: 'grant' as const,
                channel_grant_id: item.channel_grant_id ?? 0,
                child_group_id: 0,
                name: item.model_name,
                enabled: item.available,
                channel_id: item.channel_id,
                channel_name: item.channel_name,
                key_name: item.key_name,
                protocols: item.protocols,
                item_id: item.id,
            }),
        [group.items]
    );

    useEffect(() => {
        if (!isDragging.current) setMembers([...displayMembers]);
    }, [displayMembers]);

    const onSuccess = useCallback(() => toast.success(t('toast.updated')), [t]);
    const onError = useCallback((error: Error) => toast.error(t('toast.updateFailed'), { description: error.message }), [t]);

    const handleDragStart = useCallback(() => { isDragging.current = true; }, []);
    const handleDragFinish = useCallback(() => { isDragging.current = false; }, []);

    // 成员为整体替换, 拖拽与移除都直接提交当前排列, 优先级由提交顺序决定。
    // 双引用载荷：授权成员给 channel_grant_id，子分组成员给 child_group_id，另一侧填 0。
    const submitMembers = useCallback((next: SelectedMember[]) => {
        updateGroup.mutate(
            {
                id: group.id,
                items: next.map((m) => m.kind === 'child'
                    ? { channel_grant_id: 0, child_group_id: m.child_group_id }
                    : { channel_grant_id: m.channel_grant_id, child_group_id: 0 }),
            },
            { onSuccess, onError },
        );
    }, [group.id, updateGroup, onSuccess, onError]);

    const handleRemoveMember = useCallback((id: string) => {
        submitMembers(members.filter((m) => m.id !== id));
    }, [members, submitMembers]);

    // 点击当前成员即取消选择, 提交 0。
    const handleActivate = useCallback((itemId: number) => {
        if (group.mode !== 'manual' || activateItem.isPending) return;
        activateItem.mutate(
            { id: group.id, active_item_id: itemId === group.runtime.current_item_id ? 0 : itemId },
            { onSuccess, onError },
        );
    }, [activateItem, group.id, group.mode, group.runtime.current_item_id, onError, onSuccess]);

    const handleSubmitEdit = useCallback((values: GroupEditorValues, onDone?: () => void) => {
        const payload: GroupUpdateRequest & { id: number } = { id: group.id };

        if (values.name !== group.name) payload.name = values.name;
        if (values.mode !== group.mode) payload.mode = values.mode;
        if (
            values.relay_config.member_max_attempts !== group.relay_config.member_max_attempts ||
            values.relay_config.member_retry_interval_seconds !== group.relay_config.member_retry_interval_seconds ||
            values.relay_config.member_non_stream_response_timeout_seconds !== group.relay_config.member_non_stream_response_timeout_seconds ||
            values.relay_config.member_stream_first_event_timeout_seconds !== group.relay_config.member_stream_first_event_timeout_seconds ||
            values.relay_config.member_cooldown_seconds !== group.relay_config.member_cooldown_seconds ||
            values.relay_config.member_affinity_seconds !== group.relay_config.member_affinity_seconds
            || values.relay_config.hedge_enabled !== group.relay_config.hedge_enabled
            || values.relay_config.hedge_width !== group.relay_config.hedge_width
            || values.relay_config.hedge_after_ms !== group.relay_config.hedge_after_ms
            || values.relay_config.hedge_peak_in_flight !== group.relay_config.hedge_peak_in_flight
        ) payload.relay_config = values.relay_config;
        // 成员集合与顺序有任一处不同就整体提交; 后端按引用（授权/子分组）匹配, 已有成员保留其主键与统计。
        const refKey = (m: { channel_grant_id: number; child_group_id: number }) =>
            m.child_group_id ? `c:${m.child_group_id}` : `g:${m.channel_grant_id}`;
        const nextRefs = values.members.map((m) => refKey(m));
        const currentRefs = (group.items || []).map((item) => refKey({
            channel_grant_id: item.channel_grant_id ?? 0,
            child_group_id: item.child_group_id ?? 0,
        }));
        if (nextRefs.length !== currentRefs.length || nextRefs.some((id, i) => id !== currentRefs[i])) {
            payload.items = values.members.map((m) => m.kind === 'child'
                ? { channel_grant_id: 0, child_group_id: m.child_group_id }
                : { channel_grant_id: m.channel_grant_id, child_group_id: 0 });
        }

        if (Object.keys(payload).length === 1) {
            onDone?.();
            return;
        }

        updateGroup.mutate(payload, {
            onSuccess: () => {
                onSuccess();
                onDone?.();
            },
            onError,
        });
    }, [group.id, group.items, group.mode, group.name, group.relay_config, onSuccess, onError, updateGroup]);

    return (
        <article className="flex flex-col rounded-3xl border border-border bg-card text-card-foreground p-4">
            <header className="flex items-start justify-between mb-3 relative overflow-visible rounded-xl -mx-1 px-1 -my-1 py-1">
                <div className="relative flex-1 mr-2 min-w-0 group/title">
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <h3 className="text-lg font-bold truncate">{group.name}</h3>
                        </TooltipTrigger>
                        <TooltipContent key={group.name} side="top" sideOffset={10} align="center">
                            {group.name}
                        </TooltipContent>
                    </Tooltip>
                </div>

                <div className="flex items-center gap-1 shrink-0">
                    <MorphingDialog>
                        {/* trigger 自身渲染 motion.div 承担弹窗形变, 故由它出元素, IconButton 只补样式。 */}
                        <IconButton asChild className="size-7">
                            <MorphingDialogTrigger>
                                <Pencil className="size-4" />
                            </MorphingDialogTrigger>
                        </IconButton>

                        <MorphingDialogContainer>
                            <MorphingDialogContent
                                dismissOnClickOutside={false}
                                className="relative w-screen max-w-full md:max-w-4xl bg-card text-card-foreground px-6 py-4 rounded-3xl h-[calc(100vh-2rem)] flex flex-col overflow-hidden"
                            >
                                <EditDialogContent
                                    group={group}
                                    displayMembers={displayMembers}
                                    isSubmitting={updateGroup.isPending}
                                    onSubmit={handleSubmitEdit}
                                />
                            </MorphingDialogContent>
                        </MorphingDialogContainer>
                    </MorphingDialog>

                    {/* CopyIconButton 自带按钮元素与复制成功的图标切换, 故以 asChild 交给它渲染。 */}
                    <IconButton asChild className="size-7">
                        <CopyIconButton
                            text={group.name}
                            copyIconClassName="size-4"
                            checkIconClassName="size-4 text-primary"
                        />
                    </IconButton>
                    {/* asChild 保留 motion.button: 它与确认态共享 layoutId, 换成普通按钮会丢掉形变动画。 */}
                    {!confirmDelete && (
                        <IconButton asChild className="size-7 hover:text-destructive">
                            <motion.button layoutId={`delete-btn-group-${group.id}`} type="button" onClick={() => setConfirmDelete(true)}>
                                <Trash2 className="size-4" />
                            </motion.button>
                        </IconButton>
                    )}
                </div>

                <AnimatePresence>
                    {confirmDelete && (
                        <motion.div layoutId={`delete-btn-group-${group.id}`} className="absolute inset-0 flex items-center justify-center gap-2 bg-destructive p-2 rounded-xl" transition={{ type: 'spring', stiffness: 400, damping: 30 }}>
                            <button type="button" onClick={() => setConfirmDelete(false)} className="flex h-7 w-7 items-center justify-center rounded-lg bg-destructive-foreground/20 text-destructive-foreground transition-all hover:bg-destructive-foreground/30 active:scale-95">
                                <X className="size-4" />
                            </button>
                            <button type="button" onClick={() => deleteGroup.mutate(group.id, { onSuccess: () => toast.success(t('toast.deleted')) })} disabled={deleteGroup.isPending} className="flex-1 h-7 flex items-center justify-center gap-2 rounded-lg bg-destructive-foreground text-destructive text-sm font-semibold transition-all hover:bg-destructive-foreground/90 active:scale-[0.98] disabled:opacity-50 disabled:cursor-not-allowed">
                                <Trash2 className="size-3.5" />
                                {t('detail.actions.confirmDelete')}
                            </button>
                        </motion.div>
                    )}
                </AnimatePresence>
            </header>

            <section className="rounded-xl border border-border/50 bg-muted/30 overflow-hidden relative h-101">
                <MemberList
                    members={members}
                    onReorder={setMembers}
                    onRemove={handleRemoveMember}
                    onActivate={group.mode === 'manual' ? handleActivate : undefined}
                    activeItemId={group.runtime.current_item_id}
                    group={group}
                    now={now}
                    onDragStart={handleDragStart}
                    onDrop={submitMembers}
                    onDragFinish={handleDragFinish}
                    autoScrollOnAdd={false}
                    layoutScope={`card-${group.id}`}
                />
            </section>
        </article >
    );
});
