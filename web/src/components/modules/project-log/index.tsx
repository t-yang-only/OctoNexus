import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Loader2, ScrollText } from 'lucide-react';
import { SampleNote } from '@/components/sample-note';
import { useAttemptChainStats, type AttemptGroupBy } from '@/api/log';

// 分类维度。与后端 op.AttemptGroupBy 的白名单一一对应。
const GROUP_BY_OPTIONS: { value: AttemptGroupBy; labelKey: string }[] = [
    { value: 'channel', labelKey: 'byChannel' },
    { value: 'model', labelKey: 'byModel' },
    { value: 'apikey', labelKey: 'byApiKey' },
];

const WINDOW = 500;

// ProjectLog 是新的「日志」页：单独看项目侧的诊断日志，可按维度分类聚合。
//
// # 为什么要有这个页
//
// 侧边栏原来的那一项是**使用记录**（客户端视角：我发起了哪些请求、花了多少钱），
// 到 2026-09-30 为止它底部还挂着尝试链聚合面板 —— 那是**项目侧**的诊断信息
// （谁在被反复试错），混在一起有两个后果：
//   ① 想看"我又调了什么"的人被迫先划过一堆渠道故障；
//   ② 想看诊断的人得滚到页面底部，且面板在无异常时返回 null，等于常常找不到。
//
// 于是拆开：使用记录只留调用记录，诊断信息独立成页并可切换维度。
//
// # 三个维度的语义差别必须让读者看见
//
//   channel  逐次尝试 —— 一次请求换过成员时，它的几次尝试分属不同渠道
//   model    请求级（客户端请求的模型名 = 分组名）—— 哪个分组在被反复试错
//   apikey   请求级（调用方）—— 谁在反复试错
//
// 所以同一批数据在三个维度下"共尝试 N 次"是同一个数，但分组数与每组的次数不同。
// 这句说明必须常驻界面，否则用户会以为某个维度算错了。
export function ProjectLog() {
    const t = useTranslations('projectLog');
    const [groupBy, setGroupBy] = useState<AttemptGroupBy>('channel');
    const { data, isPending, isError } = useAttemptChainStats(WINDOW, true, groupBy);

    // 只列有失败轮的分组：全成功的分组在这个视图里没有信息量
    // （"从没失败过"在别处已经能看出来）。
    const problem = useMemo(
        () => (data?.groups ?? []).filter((g) => g.failures > 0),
        [data]
    );

    if (isPending) {
        return (
            <div className="flex h-full items-center justify-center">
                <Loader2 className="size-6 animate-spin text-muted-foreground" />
            </div>
        );
    }

    if (isError || !data) {
        return (
            <div className="flex h-full flex-col items-center justify-center gap-2 text-muted-foreground">
                <ScrollText className="size-8" />
                <span className="text-sm">{t('error')}</span>
            </div>
        );
    }

    return (
        <div className="flex h-full min-h-0 flex-col gap-4 overflow-y-auto px-1 pb-6">
            {/* 维度切换：放在最上面 —— 它决定下面所有数字按什么归类。 */}
            <div className="flex flex-wrap items-center gap-2">
                <span className="text-xs text-muted-foreground">{t('groupBy')}</span>
                {GROUP_BY_OPTIONS.map((opt) => (
                    <button
                        key={opt.value}
                        type="button"
                        onClick={() => setGroupBy(opt.value)}
                        className={`rounded-md border px-2.5 py-1 text-xs transition-colors ${
                            groupBy === opt.value
                                ? 'border-primary bg-primary text-primary-foreground'
                                : 'hover:bg-muted'
                        }`}
                    >
                        {t(opt.labelKey)}
                    </button>
                ))}
                <span className="ml-auto text-xs text-muted-foreground">
                    {t('window', { count: WINDOW })}
                </span>
            </div>

            {/* 语义差别常驻：切维度后数字的归法变了，不说明会被当成 bug。 */}
            <p className="text-xs text-muted-foreground">{t('semantics.' + groupBy)}</p>

            {data.scanned === 0 ? (
                <div className="flex flex-1 flex-col items-center justify-center gap-2 text-muted-foreground">
                    <ScrollText className="size-8" />
                    <span className="text-sm">{t('empty')}</span>
                </div>
            ) : (
                <>
                    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-lg border p-3 text-xs">
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

                    {/* 这句话要直说：affected 与 fault-stats 的失败数对不上时，
                        读的人会以为有 bug。 */}
                    {data.affected_requests > 0 && (
                        <p className="text-xs text-muted-foreground">{t('explain')}</p>
                    )}

                    {data.truncated > 0 && (
                        <p className="text-xs text-muted-foreground">
                            {t('truncated', { count: data.truncated })}
                        </p>
                    )}

                    {/* 样本账：这些聚合值已经把测试请求剔出去了。 */}
                    <SampleNote sample={data.sample} />

                    {problem.length > 0 && (
                        <ul className="space-y-2">
                            {problem.map((g) => (
                                <li key={g.name} className="space-y-1 rounded-lg border p-3">
                                    <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
                                        <span className="font-medium">{g.name}</span>
                                        <span className="text-muted-foreground">
                                            {t('attempts', { count: g.attempts })}
                                        </span>
                                        <span>
                                            {t('failures')}
                                            {' '}
                                            <span className="font-medium">{g.failures}</span>
                                        </span>
                                        {g.member_fault > 0 && (
                                            <span className="text-muted-foreground">
                                                {t('memberFault', { count: g.member_fault })}
                                            </span>
                                        )}
                                        {g.transient_fault > 0 && (
                                            <span className="text-muted-foreground">
                                                {t('transientFault', { count: g.transient_fault })}
                                            </span>
                                        )}
                                        {g.request_fault > 0 && (
                                            <span className="text-muted-foreground">
                                                {t('requestFault', { count: g.request_fault })}
                                            </span>
                                        )}
                                        {g.unclassified_fault > 0 && (
                                            <span className="text-muted-foreground">
                                                {t('unclassifiedFault', { count: g.unclassified_fault })}
                                            </span>
                                        )}
                                    </div>
                                    {g.last_error && (
                                        <p className="break-all text-xs text-muted-foreground/80">
                                            {g.last_error}
                                        </p>
                                    )}
                                </li>
                            ))}
                        </ul>
                    )}

                    {problem.length === 0 && (
                        <p className="py-8 text-center text-sm text-muted-foreground">
                            {t('allGood')}
                        </p>
                    )}
                </>
            )}
        </div>
    );
}

// ProjectLogActions 是日志页的顶栏动作（与其它页的 *Actions 同构）。
export function ProjectLogActions() {
    return null;
}
