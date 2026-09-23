import { useTranslations } from 'use-intl';

import { useGroupLatency } from '@/api/group';

// GroupLatencyPanel 在分组页展示各分组的首字节耗时。
//
// ## 为什么分组维度与渠道维度都要有
//
// 渠道画像回答"53HK 这个渠道多快"，但**用户调用的不是渠道，是分组** ——
// 客户端填的是 `Max-flash`、`High-flash` 这些名字。
//
// 而一个分组内部还要选路（`Max-flash` 生产上有 40 个成员，来自多个渠道），
// 所以"这个分组多快"是分组内部选路的结果，只有按分组统计才看得见。
//
// ## 为什么把成员数摆在显眼位置
//
// 单成员分组没有选择空间 —— 慢也只能用它。不显示成员数的话，
// 用户会以为"这个分组慢，换个成员就好了"，而它根本没得换。
// 这是分组维度独有的上下文（渠道维度不需要）。
export function GroupLatencyPanel() {
    const t = useTranslations('group.latency');
    const { data, isPending, isError } = useGroupLatency();

    if (isPending || isError || !data) return null;
    if (data.window === 0) return null;

    // 只展示有延迟样本的分组：没有样本的排在后面对读者无信息量。
    // 两重过滤：已删除的分组不该出现在「我常用的分组多快」里（用户已经删了它），
    // 没有首字节样本的也滤掉（显示出来只有 0ms，没有信息量）。
    const rows = data.groups
        .filter((g) => !g.deleted && g.first_byte_samples > 0)
        .slice(0, 8);
    if (rows.length === 0) return null;

    return (
        <div className="mx-4 mt-3 rounded-lg border px-3 py-2">
            <div className="flex flex-wrap items-center gap-x-3 text-xs">
                <span className="font-medium">{t('title')}</span>
                <span className="text-muted-foreground">{t('window', { total: data.window })}</span>
                <span className="text-muted-foreground">
                    {t('threshold', { ms: data.slow_threshold_ms })}
                </span>
            </div>

            {/* 这条说明同样必须：首字节含上游自己的处理时间，不是纯网络延迟。 */}
            <p className="mt-1 text-xs text-muted-foreground/80">{t('semantics')}</p>

            <div className="mt-2 overflow-x-auto">
                <table className="w-full text-xs">
                    <thead className="text-muted-foreground">
                        <tr className="text-left">
                            <th className="py-1 font-normal">{t('colGroup')}</th>
                            <th className="py-1 text-right font-normal">{t('colMembers')}</th>
                            <th className="py-1 text-right font-normal">{t('colP50')}</th>
                            <th className="py-1 text-right font-normal">{t('colP90')}</th>
                            <th className="py-1 text-right font-normal">{t('colSlow')}</th>
                        </tr>
                    </thead>
                    <tbody>
                        {rows.map((g) => (
                            <tr key={g.group_id} className="border-t border-border/40">
                                <td className="py-1 font-medium">
                                    {g.name}
                                    {/* 单成员分组标出来 —— 它没有选择空间，慢也只能用它。 */}
                                    {g.member_count <= 1 && (
                                        <span className="ml-1 text-muted-foreground/70">
                                            {t('noChoice')}
                                        </span>
                                    )}
                                </td>
                                <td className="py-1 text-right font-mono text-muted-foreground">
                                    {g.member_count}
                                </td>
                                <td className="py-1 text-right font-mono">{g.first_byte_p50_ms}ms</td>
                                <td className="py-1 text-right font-mono text-muted-foreground">
                                    {g.first_byte_p90_ms}ms
                                </td>
                                <td className="py-1 text-right">
                                    <span className={g.slow_count > 0 ? 'text-destructive' : 'text-muted-foreground'}>
                                        {g.slow_count}
                                    </span>
                                    <span className="ml-1 text-muted-foreground/70">/{g.first_byte_samples}</span>
                                </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>
        </div>
    );
}
