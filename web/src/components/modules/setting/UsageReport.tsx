import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Eye, Send } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { usePreviewUsageReport, useSendUsageReport, useUsageReportHistory, type UsageReportPeriod } from '@/api/usage-report';
import { toast } from 'sonner';

const PERIODS: UsageReportPeriod[] = ['daily', 'weekly', 'monthly'];

/**
 * 用量报告面板。
 *
 * 解决的问题：接了几十家上游之后，"这个月花了多少、哪家最贵、哪家在掉成功率"
 * 只能靠人翻面板。开启后按周期把摘要推到已配置的通知渠道（Server酱/飞书/邮件…）。
 *
 * 面板把「先看再开」放在最前面：报告会推到手机上，用户该先知道会收到什么再决定开不开，
 * 所以「预览」和「立即发送」都不要求先启用开关。
 */
export function SettingUsageReport() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();
    const preview = usePreviewUsageReport();
    const send = useSendUsageReport();
    const { data: history } = useUsageReportHistory();

    const [enabled, setEnabled] = useState(false);
    const [period, setPeriod] = useState<UsageReportPeriod>('daily');
    const [hour, setHour] = useState('9');
    const [previewText, setPreviewText] = useState('');
    const initial = useRef({ enabled: false, period: 'daily' as UsageReportPeriod, hour: '9' });

    useEffect(() => {
        if (!settings) return;
        const get = (key: string) => settings.find((s) => s.key === key)?.value ?? '';
        const rawEnabled = get(SettingKey.UsageReportEnabled) === 'true';
        const rawPeriod = (get(SettingKey.UsageReportPeriod) || 'daily') as UsageReportPeriod;
        const rawHour = get(SettingKey.UsageReportHour) || '9';
        queueMicrotask(() => {
            setEnabled(rawEnabled);
            setPeriod(rawPeriod);
            setHour(rawHour);
        });
        initial.current = { enabled: rawEnabled, period: rawPeriod, hour: rawHour };
    }, [settings]);

    const save = (key: string, value: string, current: string) => {
        if (value === current) return;
        setSetting.mutate(
            { key, value },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    if (key === SettingKey.UsageReportEnabled) initial.current.enabled = value === 'true';
                    if (key === SettingKey.UsageReportPeriod) initial.current.period = value as UsageReportPeriod;
                    if (key === SettingKey.UsageReportHour) initial.current.hour = value;
                },
                onError: (error) => toast.error(String(error)),
            },
        );
    };

    const runPreview = () => {
        preview.mutate(period, {
            onSuccess: (report) => setPreviewText(report.text),
            onError: (error) => toast.error(String(error)),
        });
    };

    const runSend = () => {
        send.mutate(period, {
            onSuccess: () => toast.success(t('usageReport.sent')),
            onError: (error) => toast.error(String(error)),
        });
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Send className="size-4" />
                {t('usageReport.title')}
            </h2>
            <p className="text-sm text-muted-foreground leading-relaxed">{t('usageReport.description')}</p>

            <div className="rounded-2xl border border-border/50 bg-muted/20 p-3 space-y-3">
                <div className="flex flex-wrap items-center gap-4">
                    <div className="flex items-center gap-2">
                        <Switch
                            checked={enabled}
                            onCheckedChange={(checked) => {
                                setEnabled(checked);
                                save(SettingKey.UsageReportEnabled, String(checked), String(initial.current.enabled));
                            }}
                        />
                        <span className="text-sm">{t('usageReport.enabled')}</span>
                    </div>
                    <div className="flex items-center gap-2">
                        <span className="text-sm text-muted-foreground">{t('usageReport.period')}</span>
                        <Select
                            value={period}
                            onValueChange={(value) => {
                                const next = value as UsageReportPeriod;
                                setPeriod(next);
                                save(SettingKey.UsageReportPeriod, next, initial.current.period);
                            }}
                        >
                            <SelectTrigger className="w-28">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {PERIODS.map((item) => (
                                    <SelectItem key={item} value={item}>
                                        {t(`usageReport.periods.${item}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </div>
                    <div className="flex items-center gap-2">
                        <span className="text-sm text-muted-foreground">{t('usageReport.hour')}</span>
                        <Input
                            className="w-20"
                            type="number"
                            min={0}
                            max={23}
                            value={hour}
                            onChange={(e) => setHour(e.target.value)}
                            onBlur={() => save(SettingKey.UsageReportHour, hour, initial.current.hour)}
                        />
                    </div>
                </div>
                <p className="text-xs text-muted-foreground">{t('usageReport.hint')}</p>
            </div>

            <div className="flex flex-wrap gap-2">
                <Button type="button" variant="secondary" onClick={runPreview} disabled={preview.isPending}>
                    <Eye className="size-3.5" />
                    {t('usageReport.preview')}
                </Button>
                <Button type="button" onClick={runSend} disabled={send.isPending}>
                    <Send className="size-3.5" />
                    {t('usageReport.sendNow')}
                </Button>
            </div>

            {previewText && (
                <pre className="max-h-72 overflow-auto whitespace-pre-wrap rounded-2xl border border-border/50 bg-muted/20 p-3 text-xs leading-relaxed">
                    {previewText}
                </pre>
            )}

            {history && history.length > 0 && (
                <div className="space-y-1">
                    <div className="text-sm font-medium">{t('usageReport.history')}</div>
                    {history.slice(0, 5).map((item) => (
                        <div key={item.period_key} className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                            <span className="font-mono">{item.period_key}</span>
                            <span>{item.summary}</span>
                            <span>
                                {t('usageReport.delivered')} {item.delivered}/{item.delivered + item.failed}
                            </span>
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}
