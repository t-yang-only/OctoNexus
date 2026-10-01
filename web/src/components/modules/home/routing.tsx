import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { useAnalyticsRouting } from '@/api/analytics';
import { formatCount as formatCountRaw } from '@/lib/utils';
import { SampleNote } from '@/components/sample-note';

// formatCount 返回的是 {raw, formatted:{value,unit}}，直接塞进 JSX 会渲染成
// [object Object]，而**模板字符串里的误用连类型检查都抓不到**（拼接接受任意类型）。
// 包一层让它在这个文件里就是一个普通的字符串函数。
const formatCount = (value: number) => formatCountRaw(value).formatted.value;

// T-insight-007 首页「选路与终止画像」区块。
//
// ## 与上方几个区块的分工
//
//   请求窗口分析  多少量、多少钱、谁在吃 token
//   延迟分布      整体有多慢
//   本区块        **谁在做主**（选了谁、按什么机制选）与**怎么停的**（终止原因）
//
// decision 与 stop_reason 落库很久了，日志页看得到、导出有列，但一直没有统计
// 读过它们：想知道「亲和是不是把请求全粘在一个成员上」「失败是预算耗尽还是
// 上游集体拒绝」，只能一行一行翻日志。
//
// ## 本组件最重要的设计：分母必须一直可见
//
// 这个功能的失败模式不是报错，而是**比例看着正常但口径错了**。
// 三个分布的分母各不相同（已记录的选路行 / 智能路由行 / 已记录的终止原因行），
// 如果界面上只给百分比，用户会把它们都读成"占全部请求的比例"，
// 而混入大量未记录行后那些百分比会一致地偏小、且毫无违和感。
//
// 所以：每个区块的标题旁都写明它的占比基数，顶部还把窗口的三态覆盖情况
// 单独摆出来 —— 覆盖率低的时候，用户第一眼就该知道这些比例只代表一部分请求。

const WINDOWS = [200, 500, 2000] as const;

// reason / source 的显示名。**认不出来就显示原值**：
// 后端刻意不枚举白名单（新机制上线时不能被吞进"其他"），前端同样不能 ——
// 一个认不出的字符串是可搜索的线索，而"其他"什么都不是。
const REASON_LABEL: Record<string, string> = {
    manual: 'reasonManual',
    affinity: 'reasonAffinity',
    probe: 'reasonProbe',
    priority: 'reasonPriority',
    ranked: 'reasonRanked',
};

const SOURCE_LABEL: Record<string, string> = {
    system: 'sourceSystem',
    config: 'sourceConfig',
    client: 'sourceClient',
    upstream: 'sourceUpstream',
};

// BarRow 是一条带占比的横向条。分母已由调用方写在区块标题里，这里不重复。
function BarRow({
    label,
    count,
    ratio,
    max,
    detail,
    tone = 'bg-chart-1',
}: {
    label: string;
    count: number;
    ratio: number;
    max: number;
    detail?: string;
    tone?: string;
}) {
    const width = max > 0 ? Math.max((count / max) * 100, 2) : 0;
    return (
        <div className="min-w-0">
            <div className="flex items-baseline justify-between gap-2 text-xs">
                <span className="truncate font-medium">{label}</span>
                <span className="shrink-0 tabular-nums text-muted-foreground">
                    {formatCount(count)} · {ratio.toFixed(1)}%
                </span>
            </div>
            <div className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-muted">
                <div className={`h-full rounded-full ${tone}`} style={{ width: `${width}%` }} />
            </div>
            {detail ? <div className="mt-0.5 text-[11px] text-muted-foreground">{detail}</div> : null}
        </div>
    );
}

function SectionTitle({ title, hint }: { title: string; hint: string }) {
    return (
        <div className="flex flex-wrap items-baseline gap-x-2">
            <span className="text-xs font-semibold">{title}</span>
            <span className="text-[11px] text-muted-foreground">{hint}</span>
        </div>
    );
}

export default function RoutingProfilePanel() {
    const t = useTranslations('home.routeProfile');
    const [window, setWindow] = useState<number>(500);
    const { data } = useAnalyticsRouting(window);

    if (!data || data.window === 0) return null;

    const reasonLabel = (reason: string) => {
        const key = REASON_LABEL[reason];
        return key ? t(key) : reason;
    };
    const sourceLabel = (source: string) => {
        if (!source) return '—';
        const key = SOURCE_LABEL[source];
        return key ? t(key) : source;
    };

    const maxReason = Math.max(...data.reasons.map((item) => item.count), 1);
    const maxSlot = Math.max(...data.slot_distribution.map((item) => item.count), 1);
    const maxStop = Math.max(...data.stops.map((item) => item.count), 1);

    // 覆盖率：没有记录的请求不参与任何机制比例，这件事必须自己说出来。
    const coverage = [
        { key: 'recorded', label: t('recorded'), value: data.decision_recorded },
        { key: 'unrecorded', label: t('unrecorded'), value: data.decision_unrecorded },
        { key: 'malformed', label: t('malformed'), value: data.decision_malformed },
    ];

    return (
        <section className="mt-6 space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-2">
                <div className="min-w-0">
                    <div className="text-sm font-semibold">{t('title')}</div>
                    <div className="text-[11px] text-muted-foreground">{t('subtitle')}</div>
                </div>
                <div className="flex items-center gap-1">
                    {WINDOWS.map((item) => (
                        <button
                            key={item}
                            type="button"
                            onClick={() => setWindow(item)}
                            className={`rounded-md px-2 py-0.5 text-[11px] tabular-nums transition-colors ${
                                item === window
                                    ? 'bg-primary text-primary-foreground'
                                    : 'text-muted-foreground hover:bg-muted'
                            }`}
                        >
                            {item}
                        </button>
                    ))}
                </div>
            </div>

            <div className="rounded-lg border p-3">
                <div className="grid grid-cols-3 gap-3 sm:grid-cols-4">
                    <div className="min-w-0">
                        <div className="text-[11px] text-muted-foreground">{t('windowTotal')}</div>
                        <div className="text-sm font-semibold tabular-nums">{formatCount(data.window)}</div>
                    </div>
                    {coverage.map((item) => (
                        <div key={item.key} className="min-w-0">
                            <div className="text-[11px] text-muted-foreground">{item.label}</div>
                            <div className="text-sm font-semibold tabular-nums">{formatCount(item.value)}</div>
                        </div>
                    ))}
                </div>
                <div className="mt-2 text-[11px] text-muted-foreground">{t('coverageHint')}</div>
                <SampleNote sample={data.sample} className="mt-1 text-[11px] text-muted-foreground" />
            </div>

            <div className="grid gap-4 lg:grid-cols-2">
                <div className="space-y-2 rounded-lg border p-3">
                    <SectionTitle
                        title={t('reasonsTitle')}
                        hint={t('denominatorHint', { count: data.decision_recorded })}
                    />
                    {data.reasons.length === 0 ? (
                        <div className="text-[11px] text-muted-foreground">{t('empty')}</div>
                    ) : (
                        <div className="space-y-2">
                            {data.reasons.map((item) => (
                                <BarRow
                                    key={item.reason}
                                    label={reasonLabel(item.reason)}
                                    count={item.count}
                                    ratio={item.ratio}
                                    max={maxReason}
                                    detail={`${t('outcomeSuccess')} ${formatCount(item.success)} · ${t('outcomeCanceled')} ${formatCount(item.canceled)} · ${t('outcomeFailed')} ${formatCount(item.failed)}`}
                                />
                            ))}
                        </div>
                    )}
                </div>

                <div className="space-y-3">
                    <div className="space-y-2 rounded-lg border p-3">
                        <SectionTitle
                            title={t('modesTitle')}
                            hint={t('denominatorHint', { count: data.decision_recorded })}
                        />
                        {data.modes.length === 0 ? (
                            <div className="text-[11px] text-muted-foreground">{t('empty')}</div>
                        ) : (
                            <div className="space-y-2">
                                {data.modes.map((item) => (
                                    <BarRow
                                        key={item.mode}
                                        label={item.mode}
                                        count={item.count}
                                        ratio={item.ratio}
                                        max={Math.max(...data.modes.map((row) => row.count), 1)}
                                        tone="bg-chart-2"
                                    />
                                ))}
                            </div>
                        )}
                    </div>

                    {/* 档位只对智能路由有意义：非智能路由的请求结构上没有档位，
                        把它们算进分母会让"决策档占多少"永远偏小。 */}
                    {data.smart_count > 0 ? (
                        <div className="space-y-2 rounded-lg border p-3">
                            <SectionTitle
                                title={t('tiersTitle')}
                                hint={t('tiersHint', { count: data.smart_count })}
                            />
                            <div className="space-y-2">
                                {data.tiers.map((item) => (
                                    <BarRow
                                        key={item.tier}
                                        label={item.tier}
                                        count={item.count}
                                        ratio={item.ratio}
                                        max={Math.max(...data.tiers.map((row) => row.count), 1)}
                                        tone="bg-chart-3"
                                    />
                                ))}
                            </div>
                        </div>
                    ) : null}
                </div>
            </div>

            <div className="space-y-2 rounded-lg border p-3">
                <SectionTitle title={t('slotTitle')} hint={t('slotHint')} />
                {data.slot_samples === 0 ? (
                    <div className="text-[11px] text-muted-foreground">{t('empty')}</div>
                ) : (
                    <>
                        <div className="text-[11px] text-muted-foreground">
                            {t('slotAvg')} {data.slot_avg.toFixed(2)} · {t('slotSamples')}{' '}
                            {formatCount(data.slot_samples)}
                        </div>
                        <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
                            {data.slot_distribution.map((item) => (
                                <BarRow
                                    key={item.slot}
                                    label={`#${item.slot}`}
                                    count={item.count}
                                    ratio={item.ratio}
                                    max={maxSlot}
                                    tone={item.slot === 1 ? 'bg-chart-1' : 'bg-muted-foreground/40'}
                                />
                            ))}
                        </div>
                    </>
                )}
            </div>

            <div className="space-y-2 rounded-lg border p-3">
                <SectionTitle title={t('stopsTitle')} hint={t('stopsHint')} />
                {data.stops.length === 0 ? (
                    <div className="text-[11px] text-muted-foreground">{t('empty')}</div>
                ) : (
                    <div className="space-y-2">
                        {data.stops.map((item) => (
                            <BarRow
                                key={`${item.reason}\u0000${item.source}`}
                                label={item.reason}
                                count={item.count}
                                ratio={item.ratio}
                                max={maxStop}
                                tone={item.failed > 0 ? 'bg-rose-500/70' : 'bg-chart-1'}
                                detail={`${t('stopSource')} ${sourceLabel(item.source)} · ${t('outcomeSuccess')} ${formatCount(item.success)} · ${t('outcomeCanceled')} ${formatCount(item.canceled)} · ${t('outcomeFailed')} ${formatCount(item.failed)}`}
                            />
                        ))}
                    </div>
                )}
                <div className="text-[11px] text-muted-foreground">
                    {t('stopDenominatorHint', {
                        recorded: data.stop_recorded,
                        unrecorded: data.stop_unrecorded,
                    })}
                </div>
            </div>
        </section>
    );
}
