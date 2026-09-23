import { useTranslations } from 'use-intl';

import { useRelayFaultStats } from '@/api/log';

// FaultStatsPanel 展示真实通过率：把「渠道故障」与「请求问题」分开。
//
// ## 为什么需要两个数字
//
// 一个通过率回答不了两个问题。实测：senseaudio 显示 31.6%，
// 看起来像渠道坏了，实际上那 25 次失败全是「用 chat 接口调 TTS/图像模型」
// 造成的**请求非法** —— 跟渠道没有任何关系，用户会去修一个没坏的东西。
//
//   success_rate  成功 / 全部（含请求非法与取消）—— 用户视角的体验
//   channel_rate  成功 / (成功+成员故障+可恢复)  —— **渠道健康度**
//
// 所以这里两个都给，并且在它们差距明显时把原因标出来。
//
// ## 为什么把「未分类」也显示
//
// 升级前的存量行没有归因分类。它们单独计数而不是并进某一类 ——
// **不知道的不能猜**：猜成渠道故障会把历史账算到渠道头上。
// 但用户有权知道有多少条是"没归类的"，否则他会疑惑两个数为什么对不上。
export function FaultStatsPanel() {
    const t = useTranslations('channel.faultStats');
    const { data, isPending, isError } = useRelayFaultStats();

    // 请求中或失败时不占位：诊断是辅助信息，不该在渠道页上闪一下或报错打断主流程。
    if (isPending || isError || !data) return null;

    const total = data.window ?? 0;
    if (total === 0) return null;

    const denominator = data.success + data.member_fault + data.transient_fault;
    const experienceRate = total > 0 ? Math.round((data.success / total) * 100) : 0;
    const channelRate = denominator > 0 ? Math.round((data.success / denominator) * 100) : 0;

    // 只列两个率差距明显的渠道：一致的渠道没有信息量，列出来只是噪音。
    const notable = (data.channels ?? [])
        .filter((c) => c.success_rate + 15 < c.channel_rate)
        .slice(0, 4);

    return (
        <div className="mt-2 space-y-2 border-t border-destructive/20 pt-2">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
                <span className="text-muted-foreground">{t('window', { total })}</span>
                <span>
                    {t('experience')}
                    {' '}
                    <span className="font-medium">{experienceRate}%</span>
                </span>
                <span>
                    {t('channel')}
                    {' '}
                    <span className="font-medium">{channelRate}%</span>
                </span>
            </div>

            {/* 未分类：只在确实有存量数据时提示，避免升级后一上来就显示一行噪音。 */}
            {data.unclassified > 0 && (
                <p className="text-xs text-muted-foreground">
                    {t('unclassified', { count: data.unclassified })}
                </p>
            )}

            {notable.length > 0 && (
                <div className="space-y-1">
                    <p className="text-xs text-muted-foreground">{t('gapHint')}</p>
                    <ul className="space-y-0.5 text-xs">
                        {notable.map((c) => (
                            <li key={c.channel}>
                                <span className="font-medium">{c.channel}</span>
                                <span className="ml-2 text-muted-foreground">
                                    {t('gap', {
                                        experience: Math.round(c.success_rate),
                                        channel: Math.round(c.channel_rate),
                                        requestFault: c.request_fault,
                                    })}
                                </span>
                            </li>
                        ))}
                    </ul>
                </div>
            )}
        </div>
    );
}
