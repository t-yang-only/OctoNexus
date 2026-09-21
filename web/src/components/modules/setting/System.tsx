import { useEffect, useMemo, useRef, useState } from 'react';
// WeightSettingField 是加权综合选路的一组设置控件（数字或下拉）。
// 自包含: 自己从设置列表取当前值、自己保存并 toast, 免得为 10 个键再抄一遍父组件的 state/ref/同步样板。
function WeightSettingField({ settingKey, label, kind, options, max, hint }: {
    settingKey: string;
    label: string;
    kind: 'number' | 'select';
    options?: { value: string; label: string }[];
    // max 是数字控件的上限: 加权维度是 0..100, 而分压设置里还有 token 数/RPM 这类更大的量纲
    // （T-allocate-001）, 故不能把 100 写死在控件上。
    max?: string;
    hint?: string;
}) {
    const t = useTranslations('setting');
    const settingsQuery = useSettingList();
    const setSetting = useSetSetting();
    const [value, setValue] = useState<string>('');
    const initial = useRef<string>('');
    const effective = (settingsQuery.data ?? []) as Setting[];

    useEffect(() => {
        const found = effective.find((s) => s.key === settingKey);
        if (found) {
            initial.current = found.value;
            queueMicrotask(() => setValue(found.value));
        }
    }, [effective, settingKey]);

    const save = (next: string) => {
        if (next === initial.current) return;
        setSetting.mutate({ key: settingKey, value: next }, {
            onSuccess: () => {
                initial.current = next;
                toast.success(t('saved'));
            },
            onError: (error) => toast.error(error instanceof Error ? error.message : String(error)),
        });
    };

    if (kind === 'select') {
        return (
            <label className="grid gap-1 text-xs text-muted-foreground">
                {label}
                <Select value={value || (options?.[0]?.value ?? '')} onValueChange={(next) => { setValue(next); save(next); }}>
                    <SelectTrigger className="rounded-xl"><SelectValue /></SelectTrigger>
                    <SelectContent>
                        {(options ?? []).map((option) => (
                            <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            </label>
        );
    }

    return (
        <label className="grid gap-1 text-xs text-muted-foreground">
            {label}
            {hint && <span className="text-[11px] text-muted-foreground/80">{hint}</span>}
            <Input
                type="number"
                min="0"
                max={max ?? '100'}
                value={value}
                onChange={(e) => setValue(e.target.value)}
                onBlur={() => save(value)}
                className="rounded-xl"
            />
        </label>
    );
}

import { useTranslations } from 'use-intl';
import { Monitor, Globe, Clock, Shield, Filter, HelpCircle, X, Gauge, BellRing, Scale, Send } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useSettingList, useSetSetting, useTestNotifyChannels, SettingKey, type Setting, type NotifyDeliveryResult } from '@/api/setting';
import { toast } from 'sonner';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { Switch } from '@/components/ui/switch';

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

export function SettingSystem() {
    const t = useTranslations('setting');
    const { data: settings } = useSettingList();
    const setSetting = useSetSetting();

    const [proxyUrl, setProxyUrl] = useState('');
    const [statsSaveInterval, setStatsSaveInterval] = useState('');
    const [corsAllowOrigins, setCorsAllowOrigins] = useState('');
    const [corsInputValue, setCorsInputValue] = useState('');
    const [modelFilter, setModelFilter] = useState('');
    const [quotaScanInterval, setQuotaScanInterval] = useState('');
    const [quotaAlertThreshold, setQuotaAlertThreshold] = useState('');
    const [alertWebhookUrl, setAlertWebhookUrl] = useState('');
    const [routeBalanceEnabled, setRouteBalanceEnabled] = useState(false);
    const [routeProbeEnabled, setRouteProbeEnabled] = useState(false);
    const [routeProbeInterval, setRouteProbeInterval] = useState('');
    // 通知渠道配置字段多且形状一致（都是 string 设置键），用一个对象承载，避免十几对 state/ref。
    const [notifyFields, setNotifyFields] = useState<Record<string, string>>({});
    const [notifyResults, setNotifyResults] = useState<NotifyDeliveryResult[] | null>(null);

    const initialProxyUrl = useRef('');
    const initialStatsSaveInterval = useRef('');
    const initialCorsAllowOrigins = useRef('');
    const initialModelFilter = useRef('');
    const initialQuotaScanInterval = useRef('');
    const initialQuotaAlertThreshold = useRef('');
    const initialAlertWebhookUrl = useRef('');
    const initialRouteBalanceEnabled = useRef(false);
    const initialRouteProbeEnabled = useRef(false);
    const initialRouteProbeInterval = useRef('');
    const initialNotifyFields = useRef<Record<string, string>>({});

    useEffect(() => {
        if (settings) {
            const proxy = settings.find(s => s.key === SettingKey.ProxyURL);
            const interval = settings.find(s => s.key === SettingKey.StatsSaveInterval);
            const cors = settings.find(s => s.key === SettingKey.CORSAllowOrigins);
            const modelFilterSetting = settings.find(s => s.key === SettingKey.ModelFilter);
            const quotaInterval = settings.find(s => s.key === SettingKey.QuotaScanInterval);
            const quotaThreshold = settings.find(s => s.key === SettingKey.QuotaAlertThreshold);
            const webhook = settings.find(s => s.key === SettingKey.AlertWebhookURL);
            const balance = settings.find(s => s.key === SettingKey.RouteBalanceEnabled);
            if (proxy) {
                queueMicrotask(() => setProxyUrl(proxy.value));
                initialProxyUrl.current = proxy.value;
            }
            if (interval) {
                queueMicrotask(() => setStatsSaveInterval(interval.value));
                initialStatsSaveInterval.current = interval.value;
            }
            if (cors) {
                queueMicrotask(() => setCorsAllowOrigins(cors.value));
                initialCorsAllowOrigins.current = cors.value;
            }
            if (modelFilterSetting) {
                queueMicrotask(() => setModelFilter(modelFilterSetting.value));
                initialModelFilter.current = modelFilterSetting.value;
            }
            if (quotaInterval) { queueMicrotask(() => setQuotaScanInterval(quotaInterval.value)); initialQuotaScanInterval.current = quotaInterval.value; }
            if (quotaThreshold) { queueMicrotask(() => setQuotaAlertThreshold(quotaThreshold.value)); initialQuotaAlertThreshold.current = quotaThreshold.value; }
            if (webhook) { queueMicrotask(() => setAlertWebhookUrl(webhook.value)); initialAlertWebhookUrl.current = webhook.value; }
            if (balance) { const enabled = balance.value === 'true'; queueMicrotask(() => setRouteBalanceEnabled(enabled)); initialRouteBalanceEnabled.current = enabled; }
            const probe = settings.find(s => s.key === SettingKey.RouteProbeEnabled);
            const probeInterval = settings.find(s => s.key === SettingKey.RouteProbeInterval);
            if (probe) { const enabled = probe.value === 'true'; queueMicrotask(() => setRouteProbeEnabled(enabled)); initialRouteProbeEnabled.current = enabled; }
            if (probeInterval) { queueMicrotask(() => setRouteProbeInterval(probeInterval.value)); initialRouteProbeInterval.current = probeInterval.value; }
            const notifyKeys = Object.values(NOTIFY_SETTING_KEYS) as string[];
            const found = settings.filter(s => notifyKeys.includes(s.key));
            if (found.length > 0) {
                const values: Record<string, string> = {};
                found.forEach(s => { values[s.key] = s.value; });
                queueMicrotask(() => setNotifyFields(prev => ({ ...prev, ...values })));
                initialNotifyFields.current = { ...initialNotifyFields.current, ...values };
            }
        }
    }, [settings]);

    const handleSave = (key: string, value: string, initialValue: string) => {
        if (value === initialValue) return;

        setSetting.mutate({ key, value }, {
            onSuccess: () => {
                toast.success(t('saved'));
                if (key === SettingKey.ProxyURL) {
                    initialProxyUrl.current = value;
                } else if (key === SettingKey.StatsSaveInterval) {
                    initialStatsSaveInterval.current = value;
                } else if (key === SettingKey.CORSAllowOrigins) {
                    initialCorsAllowOrigins.current = value;
                } else if (key === SettingKey.ModelFilter) {
                    initialModelFilter.current = value;
                } else if (key === SettingKey.QuotaScanInterval) {
                    initialQuotaScanInterval.current = value;
                } else if (key === SettingKey.QuotaAlertThreshold) {
                    initialQuotaAlertThreshold.current = value;
                } else if (key === SettingKey.AlertWebhookURL) {
                    initialAlertWebhookUrl.current = value;
                } else if (key === SettingKey.RouteBalanceEnabled) {
                    initialRouteBalanceEnabled.current = value === 'true';
                } else if (key === SettingKey.RouteProbeEnabled) {
                    initialRouteProbeEnabled.current = value === 'true';
                } else if (key === SettingKey.RouteProbeInterval) {
                    initialRouteProbeInterval.current = value;
                } else if ((Object.values(NOTIFY_SETTING_KEYS) as string[]).includes(key)) {
                    initialNotifyFields.current = { ...initialNotifyFields.current, [key]: value };
                }
            }
        });
    };

    const testNotify = useTestNotifyChannels();

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
        handleSave(SettingKey.AlertChannels, value, initialNotifyFields.current[SettingKey.AlertChannels] ?? '');
    };

    const notifyField = (key: string) => notifyFields[key] ?? '';

    const setNotifyField = (key: string, value: string) => setNotifyFields(prev => ({ ...prev, [key]: value }));

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

    const corsAllowOriginsList = useMemo(() => {
        const value = corsAllowOrigins.trim();
        if (!value) return [];
        if (value === '*') return ['*'];
        return Array.from(new Set(
            value
                .split(/[,\n，]/)
                .map(item => item.trim())
                .filter(Boolean)
        ));
    }, [corsAllowOrigins]);

    const corsAllowOriginsDisplay = useMemo(
        () => (corsAllowOriginsList.length > 0 ? corsAllowOriginsList.join(', ') : t('corsAllowOrigins.hint')),
        [corsAllowOriginsList, t]
    );

    const saveCorsAllowOrigins = (origins: string[]) => {
        const normalizedOrigins = Array.from(new Set(
            origins
                .map(origin => origin.trim())
                .filter(Boolean)
        ));
        const normalizedValue = normalizedOrigins.includes('*') ? '*' : normalizedOrigins.join(',');
        setCorsAllowOrigins(normalizedValue);
        handleSave(SettingKey.CORSAllowOrigins, normalizedValue, initialCorsAllowOrigins.current);
    };

    const handleAddCorsOrigin = () => {
        const newOrigins = Array.from(new Set(
            corsInputValue
                .split(/[,\n，]/)
                .map(item => item.trim())
                .filter(Boolean)
        ));
        if (newOrigins.length === 0) return;

        if (newOrigins.includes('*')) {
            saveCorsAllowOrigins(['*']);
            setCorsInputValue('');
            return;
        }

        const base = corsAllowOriginsList.includes('*') ? [] : corsAllowOriginsList;
        const merged = Array.from(new Set([...base, ...newOrigins]));
        saveCorsAllowOrigins(merged);
        setCorsInputValue('');
    };

    const handleRemoveCorsOrigin = (originToRemove: string) => {
        const nextOrigins = corsAllowOriginsList.filter(origin => origin !== originToRemove);
        saveCorsAllowOrigins(nextOrigins);
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Monitor className="h-5 w-5" />
                {t('system')}
            </h2>

            {/* 代理地址 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Globe className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('proxyUrl.label')}</span>
                </div>
                <Input
                    value={proxyUrl}
                    onChange={(e) => setProxyUrl(e.target.value)}
                    onBlur={() => handleSave('proxy_url', proxyUrl, initialProxyUrl.current)}
                    placeholder={t('proxyUrl.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 统计保存周期 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Clock className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('statsSaveInterval.label')}</span>
                </div>
                <Input
                    type="number"
                    value={statsSaveInterval}
                    onChange={(e) => setStatsSaveInterval(e.target.value)}
                    onBlur={() => handleSave('stats_save_interval', statsSaveInterval, initialStatsSaveInterval.current)}
                    placeholder={t('statsSaveInterval.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 全局模型过滤 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Filter className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('modelFilter.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center">
                            {t('modelFilter.hint')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Input
                    value={modelFilter}
                    onChange={(e) => setModelFilter(e.target.value)}
                    onBlur={() => handleSave('model_filter', modelFilter, initialModelFilter.current)}
                    placeholder={t('modelFilter.placeholder')}
                    className="w-48 rounded-xl"
                />
            </div>

            {/* 余额扫描与告警 */}
            <div className="space-y-3 rounded-2xl border border-border/50 bg-muted/20 p-3">
                <div className="flex items-center gap-2 text-sm font-semibold"><Gauge className="size-4 text-primary" />{t('quota.title')}</div>
                <div className="grid gap-3 sm:grid-cols-2">
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('quota.scanInterval')}<Input type="number" min="0" value={quotaScanInterval} onChange={(e) => setQuotaScanInterval(e.target.value)} onBlur={() => handleSave(SettingKey.QuotaScanInterval, quotaScanInterval, initialQuotaScanInterval.current)} className="rounded-xl" /></label>
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('quota.alertThreshold')}<Input type="number" min="0" step="any" value={quotaAlertThreshold} onChange={(e) => setQuotaAlertThreshold(e.target.value)} onBlur={() => handleSave(SettingKey.QuotaAlertThreshold, quotaAlertThreshold, initialQuotaAlertThreshold.current)} placeholder={t('quota.unlimited')} className="rounded-xl" /></label>
                </div>
            </div>
            {/* 均衡开关与 webhook 告警 */}
            <div className="space-y-3 rounded-2xl border border-border/50 bg-muted/20 p-3">
                <div className="flex items-center gap-2 text-sm font-semibold"><Scale className="size-4 text-primary" />{t('routing.title')}</div>
                <div className="flex items-center justify-between gap-4"><div><p className="text-sm font-medium">{t('routing.balance')}</p><p className="text-xs text-muted-foreground">{t('routing.balanceHint')}</p></div><Switch checked={routeBalanceEnabled} onCheckedChange={(checked) => { setRouteBalanceEnabled(checked); handleSave(SettingKey.RouteBalanceEnabled, String(checked), String(initialRouteBalanceEnabled.current)); }} /></div>
                <div className="flex items-center justify-between gap-4"><div><p className="text-sm font-medium">{t('routing.probe')}</p><p className="text-xs text-muted-foreground">{t('routing.probeHint')}</p></div><Switch checked={routeProbeEnabled} onCheckedChange={(checked) => { setRouteProbeEnabled(checked); handleSave(SettingKey.RouteProbeEnabled, String(checked), String(initialRouteProbeEnabled.current)); }} /></div>
                <label className="grid gap-1 text-xs text-muted-foreground">{t('routing.probeInterval')}<Input type="number" min="0" value={routeProbeInterval} onChange={(e) => setRouteProbeInterval(e.target.value)} onBlur={() => handleSave(SettingKey.RouteProbeInterval, routeProbeInterval, initialRouteProbeInterval.current)} className="rounded-xl" /></label>
                {/* 加权综合选路（weighted 模式）的维度权重: 0 表示该维度不参与, 9 个维度合计不必凑满 100。 */}
                <div id="weighted-weights" className="mt-2 grid gap-3 rounded-xl border border-border p-3">
                    <div>
                        <p className="text-sm font-medium">{t('routing.weights')}</p>
                        <p className="text-xs text-muted-foreground">{t('routing.weightsHint')}</p>
                    </div>
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                        <WeightSettingField settingKey="route_weight_cost" label={t('routing.weightCost')} kind="number" />
                        <WeightSettingField settingKey="route_weight_quality" label={t('routing.weightQuality')} kind="number" />
                        <WeightSettingField settingKey="route_weight_latency" label={t('routing.weightLatency')} kind="number" />
                        <WeightSettingField settingKey="route_weight_busy" label={t('routing.weightBusy')} kind="number" />
                        <WeightSettingField settingKey="route_weight_load" label={t('routing.weightLoad')} kind="number" />
                        <WeightSettingField settingKey="route_weight_multiplier" label={t('routing.weightMultiplier')} kind="number" />
                        <WeightSettingField settingKey="route_weight_per_call" label={t('routing.weightPerCall')} kind="number" />
                        <WeightSettingField settingKey="route_weight_balance" label={t('routing.weightBalance')} kind="number" />
                        <WeightSettingField settingKey="route_weight_monthly" label={t('routing.weightMonthly')} kind="number" />
                        <WeightSettingField settingKey="route_monthly_exhausted_action" label={t('routing.monthlyAction')} kind="select"
                            options={[{ value: 'demote', label: t('routing.monthlyDemote') }, { value: 'exclude', label: t('routing.monthlyExclude') }]} />
                        {/* 上游判定"请求本身非法"（400 一类）时的取向: 换成员再试 / 立刻回上游原文 */}
                        <WeightSettingField settingKey="relay_request_fault_action" label={t('routing.requestFaultAction')} kind="select"
                            options={[{ value: 'failover', label: t('routing.requestFaultFailover') }, { value: 'failfast', label: t('routing.requestFaultFailfast') }]} />
                    </div>
                </div>
                {/* 额度分压（allocate 模式, T-allocate-001）：剩余请求数的折算、健康折扣与自限流。
                    全部设置都由选路层读取（allocateSettingsOf）, 面板只负责写值。 */}
                <div id="allocate-settings" className="mt-2 grid gap-3 rounded-xl border border-border p-3">
                    <div>
                        <p className="text-sm font-medium">{t('routing.allocateTitle')}</p>
                        <p className="text-xs text-muted-foreground">{t('routing.allocateHint')}</p>
                    </div>
                    <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                        <WeightSettingField settingKey="route_allocate_estimate_tokens" label={t('routing.allocateTokens')} hint={t('routing.allocateTokensHint')} kind="number" max="2000000" />
                        <WeightSettingField settingKey="route_allocate_health_weight" label={t('routing.allocateHealth')} hint={t('routing.allocateHealthHint')} kind="number" />
                        <WeightSettingField settingKey="route_allocate_slow_latency_ms" label={t('routing.allocateSlowMs')} hint={t('routing.allocateSlowMsHint')} kind="number" max="600000" />
                        <WeightSettingField settingKey="route_allocate_min_requests" label={t('routing.allocateMinRequests')} hint={t('routing.allocateMinRequestsHint')} kind="number" max="1000000" />
                        <WeightSettingField settingKey="route_member_rpm_limit" label={t('routing.memberRpmLimit')} hint={t('routing.memberRpmLimitHint')} kind="number" max="1000000" />
                        <WeightSettingField settingKey="route_member_tpm_limit" label={t('routing.memberTpmLimit')} hint={t('routing.memberTpmLimitHint')} kind="number" max="1000000000" />
                        <WeightSettingField settingKey="route_ratelimit_cooldown_max_seconds" label={t('routing.throttleCap')} hint={t('routing.throttleCapHint')} kind="number" max="86400" />
                    </div>
                    {/* 速度维度（T-speed-001）：把"速度不好"变成两个可调的量——谁算慢、慢成员少拿多少流量,
                        外加一条"慢成员更早被放弃"的自适应首帧看门狗。 */}
                    <div className="mt-1 rounded-xl border border-border/60 p-3">
                        <p className="text-sm font-medium">{t('routing.speedTitle')}</p>
                        <p className="text-xs text-muted-foreground">{t('routing.speedHint')}</p>
                        <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
                            <WeightSettingField settingKey="route_allocate_speed_weight" label={t('routing.speedWeight')} hint={t('routing.speedWeightHint')} kind="number" />
                            <WeightSettingField settingKey="route_speed_slow_ttfb_ms" label={t('routing.speedSlowTtfb')} hint={t('routing.speedSlowTtfbHint')} kind="number" max="600000" />
                            <WeightSettingField settingKey="route_speed_slow_tps" label={t('routing.speedSlowTps')} hint={t('routing.speedSlowTpsHint')} kind="number" max="100000" />
                            <WeightSettingField settingKey="route_speed_first_event_multiple" label={t('routing.speedFeMultiple')} hint={t('routing.speedFeMultipleHint')} kind="number" max="100" />
                            <WeightSettingField settingKey="route_speed_first_event_floor_ms" label={t('routing.speedFeFloor')} hint={t('routing.speedFeFloorHint')} kind="number" max="600000" />
                        </div>
                    </div>
                    {/* 入站正文上限（T-bodylimit-001）：依赖库把上限写死成 64 MiB，Codex 的 remote compact
                        这类大正文会被自家网关先拒掉（报错还长得像上游错误）。这里把它变成可调的一项：
                        0 = 不限制，默认 256 MiB。提示里要写明"开大上限=单请求内存上升"的代价。 */}
                    <div className="mt-1 rounded-xl border border-border/60 p-3">
                        <p className="text-sm font-medium">{t('routing.bodyLimitTitle')}</p>
                        <p className="text-xs text-muted-foreground">{t('routing.bodyLimitHint')}</p>
                        <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
                            <WeightSettingField settingKey="relay_max_request_body_bytes" label={t('routing.bodyLimit')} hint={t('routing.bodyLimitHint2')} kind="number" max="4294967296" />
                        </div>
                    </div>
                </div>
                <label className="grid gap-1 text-xs text-muted-foreground"><span className="flex items-center gap-1"><BellRing className="size-3.5" />{t('alerts.webhook')}</span><Input value={alertWebhookUrl} onChange={(e) => setAlertWebhookUrl(e.target.value)} onBlur={() => handleSave(SettingKey.AlertWebhookURL, alertWebhookUrl, initialAlertWebhookUrl.current)} placeholder="https://..." type="url" className="rounded-xl" /></label>
            </div>

            {/* 通知渠道（R-alert-001）：一个事件投递到全部启用渠道 + 发送前真实测试 */}
            <div className="space-y-3 rounded-2xl border border-border/50 bg-muted/20 p-3">
                <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-2 text-sm font-semibold"><Send className="size-4 text-primary" />{t('notify.title')}</div>
                    <Button size="sm" variant="outline" className="rounded-xl" disabled={testNotify.isPending} onClick={runNotifyTest}>
                        {testNotify.isPending ? t('notify.testing') : t('notify.test')}
                    </Button>
                </div>
                <p className="text-xs text-muted-foreground">{t('notify.hint')}</p>
                <div className="grid gap-2 sm:grid-cols-2">
                    {NOTIFY_KINDS.map(kind => (
                        <label key={kind} className="flex items-center justify-between gap-3 rounded-xl border border-border/60 px-3 py-2 text-xs">
                            <span>{t(`notify.kind.${kind}`)}</span>
                            <Switch checked={enabledNotifyKinds.includes(kind)} onCheckedChange={(checked) => toggleNotifyKind(kind, checked)} />
                        </label>
                    ))}
                </div>
                <div className="grid gap-3 sm:grid-cols-4">
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.feishu')}<Input value={notifyField(SettingKey.AlertFeishuWebhook)} onChange={(e) => setNotifyField(SettingKey.AlertFeishuWebhook, e.target.value)} onBlur={() => handleSave(SettingKey.AlertFeishuWebhook, notifyField(SettingKey.AlertFeishuWebhook), initialNotifyFields.current[SettingKey.AlertFeishuWebhook] ?? '')} placeholder="https://open.feishu.cn/..." type="url" className="rounded-xl" /></label>
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.dingtalk')}<Input value={notifyField(SettingKey.AlertDingTalkWebhook)} onChange={(e) => setNotifyField(SettingKey.AlertDingTalkWebhook, e.target.value)} onBlur={() => handleSave(SettingKey.AlertDingTalkWebhook, notifyField(SettingKey.AlertDingTalkWebhook), initialNotifyFields.current[SettingKey.AlertDingTalkWebhook] ?? '')} placeholder="https://oapi.dingtalk.com/..." type="url" className="rounded-xl" /></label>
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.wecom')}<Input value={notifyField(SettingKey.AlertWeComWebhook)} onChange={(e) => setNotifyField(SettingKey.AlertWeComWebhook, e.target.value)} onBlur={() => handleSave(SettingKey.AlertWeComWebhook, notifyField(SettingKey.AlertWeComWebhook), initialNotifyFields.current[SettingKey.AlertWeComWebhook] ?? '')} placeholder="https://qyapi.weixin.qq.com/..." type="url" className="rounded-xl" /></label>
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.serverchan')}<Input value={notifyField(SettingKey.AlertServerChanSendKey)} onChange={(e) => setNotifyField(SettingKey.AlertServerChanSendKey, e.target.value)} onBlur={() => handleSave(SettingKey.AlertServerChanSendKey, notifyField(SettingKey.AlertServerChanSendKey), initialNotifyFields.current[SettingKey.AlertServerChanSendKey] ?? '')} placeholder="SCT..." className="rounded-xl" /></label>
                </div>
                <div className="grid gap-3 sm:grid-cols-5">
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.smtpHost')}<Input value={notifyField(SettingKey.AlertSMTPHost)} onChange={(e) => setNotifyField(SettingKey.AlertSMTPHost, e.target.value)} onBlur={() => handleSave(SettingKey.AlertSMTPHost, notifyField(SettingKey.AlertSMTPHost), initialNotifyFields.current[SettingKey.AlertSMTPHost] ?? '')} placeholder="smtp.example.com" className="rounded-xl" /></label>
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.smtpPort')}<Input type="number" min="0" value={notifyField(SettingKey.AlertSMTPPort)} onChange={(e) => setNotifyField(SettingKey.AlertSMTPPort, e.target.value)} onBlur={() => handleSave(SettingKey.AlertSMTPPort, notifyField(SettingKey.AlertSMTPPort), initialNotifyFields.current[SettingKey.AlertSMTPPort] ?? '')} placeholder="587" className="rounded-xl" /></label>
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.smtpUser')}<Input value={notifyField(SettingKey.AlertSMTPUser)} onChange={(e) => setNotifyField(SettingKey.AlertSMTPUser, e.target.value)} onBlur={() => handleSave(SettingKey.AlertSMTPUser, notifyField(SettingKey.AlertSMTPUser), initialNotifyFields.current[SettingKey.AlertSMTPUser] ?? '')} placeholder="user@example.com" className="rounded-xl" /></label>
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.smtpFrom')}<Input value={notifyField(SettingKey.AlertSMTPFrom)} onChange={(e) => setNotifyField(SettingKey.AlertSMTPFrom, e.target.value)} onBlur={() => handleSave(SettingKey.AlertSMTPFrom, notifyField(SettingKey.AlertSMTPFrom), initialNotifyFields.current[SettingKey.AlertSMTPFrom] ?? '')} placeholder="octopus@example.com" className="rounded-xl" /></label>
                    <label className="grid gap-1 text-xs text-muted-foreground">{t('notify.smtpTo')}<Input value={notifyField(SettingKey.AlertSMTPTo)} onChange={(e) => setNotifyField(SettingKey.AlertSMTPTo, e.target.value)} onBlur={() => handleSave(SettingKey.AlertSMTPTo, notifyField(SettingKey.AlertSMTPTo), initialNotifyFields.current[SettingKey.AlertSMTPTo] ?? '')} placeholder="ops@example.com" className="rounded-xl" /></label>
                </div>
                <p className="text-xs text-muted-foreground">{t('notify.smtpPasswordHint')}</p>
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

            {/* CORS 跨域白名单 */}
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <Shield className="h-5 w-5 text-muted-foreground" />
                    <span className="text-sm font-medium">{t('corsAllowOrigins.label')}</span>
                    <Tooltip>
                        <TooltipTrigger asChild>
                            <HelpCircle className="size-4 text-muted-foreground cursor-help" />
                        </TooltipTrigger>
                        <TooltipContent side="top" sideOffset={10} align="center">
                            {t('corsAllowOrigins.hint')}
                            <br />
                            {t('corsAllowOrigins.example')}
                        </TooltipContent>
                    </Tooltip>
                </div>
                <Popover>
                    <PopoverTrigger asChild>
                        <button
                            type="button"
                            className="border-input focus-visible:border-ring focus-visible:ring-ring/50 w-48 min-h-9 rounded-xl border bg-transparent px-3 py-2 text-left text-sm shadow-xs transition-[color,box-shadow] outline-none focus-visible:ring-[3px]"
                        >
                            <span className={`block overflow-hidden text-ellipsis whitespace-nowrap ${corsAllowOriginsList.length === 0 ? 'text-muted-foreground' : ''}`}>
                                {corsAllowOriginsDisplay}
                            </span>
                        </button>
                    </PopoverTrigger>
                    <PopoverContent className="w-72 space-y-2 rounded-3xl p-3 bg-card">
                        <Input
                            value={corsInputValue}
                            onChange={(e) => setCorsInputValue(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === 'Enter') {
                                    e.preventDefault();
                                    handleAddCorsOrigin();
                                }
                            }}
                            placeholder={t('corsAllowOrigins.example')}
                            className="h-9 rounded-xl"
                            autoFocus
                        />
                        <div className="max-h-48 space-y-1 overflow-y-auto">
                            {corsAllowOriginsList.length > 0 && (
                                corsAllowOriginsList.map((origin) => (
                                    <div key={origin} className="flex items-center justify-between gap-2 rounded-xl border border-border/60 px-2 py-1">
                                        <span className="break-all text-xs leading-5">{origin}</span>
                                        <button
                                            type="button"
                                            onClick={() => handleRemoveCorsOrigin(origin)}
                                            className="text-muted-foreground transition-colors hover:text-destructive"
                                            aria-label={`remove ${origin}`}
                                        >
                                            <X className="size-4" />
                                        </button>
                                    </div>
                                ))
                            )}
                        </div>
                    </PopoverContent>
                </Popover>
            </div>
        </div>
    );
}
