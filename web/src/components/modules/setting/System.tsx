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
import { Monitor, Globe, Clock, Shield, Filter, HelpCircle, X, Gauge } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover';
import { useSettingList, useSetSetting, SettingKey, type Setting } from '@/api/setting';
import { toast } from 'sonner';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

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
    const initialProxyUrl = useRef('');
    const initialStatsSaveInterval = useRef('');
    const initialCorsAllowOrigins = useRef('');
    const initialModelFilter = useRef('');
    const initialQuotaScanInterval = useRef('');
    const initialQuotaAlertThreshold = useRef('');
    useEffect(() => {
        if (settings) {
            const proxy = settings.find(s => s.key === SettingKey.ProxyURL);
            const interval = settings.find(s => s.key === SettingKey.StatsSaveInterval);
            const cors = settings.find(s => s.key === SettingKey.CORSAllowOrigins);
            const modelFilterSetting = settings.find(s => s.key === SettingKey.ModelFilter);
            const quotaInterval = settings.find(s => s.key === SettingKey.QuotaScanInterval);
            const quotaThreshold = settings.find(s => s.key === SettingKey.QuotaAlertThreshold);
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
