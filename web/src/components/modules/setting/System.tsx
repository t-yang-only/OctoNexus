import { useEffect, useMemo, useRef, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Monitor, Globe, Clock, Shield, Filter, HelpCircle, X, Gauge, BellRing, Scale } from 'lucide-react';
import { Input } from '@/components/ui/input';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useSettingList, useSetSetting, SettingKey } from '@/api/setting';
import { toast } from 'sonner';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';
import { Switch } from '@/components/ui/switch';

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
                }
            }
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
                <label className="grid gap-1 text-xs text-muted-foreground"><span className="flex items-center gap-1"><BellRing className="size-3.5" />{t('alerts.webhook')}</span><Input value={alertWebhookUrl} onChange={(e) => setAlertWebhookUrl(e.target.value)} onBlur={() => handleSave(SettingKey.AlertWebhookURL, alertWebhookUrl, initialAlertWebhookUrl.current)} placeholder="https://..." type="url" className="rounded-xl" /></label>
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
