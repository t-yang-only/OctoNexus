import { useEffect, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { BellRing, Send } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { useSettingList, useSetSetting, useTestNotifyChannels, SettingKey, type Setting, type NotifyDeliveryResult } from '@/api/setting';
import { toast } from 'sonner';

// 通知渠道的固定顺序：界面按此顺序渲染，写回 alert_channels 时也按此顺序拼接，避免"勾选顺序"造成无意义 diff。
const NOTIFY_KINDS = ['webhook', 'feishu', 'dingtalk', 'wecom', 'smtp', 'serverchan'] as const;

// 通知相关的设置键：界面里统一样式渲染，保存后统一回写初始值。
const NOTIFY_SETTING_KEYS = {
    channels: SettingKey.AlertChannels,
    webhook: SettingKey.AlertWebhookURL,
    feishu: SettingKey.AlertFeishuWebhook,
    dingtalk: SettingKey.AlertDingTalkWebhook,
    wecom: SettingKey.AlertWeComWebhook,
    serverchanSendKey: SettingKey.AlertServerChanSendKey,
    smtpHost: SettingKey.AlertSMTPHost,
    smtpPort: SettingKey.AlertSMTPPort,
    smtpUser: SettingKey.AlertSMTPUser,
    smtpFrom: SettingKey.AlertSMTPFrom,
    smtpTo: SettingKey.AlertSMTPTo,
} as const;

// SettingNotify 是「通知」功能区：一个事件投递到全部启用渠道，发送前可做一次真实测试（R-alert-001）。
export function SettingNotify() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    // 通知渠道配置字段多且形状一致（都是 string 设置键），用一个对象承载，避免十几对 state/ref。
    const [notifyFields, setNotifyFields] = useState<Record<string, string>>({});
    const [notifyResults, setNotifyResults] = useState<NotifyDeliveryResult[] | null>(null);
    const initialNotifyFields = useRef<Record<string, string>>({});

    useEffect(() => {
        if (!settings) return;
        const list = settings as Setting[];
        const notifyKeys = Object.values(NOTIFY_SETTING_KEYS) as string[];
        const found = list.filter(s => notifyKeys.includes(s.key));
        if (found.length === 0) return;
        const values: Record<string, string> = {};
        found.forEach(s => { values[s.key] = s.value; });
        queueMicrotask(() => setNotifyFields(prev => ({ ...prev, ...values })));
        initialNotifyFields.current = { ...initialNotifyFields.current, ...values };
    }, [settings]);

    const save = (key: string, value: string) => {
        if (value === (initialNotifyFields.current[key] ?? '')) return;
        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                initialNotifyFields.current = { ...initialNotifyFields.current, [key]: value };
                toast.success(t('saved'));
            },
            onError: (error) => toast.error(error instanceof Error ? error.message : String(error)),
        });
    };

    const notifyField = (key: string) => notifyFields[key] ?? '';
    const setNotifyField = (key: string, value: string) => setNotifyFields(prev => ({ ...prev, [key]: value }));

    const testNotify = useTestNotifyChannels();

    const runNotifyTest = () => {
        testNotify.mutate(undefined, {
            onSuccess: (results) => {
                setNotifyResults(results);
                const sent = results.filter(r => r.sent).length;
                if (sent > 0) toast.success(t('notify.testDone', { sent }));
                else toast.error(t('notify.testAllFailed'));
            },
            onError: (error) => toast.error(String(error)),
        });
    };

    // 通知渠道：勾选即写回 alert_channels（逗号分隔），顺序固定避免无意义 diff。
    const enabledNotifyKinds = (notifyFields[SettingKey.AlertChannels] ?? 'webhook')
        .split(',')
        .map(kind => kind.trim())
        .filter(Boolean);

    const toggleNotifyKind = (kind: string, checked: boolean) => {
        const next = new Set(enabledNotifyKinds);
        if (checked) next.add(kind); else next.delete(kind);
        const value = NOTIFY_KINDS.filter(known => next.has(known)).join(',');
        setNotifyFields(prev => ({ ...prev, [SettingKey.AlertChannels]: value }));
        save(SettingKey.AlertChannels, value);
    };

    // 每个渠道的输入框形状一致，用一份配置描述驱动渲染，避免十几段重复 JSX。
    const webhookFields: { key: string; label: string; placeholder: string; type?: string }[] = [
        { key: SettingKey.AlertWebhookURL, label: t('alerts.webhook'), placeholder: 'https://...', type: 'url' },
        { key: SettingKey.AlertFeishuWebhook, label: t('notify.feishu'), placeholder: 'https://open.feishu.cn/...', type: 'url' },
        { key: SettingKey.AlertDingTalkWebhook, label: t('notify.dingtalk'), placeholder: 'https://oapi.dingtalk.com/...', type: 'url' },
        { key: SettingKey.AlertWeComWebhook, label: t('notify.wecom'), placeholder: 'https://qyapi.weixin.qq.com/...', type: 'url' },
        { key: SettingKey.AlertServerChanSendKey, label: t('notify.serverchan'), placeholder: 'SCT...' },
    ];

    const smtpFields: { key: string; label: string; placeholder: string; type?: string }[] = [
        { key: SettingKey.AlertSMTPHost, label: t('notify.smtpHost'), placeholder: 'smtp.example.com' },
        { key: SettingKey.AlertSMTPPort, label: t('notify.smtpPort'), placeholder: '587', type: 'number' },
        { key: SettingKey.AlertSMTPUser, label: t('notify.smtpUser'), placeholder: 'user@example.com' },
        { key: SettingKey.AlertSMTPFrom, label: t('notify.smtpFrom'), placeholder: 'octopus@example.com' },
        { key: SettingKey.AlertSMTPTo, label: t('notify.smtpTo'), placeholder: 'ops@example.com' },
    ];

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <div className="flex items-center justify-between gap-2">
                <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                    <Send className="h-5 w-5" />
                    {t('notify.title')}
                </h2>
                <Button size="sm" variant="outline" className="rounded-xl" disabled={testNotify.isPending} onClick={runNotifyTest}>
                    {testNotify.isPending ? t('notify.testing') : t('notify.test')}
                </Button>
            </div>
            <p className="text-xs text-muted-foreground">{t('notify.hint')}</p>

            {/* 渠道开关：勾选顺序无关，写回时按固定顺序拼接 */}
            <div className="grid gap-2 sm:grid-cols-2">
                {NOTIFY_KINDS.map(kind => (
                    <label key={kind} className="flex items-center justify-between gap-3 rounded-xl border border-border/60 px-3 py-2 text-xs">
                        <span>{t(`notify.kind.${kind}`)}</span>
                        <Switch checked={enabledNotifyKinds.includes(kind)} onCheckedChange={(checked) => toggleNotifyKind(kind, checked)} />
                    </label>
                ))}
            </div>

            {/* Webhook 类渠道 */}
            <div className="space-y-3 rounded-2xl border border-border/50 bg-muted/20 p-3">
                <div className="flex items-center gap-2 text-sm font-semibold">
                    <BellRing className="size-4 text-primary" />
                    {t('notify.webhookGroup')}
                </div>
                <div className="grid gap-3 sm:grid-cols-3">
                    {webhookFields.map(field => (
                        <label key={field.key} className="grid gap-1 text-xs text-muted-foreground">
                            {field.label}
                            <Input
                                value={notifyField(field.key)}
                                onChange={(e) => setNotifyField(field.key, e.target.value)}
                                onBlur={() => save(field.key, notifyField(field.key))}
                                placeholder={field.placeholder}
                                type={field.type ?? 'text'}
                                className="rounded-xl"
                            />
                        </label>
                    ))}
                </div>
            </div>

            {/* SMTP 邮件渠道 */}
            <div className="space-y-3 rounded-2xl border border-border/50 bg-muted/20 p-3">
                <div className="flex items-center gap-2 text-sm font-semibold">
                    <Send className="size-4 text-primary" />
                    {t('notify.smtpGroup')}
                </div>
                <div className="grid gap-3 sm:grid-cols-5">
                    {smtpFields.map(field => (
                        <label key={field.key} className="grid gap-1 text-xs text-muted-foreground">
                            {field.label}
                            <Input
                                value={notifyField(field.key)}
                                onChange={(e) => setNotifyField(field.key, e.target.value)}
                                onBlur={() => save(field.key, notifyField(field.key))}
                                placeholder={field.placeholder}
                                type={field.type ?? 'text'}
                                className="rounded-xl"
                            />
                        </label>
                    ))}
                </div>
                <p className="text-xs text-muted-foreground">{t('notify.smtpPasswordHint')}</p>
            </div>

            {notifyResults && (
                <div className="space-y-1">
                    {notifyResults.map(result => (
                        <div key={result.kind} className="flex items-start justify-between gap-3 rounded-xl border border-border/60 px-3 py-2 text-xs">
                            <span className="font-medium">{t(`notify.kind.${result.kind}`)}</span>
                            <span className={result.sent ? 'text-muted-foreground' : 'text-destructive'}>
                                {result.sent ? t('notify.resultOk') : t('notify.resultFail', { reason: result.detail })}
                            </span>
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}
