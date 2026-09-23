import { useState } from 'react';
import { useTranslations } from 'use-intl';

import { verifyChannelModels, type ModelVerifyResponse } from '@/api/channel';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';

// VerifyModelsPanel 按需对渠道做**真实调用**级别的可用性实测。
//
// ## 它回答的是前三层都答不了的问题
//
//   配置层诊断   看的是"我们自己这边的账"（模型/授权/分组齐全与否）
//   上游清单对比 清单本身不完整（实测有模型不在清单里却能调通）
//
// 三层齐全都答不了「上游到底认不认这个名字」。唯一可靠的判据是发一次真实请求。
//
// ## 为什么必须手动触发
//
// 它会**真打上游**（每个模型一次请求），所以做成按钮而非自动加载 ——
// 绝不挂在渲染路径上。并发在后端固定为 3（防风控），用户等十几秒是预期内的。
export function VerifyModelsPanel({ channelIds }: { channelIds: number[] }) {
    const t = useTranslations('channel.verify');
    const [busy, setBusy] = useState(false);
    const [results, setResults] = useState<ModelVerifyResponse[]>([]);
    const [current, setCurrent] = useState('');

    // 逐个渠道串行：每个渠道内部已经并发 3 了，渠道之间再并发会叠加成更大的压力。
    const run = async () => {
        setBusy(true);
        const collected: ModelVerifyResponse[] = [];
        for (const id of channelIds) {
            try {
                setCurrent(String(id));
                const one = await verifyChannelModels(id);
                collected.push(one);
                setResults([...collected]);
            } catch {
                // 单个渠道失败不中断整轮：继续跑其余的。
            }
        }
        setCurrent('');
        setBusy(false);
    };

    const withProblems = results.filter((r) => (r.unusable ?? 0) > 0 || (r.results?.length ?? 0) === 0);
    const totalUsable = results.reduce((sum, r) => sum + (r.usable ?? 0), 0);
    const totalChecked = results.reduce((sum, r) => sum + (r.total ?? 0), 0);
    const truncated = results.some((r) => r.truncated);

    return (
        <div className="mt-2 space-y-2 border-t border-destructive/20 pt-2">
            <div className="flex flex-wrap items-center gap-2">
                <Button variant="ghost" size="sm" className="h-7 px-2 text-xs"
                    disabled={busy || channelIds.length === 0} onClick={run}>
                    {busy
                        ? t('running', { done: results.length, total: channelIds.length, current })
                        : t('run')}
                </Button>
                {!busy && results.length > 0 && (
                    <span className="text-xs text-muted-foreground">
                        {t('finished', { usable: totalUsable, total: totalChecked })}
                    </span>
                )}
            </div>

            {/* 会打上游这件事必须写在按钮旁边，不能只藏在文档里。 */}
            <p className="text-xs text-muted-foreground">{t('cost')}</p>

            {/* 被上限截断时说清楚 —— 否则用户以为"全测过了"。 */}
            {truncated && (
                <p className="rounded border border-amber-500/40 bg-amber-500/5 px-2 py-1 text-xs">
                    {t('truncated')}
                </p>
            )}

            <div className="max-h-64 space-y-1.5 overflow-y-auto">
                {withProblems.map((r) => (
                    <div key={r.channel_id} className="text-xs">
                        <span className="font-medium">{r.channel_name}</span>
                        {(r.results?.length ?? 0) === 0 ? (
                            <span className="ml-2 text-muted-foreground">{r.note ?? t('noTargets')}</span>
                        ) : (
                            <span className="ml-2 text-muted-foreground">
                                {t('channelResult', { usable: r.usable, total: r.total })}
                            </span>
                        )}
                        <ul className="ml-4 mt-0.5 space-y-0.5 text-muted-foreground">
                            {(r.results ?? []).filter((x) => !x.usable).slice(0, 5).map((x) => (
                                <li key={x.model}>
                                    <span className="font-mono">{x.model}</span>
                                    {' — '}
                                    {/* status=0 是"请求没发出去"，与"上游拒绝"完全不同：
                                        前者查网络/代理，后者查模型名/权限。 */}
                                    {x.status === 0 ? t('networkFailure', { error: x.error ?? '' })
                                        : t('upstreamRejected', { status: x.status })}
                                </li>
                            ))}
                            {(r.results ?? []).filter((x) => !x.usable).length > 5 && (
                                <li>{t('more', { count: (r.results ?? []).filter((x) => !x.usable).length - 5 })}</li>
                            )}
                        </ul>
                    </div>
                ))}
            </div>

            {!busy && results.length > 0 && withProblems.length === 0 && (
                <Badge variant="secondary" className="text-xs">{t('allUsable')}</Badge>
            )}
        </div>
    );
}
