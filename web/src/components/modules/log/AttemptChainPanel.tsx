import { AlertTriangle, CheckCircle2, Repeat2 } from 'lucide-react';
import { useTranslations } from 'use-intl';
import type { RelayAttemptDetail } from '@/api/log';
import { cn } from '@/lib/utils';

// AttemptChainPanel 展示一次请求"试过哪些成员、各自为什么失败"（T-trace-001）。
//
// 为什么需要它: 卡片上既有 Attempts（次数）也有 TargetChannel（最终渠道），
// 但**中间试过谁、各自为何失败**在界面上无处可见。某个成员每次都失败、每次都要绕开它，
// 从"试了 6 次"和"最后走了 channelC"这两条信息里完全读不出来。
//
// 展示口径（与后端 RelayAttemptDetail 一致）:
//   · 链上只含**已结束**的轮次；正在进行的那轮由卡片其它字段表达, 这里不重复也不补造。
//   · fault_kind 为空表示这一轮没有归因 —— 可能是成功, 也可能是被人工中止。
//     两种情况都不得显示成故障（把用户自己按的停止记成"渠道坏了"是最典型的误导）。
//   · 截断必须以文字说明, 否则读的人会以为这就是全部轮次。

// faultLabel 把后代归因码翻成用户能直接用的词; 未分类返回空串（不猜）。
function useFaultLabel() {
    const t = useTranslations('log.filter');
    return (kind: string | undefined): string => {
        switch (kind) {
            case 'request':
                return t('faultRequest');
            case 'member':
                return t('faultMember');
            case 'transient':
                return t('faultTransient');
            default:
                return '';
        }
    };
}

export function AttemptChainPanel({ chain, truncated }: { chain: RelayAttemptDetail[]; truncated: boolean }) {
    const t = useTranslations('log.card');
    const faultLabel = useFaultLabel();

    // 只有一轮且没出错时不必占地方: 那不是"重试故事", 卡片上已经有足够信息。
    const showPanel = chain.length > 1 || chain.some((item) => item.fault_kind || item.error);
    if (!showPanel) return null;

    return (
        <div className="rounded-lg border border-border bg-muted/20">
            <div className="flex h-8 items-center gap-2 border-b border-border/60 px-3">
                <Repeat2 className="size-3.5 shrink-0 text-fuchsia-500" />
                <span className="text-[11px] font-medium text-card-foreground">{t('attemptChain')}</span>
                <span className="truncate text-[11px] text-muted-foreground/70">{t('attemptChainHint')}</span>
            </div>
            <div className="divide-y divide-border/60">
                {chain.map((item) => {
                    const label = faultLabel(item.fault_kind);
                    // 有归因才是故障轮; 空归因是"已接管/被中止", 用中性样式。
                    const isFault = label !== '';
                    return (
                        <div key={item.round} className="flex items-start gap-2 px-3 py-1.5 text-[11px]">
                            <span className="w-14 shrink-0 tabular-nums text-muted-foreground/70">
                                #{item.round}
                            </span>
                            <span className="shrink-0 font-medium text-foreground">{item.channel || '-'}</span>
                            <span className="shrink-0 tabular-nums text-muted-foreground/70">{item.wait_ms}ms</span>
                            {isFault ? (
                                <span className="flex shrink-0 items-center gap-1 rounded border border-destructive/30 px-1 text-destructive">
                                    <AlertTriangle className="size-3" />
                                    {label}
                                </span>
                            ) : (
                                <span className="flex shrink-0 items-center gap-1 text-emerald-600 dark:text-emerald-400">
                                    <CheckCircle2 className="size-3" />
                                    {t('attemptOk')}
                                </span>
                            )}
                            {item.error && (
                                <span className={cn('min-w-0 flex-1 truncate', isFault ? 'text-destructive/80' : 'text-muted-foreground')} title={item.error}>
                                    {item.error}
                                </span>
                            )}
                        </div>
                    );
                })}
            </div>
            {truncated && (
                <div className="border-t border-border/60 px-3 py-1 text-[11px] text-amber-600 dark:text-amber-400">
                    {t('attemptTruncated')}
                </div>
            )}
        </div>
    );
}
