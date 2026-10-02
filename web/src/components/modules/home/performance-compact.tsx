import { Gauge, Timer, Zap } from 'lucide-react';
import { useTranslations } from 'use-intl';
import { useAnalyticsOverview } from '@/api/analytics';
import { AnimatedNumber } from '@/components/common/AnimatedNumber';

// PerformanceCompact 是「性能指标」的一行简版，放在主页概览里。
//
// ## 与完整版的取舍
//
// 完整版（analytics 页的 PerformancePanel）回答的是「忙的时候到底多忙」，
// 因此把「平均」和「忙起来的时候」两个口径分开摆，还带样本口径说明与空态。
// 那套说明在主页是噪音 —— 主页只需要三个当下的读数。
//
// 两个组件读**同一个** useAnalyticsOverview（同 queryKey，React Query 去重），
// 所以主页与分析页各挂一份也只发一次请求，且两边数字必然一致。
export function PerformanceCompact() {
    const t = useTranslations('home.performance');
    const { data, isSuccess } = useAnalyticsOverview();
    const ready = isSuccess && data;

    const items = [
        { key: 'rpm', label: t('rpm'), value: ready ? data.avg_rpm.toFixed(2) : '—', unit: t('rpmUnit'), Icon: Gauge },
        { key: 'tpm', label: t('tpm'), value: ready ? data.avg_tpm.toFixed(2) : '—', unit: t('tpmUnit'), Icon: Timer },
        { key: 'tps', label: t('tps'), value: ready ? data.throughput_tps.toFixed(1) : '—', unit: t('tpsUnit'), Icon: Zap },
    ];

    return (
        <section className="rounded-3xl border border-border bg-card p-5 text-card-foreground">
            <div className="mb-4 flex items-center gap-2">
                <Gauge className="size-4 text-muted-foreground" />
                <h3 className="text-sm font-medium">{t('title')}</h3>
                {/* 口径要摆出来：同一批请求按不同跨度折算出来的数不一样，
                    不写基数时读者会以为这两个数可以横向比。 */}
                {ready && (
                    <span className="ml-auto min-w-0 truncate text-xs text-muted-foreground">
                        {t('basis', { count: data.sample.samples, span: `${Math.round(data.span_seconds)}s`, window: data.sample.window })}
                    </span>
                )}
            </div>

            {ready ? (
                <div className="grid grid-cols-1 gap-4 @xl/home:grid-cols-3">
                    {items.map((item) => (
                        <div key={item.key} className="flex items-center gap-3">
                            <div className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-primary/10 text-primary">
                                <item.Icon className="size-5" />
                            </div>
                            <div className="flex min-w-0 flex-col">
                                <span className="text-xs text-muted-foreground">{item.label}</span>
                                <div className="flex items-baseline gap-1">
                                    <span className="text-xl"><AnimatedNumber value={item.value} /></span>
                                    <span className="text-sm text-muted-foreground">{item.unit}</span>
                                </div>
                            </div>
                        </div>
                    ))}
                </div>
            ) : (
                <p className="flex h-16 items-center justify-center text-sm text-muted-foreground">{t('empty')}</p>
            )}
        </section>
    );
}
