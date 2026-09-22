import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';

import { useChannelDiagnose } from '@/api/channel';
import { Badge } from '@/components/ui/badge';
import { UpstreamCheckPanel } from './UpstreamCheckPanel';
import { Button } from '@/components/ui/button';

// DiagnoseBanner 在渠道页顶部显示可用率，并在有缺口时给出「配了却用不上」的清单。
//
// ## 为什么需要它
//
// 一个模型要能被客户端调用，必须三层齐全（模型 → 凭据授权 → 分组）。
// 任何一层断掉，客户端都只看到 model not found，**而界面上完全看不出是哪一层**：
// 渠道详情里模型列得好好的，用户不知道自己配的东西其实用不了。
//
// 生产实测（2026-09-23）：17 个渠道 / 358 个模型里有 91 个卡在「有模型、无授权」，
// 全局可用率只有 74.6% —— 这个数字此前在任何界面上都看不到。
//
// ## 为什么做成默认收起
//
// 没有缺口时它只占一行；有缺口时才展开成清单。原因是渠道页的主任务是「管理渠道」，
// 诊断是辅助信息 —— 让辅助信息默认铺开，会挤掉用户真正要操作的东西。
export function DiagnoseBanner() {
    const t = useTranslations('channel.diagnose');
    const [open, setOpen] = useState(false);
    // 按需拉取：诊断要遍历全部渠道的模型与授权，不在首屏就发请求。
    const { data, isPending, isError } = useChannelDiagnose();

    const summary = data?.summary;
    const reasons = useMemo(() => {
        const entries = Object.entries(data?.reasons ?? {});
        // 按数量倒序：用户该先修影响面最大的那一类。
        return entries.sort((a, b) => b[1] - a[1]);
    }, [data?.reasons]);

    // 请求中或失败时不占位：诊断是辅助信息，不该在渠道页上闪一下或报错打断主流程。
    if (isPending || isError || !summary) return null;

    const total = summary.total_models ?? 0;
    const broken = summary.broken_models ?? 0;
    const usable = summary.usable_models ?? 0;
    const rate = total > 0 ? Math.round((usable / total) * 100) : 100;

    // 全部可用时不显示横幅 —— 没有要处理的事，不该占用户一行。
    if (broken === 0) return null;

    const gapChannels = (data?.channels ?? []).filter((channel) => (channel.broken?.length ?? 0) > 0);

    return (
        <div className="mx-4 mt-3 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2">
            <div className="flex flex-wrap items-center gap-2">
                <Badge variant="secondary" className="border border-destructive/40 text-destructive">
                    {t('title')}
                </Badge>
                <span className="text-sm">
                    {t('summary', { usable, total, rate })}
                </span>
                <Button
                    variant="ghost"
                    size="sm"
                    className="ml-auto h-7 px-2 text-xs"
                    onClick={() => setOpen((value) => !value)}
                >
                    {open ? t('collapse') : t('expand', { count: broken })}
                </Button>
            </div>

            {open && (
                <div className="mt-2 space-y-2 border-t border-destructive/20 pt-2">
                    {/* 原因归类：让用户知道该优先修哪一类，而不是逐个渠道翻。 */}
                    <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
                        {reasons.map(([reason, count]) => (
                            <span key={reason}>
                                {reason}：{count}
                            </span>
                        ))}
                    </div>

                    <div className="max-h-64 space-y-1.5 overflow-y-auto">
                        {gapChannels.map((channel) => (
                            <div key={channel.channel_id} className="text-xs">
                                <span className="font-medium">{channel.channel_name}</span>
                                <span className="ml-2 text-muted-foreground">
                                    {t('channelGap', {
                                        usable: channel.usable_count,
                                        total: channel.total_count,
                                    })}
                                </span>
                                {/* 只列前几条：全列出来会淹没重点，具体去渠道详情看。 */}
                                <ul className="ml-4 mt-0.5 space-y-0.5 text-muted-foreground">
                                    {(channel.broken ?? []).slice(0, 3).map((model) => (
                                        <li key={model.model_name}>
                                            <span className="font-mono">{model.model_name}</span>
                                            {' — '}
                                            {model.reason}
                                        </li>
                                    ))}
                                    {(channel.broken?.length ?? 0) > 3 && (
                                        <li>{t('more', { count: channel.broken.length - 3 })}</li>
                                    )}
                                </ul>
                            </div>
                        ))}
                    </div>

                    {/* 配置层的缺口列完了，接一段「配置 vs 上游」的核查 —— 
                        它是另一个问题：配置齐全不代表上游提供。手动触发，会真打上游。 */}
                    <UpstreamCheckPanel channelIds={(data?.channels ?? []).map((c) => c.channel_id)} />
                </div>
            )}
        </div>
    );
}
