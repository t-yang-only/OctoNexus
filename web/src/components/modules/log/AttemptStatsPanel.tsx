import { useTranslations } from 'use-intl';

import { useAttemptChainStats } from '@/api/log';
import { SampleNote } from '@/components/sample-note';

// AttemptStatsPanel 展示「谁在被反复试错」——尝试链的聚合视图（T-trace-002）。
//
// ## 补的是哪个盲区
//
// 现有的统计都只看**最终结果**：
//
//   fault_stats      只看最终失败的请求，按最终渠道归因
//   渠道/分组画像     只看最终走到哪个渠道、耗时多少
//
// 于是有一整类事实在任何视图里都不存在：一次**成功**的请求里，
// 成员 A 失败、成员 B 接手成功了 —— A 的那次失败不进 fault_stats
// （那条日志 status=success），也不进任何画像（画像记的是 B）。
// 用户看到"成功率 100%"，而实际上每次请求都在某个成员上白等一次。
//
// 生产实测里这不是理论问题：`Max-flash` 有 40 个成员，正常请求走 1 轮，
// 而某个成员凭据失效后它会**每次都被试一遍再跳过** —— 用户只能看到延迟从
// 1.5s 变成 5.5s，看不出是"有个成员在每次都被重试"。
//
// ## 为什么 affected_requests 要单独讲
//
// 它与 fault_stats 的失败数**刻意不同**：这里的请求最终可能是成功的。
// 两者之差就是"试错但最终成功"的量 —— 正是这个视图存在的理由，
// 所以面板上要把这句话直说，否则读的人会以为两个数对不上是 bug。
export function AttemptStatsPanel() {
    const t = useTranslations('log.attemptStats');
    const { data, isPending, isError } = useAttemptChainStats();

    // 请求中或失败时不占位：诊断是辅助信息，不该打断日志页主流程。
    if (isPending || isError || !data) return null;

    // 没有任何带尝试链的日志（升级后还没发生过请求）：整块不显示。
    if (data.scanned === 0) return null;

    // 只列**有失败轮**的渠道：全成功的渠道在这个视图里没有信息量
    // （"从没失败过"在别处已经能看出来）。
    const problem = (data.channels ?? []).filter((c) => c.failures > 0).slice(0, 5);

    // 多轮与失败请求都是 0 —— 说明窗口内一切正常，不必占版面。
    if (problem.length === 0 && data.multi_round_requests === 0) return null;

    return (
        <div className="space-y-2 border-t border-destructive/20 pt-3">
            <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs">
                <span className="text-muted-foreground">{t('title')}</span>
                <span>{t('scanned', { count: data.scanned })}</span>
                <span>
                    {t('multiRound')}
                    {' '}
                    <span className="font-medium">{data.multi_round_requests}</span>
                </span>
                <span>
                    {t('affected')}
                    {' '}
                    <span className="font-medium">{data.affected_requests}</span>
                </span>
            </div>

            {/* 这句话要直说：两个数对不上时读的人会以为有 bug。 */}
            {data.affected_requests > 0 && (
                <p className="text-xs text-muted-foreground">{t('explain')}</p>
            )}

            {/* 截断会让聚合值偏低，必须让读者知道。 */}
            {data.truncated > 0 && (
                <p className="text-xs text-muted-foreground">
                    {t('truncated', { count: data.truncated })}
                </p>
            )}

            {/* 样本账：这些聚合值已经把测试请求剔出去了（T-trace-006）。 */}
            <SampleNote sample={data.sample} />

            {problem.length > 0 && (
                <ul className="space-y-1 text-xs">
                    {problem.map((c) => (
                        <li key={c.channel} className="space-y-0.5">
                            <div className="flex flex-wrap items-center gap-x-3 gap-y-0.5">
                                <span className="font-medium">{c.channel}</span>
                                <span className="text-muted-foreground">
                                    {t('attempts', { count: c.attempts })}
                                </span>
                                <span>
                                    {t('failures')}
                                    {' '}
                                    <span className="font-medium">{c.failures}</span>
                                </span>
                                {/* 只列非零的归因档：全列出来会让一行变得很长而没有信息量。 */}
                                {c.member_fault > 0 && (
                                    <span className="text-muted-foreground">
                                        {t('memberFault', { count: c.member_fault })}
                                    </span>
                                )}
                                {c.transient_fault > 0 && (
                                    <span className="text-muted-foreground">
                                        {t('transientFault', { count: c.transient_fault })}
                                    </span>
                                )}
                                {c.request_fault > 0 && (
                                    <span className="text-muted-foreground">
                                        {t('requestFault', { count: c.request_fault })}
                                    </span>
                                )}
                            </div>
                            {c.last_error && (
                                <p className="break-all text-muted-foreground/80">
                                    {c.last_error}
                                </p>
                            )}
                        </li>
                    ))}
                </ul>
            )}
        </div>
    );
}
