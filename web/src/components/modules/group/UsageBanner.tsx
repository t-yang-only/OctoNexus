import { useState } from 'react';
import { useTranslations } from 'use-intl';

import { useGroupUsage } from '@/api/group';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

// GroupUsageBanner 在分组页顶部显示「建了多少、用了多少」。
//
// ## 为什么这个数字值得占一行
//
// 生产实测：418 个分组里只有 41 个被调用过，而它们**全部**是客户端可直接调用的
// 模型名（没有分组嵌套）—— 也就是说用户在 AI 客户端的模型列表里会看到 418 个条目。
//
// 这个事实此前在任何界面上都看不到：分组页只列条目，不告诉你哪些在用。
//
// ## 刻意不给「建议删除」
//
// 未被调用**不等于**该删 —— 备用分组、给子分组复用的分组、尚未启用的新分组
// 都可能合法地没有流量。本项目在 T-usability-006 上刚踩过同一个坑
// （把「上游清单未列出」当成「不可用」，差点让用户删掉有效配置）。
//
// 所以这里只陈述事实，把判断留给掌握上下文的人。界面上不出现「清理」「建议删除」
// 这类措辞 —— 用户看到数字自己会决定。
export function GroupUsageBanner() {
    const t = useTranslations('group.usage');
    const [open, setOpen] = useState(false);
    // 按需拉取：它要扫日志做聚合，不在首屏就发请求。
    const { data, isPending, isError } = useGroupUsage();

    // 请求中或失败时不占位：这是辅助信息，不该在分组页上闪一下或报错打断主流程。
    if (isPending || isError || !data) return null;
    // 一个分组都没有时不显示 —— 新装的实例不需要看到这行。
    if (data.total === 0) return null;

    const usedRate = data.total > 0 ? Math.round((data.used / data.total) * 100) : 0;
    // 只有确实有"没用过"的分组时才值得展开；全都用过就没必要点。
    const hasUnused = data.unused > 0;
    // 调用最多的几个：让用户一眼看到主力是哪几个。
    const top = data.groups.slice(0, 5);

    return (
        <div className="mx-4 mt-3 rounded-lg border px-3 py-2">
            <div className="flex flex-wrap items-center gap-2">
                <Badge variant="secondary" className="text-xs">
                    {t('title')}
                </Badge>
                <span className="text-sm">
                    {t('summary', { used: data.used, total: data.total, rate: usedRate })}
                </span>
                <span className="text-xs text-muted-foreground">
                    {t('breakdown', { auto: data.auto, manual: data.manual })}
                </span>
                {hasUnused && (
                    <Button
                        variant="ghost"
                        size="sm"
                        className="ml-auto h-7 px-2 text-xs"
                        onClick={() => setOpen((v) => !v)}
                    >
                        {open ? t('collapse') : t('expand')}
                    </Button>
                )}
            </div>

            {open && (
                <div className="mt-2 space-y-2 border-t pt-2">
                    {/* 这条说明是刻意的：避免用户把「没用过」直接读成「该删」。 */}
                    <p className="text-xs text-muted-foreground">{t('caveat')}</p>

                    {top.length > 0 && (
                        <div className="space-y-0.5">
                            <p className="text-xs font-medium">{t('topTitle')}</p>
                            <ul className="space-y-0.5 text-xs">
                                {top.map((g) => (
                                    <li key={g.group_id} className="flex flex-wrap gap-x-2">
                                        <span className="font-mono">{g.name}</span>
                                        <span className="text-muted-foreground">
                                            {t('calls', { count: g.call_count })}
                                        </span>
                                        <span className="text-muted-foreground">
                                            {t('members', { count: g.item_count })}
                                        </span>
                                        <span className="text-muted-foreground">
                                            {g.is_auto ? t('auto') : t('manual')}
                                        </span>
                                    </li>
                                ))}
                            </ul>
                        </div>
                    )}

                    {hasUnused && (
                        <p className="text-xs text-muted-foreground">
                            {t('unused', { count: data.unused, window: data.window })}
                        </p>
                    )}
                </div>
            )}
        </div>
    );
}
