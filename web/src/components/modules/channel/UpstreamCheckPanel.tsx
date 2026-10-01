import { useState } from 'react';
import { useTranslations } from 'use-intl';

import { apiRequest } from '@/api/client';
import type { UpstreamCheckResult } from '@/api/channel';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

// UpstreamCheckPanel 按需核查各渠道的上游模型清单，并列出与配置的差异。
//
// ## 它回答的是另一个问题
//
// 渠道诊断（DiagnoseBanner）看的是**配置层**：模型有没有、有没有授权、有没有分组。
// 但那都是「我们自己这边的账」，答不了：**客户端列表里那些模型，上游到底有没有？**
//
// 实测（2026-09-23）：pipixia 配了 33 个模型而上游只列出 4 个；
// Forestapi 的探测直接返回「IP 已被封禁」——那是渠道级故障，
// 在此之前没有任何界面会告诉用户这件事。
//
// ## 为什么必须手动触发、且必须带警告
//
// 它会真的打向上游（每个渠道一次请求），所以不能在列表渲染时自动跑。
//
// 更要紧的是**它的结论不能被当成清理依据**：实测发现上游的 /v1/models
// 并不完整 —— 可茶/MiniMax-M3 不在清单里但实际调用返回 200。
// 把它读成「上游没有」会让用户删掉本来能用的配置，而那是不可逆的。
// 所以警告文案直接来自后端的 warning 字段（单一来源，前后端不各写一份）。
export function UpstreamCheckPanel({ channelIds }: { channelIds: number[] }) {
    const t = useTranslations('channel.upstream');
    const [busy, setBusy] = useState(false);
    const [results, setResults] = useState<UpstreamCheckResult[]>([]);

    // 逐个串行探测：并发打十几个上游容易被风控，而且这里本来就是按需操作，
    // 用户等几秒可以接受（比起被上游封 IP 要划算得多）。
    const run = async () => {
        setBusy(true);
        const collected: UpstreamCheckResult[] = [];
        for (const id of channelIds) {
            try {
                const one = await apiRequest<UpstreamCheckResult>(
                    `/api/v1/channel/${id}/upstream-check`,
                );
                collected.push(one);
                // 边跑边显示：串行十几秒，全跑完再渲染会让用户以为卡死了。
                setResults([...collected]);
            } catch {
                // 单个渠道失败不该中断整轮：继续跑其余的。
            }
        }
        setBusy(false);
    };

    const problemChannels = results.filter(
        (r) => !r.probe_ok || (r.missing_upstream?.length ?? 0) > 0,
    );
    const warning = results.find((r) => r.warning)?.warning;

    return (
        <div className="mt-2 space-y-2 border-t border-destructive/20 pt-2">
            <div className="flex flex-wrap items-center gap-2">
                <Button variant="ghost" size="sm" className="h-7 px-2 text-xs"
                    disabled={busy || channelIds.length === 0} onClick={run}>
                    {busy
                        ? t('running', { done: results.length, total: channelIds.length })
                        : t('run')}
                </Button>
                {results.length > 0 && !busy && (
                    <span className="text-xs text-muted-foreground">
                        {t('finished', { total: results.length, problem: problemChannels.length })}
                    </span>
                )}
            </div>

            {/* 警告来自后端，不在这里另写一份 —— 两处措辞会漂移，而这条警告是安全相关的。 */}
            {warning && results.length > 0 && (
                <p className="rounded border border-amber-500/40 bg-amber-500/5 px-2 py-1 text-xs">
                    {warning}
                </p>
            )}

            <div className="max-h-64 space-y-1.5 overflow-y-auto">
                {problemChannels.map((r) => (
                    <div key={r.channel_id} className="text-xs">
                        <span className="font-medium">{r.channel_name}</span>
                        {!r.probe_ok ? (
                            <span className="ml-2 text-destructive">
                                {t('probeFailed', { reason: r.probe_error ?? '' })}
                            </span>
                        ) : (
                            <>
                                <span className="ml-2 text-muted-foreground">
                                    {t('diff', {
                                        listed: r.listed_count ?? 0,
                                        configured: r.configured_count,
                                        upstream: r.upstream_count,
                                    })}
                                </span>
                                <ul className="ml-4 mt-0.5 space-y-0.5 text-muted-foreground">
                                    {(r.missing_upstream ?? []).slice(0, 4).map((name) => (
                                        <li key={name}>
                                            <span className="font-mono">{name}</span>
                                            {' '}
                                            <span>{t('notListed')}</span>
                                        </li>
                                    ))}
                                    {(r.missing_upstream?.length ?? 0) > 4 && (
                                        <li>{t('more', { count: (r.missing_upstream?.length ?? 0) - 4 })}</li>
                                    )}
                                </ul>
                            </>
                        )}
                    </div>
                ))}
            </div>

            {results.length > 0 && problemChannels.length === 0 && !busy && (
                <Badge variant="secondary" className="text-xs">{t('allConsistent')}</Badge>
            )}
        </div>
    );
}
