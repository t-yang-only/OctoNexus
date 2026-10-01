import { useTranslations } from 'use-intl';

import { useChannelLatency } from '@/api/channel';

// LatencyPanel 展示各渠道的首字节耗时画像。
//
// ## 为什么需要它
//
// 实测发现 17 个渠道的上游 TLS 握手从 1ms 到 1177ms（差三个数量级）。
// 慢的上游会拖慢**每一次**转发 —— 用户在客户端只感觉"这个模型怎么这么慢"，
// 看不出是渠道的问题，更不知道该换哪个用。
//
// ## 为什么显示中位数与 p90 两个数
//
// 中位数是"典型体验"（用户日常感受到的那个），p90 看最坏情况的频率。
// 只看中位数会漏掉偶尔卡顿的渠道，只看 p90 会把偶发抖动误判成常态。
// 再加一个慢请求计数，用户就能分辨"慢是常态"还是"今天抖了一下"。
export function LatencyPanel() {
    const t = useTranslations('channel.latency');
    const { data, isPending, isError } = useChannelLatency();

    if (isPending || isError || !data) return null;
    // 没有样本时不显示：新装的实例或刚清理过日志时，这一行没有信息量。
    if (data.window === 0) return null;

    // 样本太少的渠道数字不可信，单独标出而不是藏起来 ——
    // 藏起来会让用户疑惑"我那个渠道怎么没出现"。
    const rows = data.channels.slice(0, 8);
    const withData = rows.filter((r) => r.samples > 0);

    return (
        <div className="mt-2 space-y-2 border-t border-destructive/20 pt-2">
            <div className="flex flex-wrap items-center gap-x-3 text-xs">
                <span className="text-muted-foreground">{t('window', { total: data.window })}</span>
                <span className="text-muted-foreground">
                    {t('threshold', { ms: data.slow_threshold_ms })}
                </span>
            </div>

            {/* 这条说明是必须的：首字节里含着上游自己的处理时间，
                不加说明用户会以为慢是网络问题（实测 TLS 握手只有 1ms，
                而首字节中位数可达 4515ms —— 差的是上游排队）。 */}
            <p className="text-xs text-muted-foreground/80">{t('semantics')}</p>

            {withData.length === 0 ? (
                <p className="text-xs text-muted-foreground">{t('noSamples')}</p>
            ) : (
                <div className="overflow-x-auto">
                    <table className="w-full text-xs">
                        <thead className="text-muted-foreground">
                            <tr className="text-left">
                                <th className="py-1 font-normal">{t('colChannel')}</th>
                                <th className="py-1 text-right font-normal">{t('colP50')}</th>
                                <th className="py-1 text-right font-normal">{t('colP90')}</th>
                                <th className="py-1 text-right font-normal">{t('colDuration')}</th>
                                <th className="py-1 text-right font-normal">{t('colSlow')}</th>
                            </tr>
                        </thead>
                        <tbody>
                            {withData.map((r) => (
                                <tr key={r.channel} className="border-t border-border/40">
                                    <td className="py-1 font-medium">{r.channel}</td>
                                    <td className="py-1 text-right font-mono">
                                        {/* 没有首字节样本时显示「—」而不是 0ms —— 
                                            0ms 会被读成「快得不可思议」，实际是没数据。 */}
                                        {r.first_byte_samples > 0 ? r.first_byte_p50_ms + 'ms' : '—'}
                                    </td>
                                    <td className="py-1 text-right font-mono text-muted-foreground">
                                        {r.first_byte_samples > 0 ? r.first_byte_p90_ms + 'ms' : '—'}
                                    </td>
                                    <td className="py-1 text-right font-mono text-muted-foreground">
                                        {r.duration_p50_ms}ms
                                    </td>
                                    <td className="py-1 text-right">
                                        <span className={r.slow_count > 0 ? 'text-destructive' : 'text-muted-foreground'}>
                                            {r.slow_count}
                                        </span>
                                        {/* 样本数是可信度的依据，必须能看到。 */}
                                        <span className="ml-1 text-muted-foreground/70">
                                            /{r.samples}
                                        </span>
                                    </td>
                                </tr>
                            ))}
                        </tbody>
                    </table>
                </div>
            )}

            {/* 样本少的提醒放在表格下面：它是"怎么读这张表"的说明，不是主要信息。 */}
            {withData.some((r) => r.samples < 5) && (
                <p className="text-xs text-muted-foreground/80">{t('lowSampleHint')}</p>
            )}
        </div>
    );
}
