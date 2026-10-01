import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Activity, Eye, Play, Plus, ShieldAlert, Trash2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    useAlertRules,
    useAlertFires,
    useCreateAlertRule,
    useUpdateAlertRule,
    useDeleteAlertRule,
    useEvaluateAlertRule,
    useRunAlertRules,
    usePreviewAlertRules,
    type AlertMetric,
    type AlertScope,
} from '@/api/alert-rule';
import { toast } from 'sonner';

const METRICS: AlertMetric[] = ['error_rate', 'latency'];

const EMPTY = {
    name: '',
    metric: 'error_rate' as AlertMetric,
    scope: 'all' as AlertScope,
    scope_value: '',
    threshold: 30,
    window_minutes: 15,
    min_requests: 5,
    cooldown_minutes: 60,
    enabled: true,
};

/**
 * 告警规则面板。
 *
 * 面板把「样本量下限」与「冷却」放在与阈值同等显眼的位置：它们是告警系统能不能用的分水岭。
 * 只给阈值不给这两项，用户配完第二天就会被噪音淹没然后关掉整个功能。
 *
 * 「试算」放在每条规则上（而不是全局）：用户最常问的是"这条规则为什么没报"，
 * 试算会把未触发的原因直接说出来（样本不足 / 冷却中 / 未越阈值）。
 */
export function SettingAlertRule() {
    const t = useTranslations('setting');
    const { data: rules, isLoading } = useAlertRules();
    const { data: fires } = useAlertFires();
    const createRule = useCreateAlertRule();
    const updateRule = useUpdateAlertRule();
    const deleteRule = useDeleteAlertRule();
    const evaluate = useEvaluateAlertRule();
    const runAll = useRunAlertRules();
    const previewAll = usePreviewAlertRules();

    const [draft, setDraft] = useState({ ...EMPTY });
    const [probe, setProbe] = useState<{ ruleId: number; rows: { channel: string; text: string }[] } | null>(null);

    const submit = () => {
        if (!draft.name.trim()) {
            toast.error(t('alertRule.nameRequired'));
            return;
        }
        if (draft.scope === 'channel' && !draft.scope_value.trim()) {
            toast.error(t('alertRule.channelRequired'));
            return;
        }
        createRule.mutate(
            { ...draft, name: draft.name.trim(), scope_value: draft.scope_value.trim() },
            {
                onSuccess: () => {
                    toast.success(t('saved'));
                    setDraft({ ...EMPTY });
                },
                onError: (error) => toast.error(String(error)),
            },
        );
    };

    const runEvaluate = (ruleId: number) => {
        evaluate.mutate(ruleId, {
            onSuccess: (rows) => {
                setProbe({
                    ruleId,
                    rows: rows.map((row) => ({
                        channel: row.channel,
                        text: `${row.channel} · ${row.metric === 'latency' ? `${Math.round(row.value)}ms` : `${row.value.toFixed(1)}%`} / ${row.requests} 次 · ${t(`alertRule.reason.${row.reason}`)}`,
                    })),
                });
                if (rows.length === 0) toast.info(t('alertRule.noSamples'));
            },
            onError: (error) => toast.error(String(error)),
        });
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <ShieldAlert className="size-4" />
                {t('alertRule.title')}
            </h2>
            <p className="text-sm text-muted-foreground leading-relaxed">{t('alertRule.description')}</p>

            {/* 新建规则 */}
            <div className="rounded-2xl border border-border/50 bg-muted/20 p-3 space-y-3">
                <div className="text-sm font-medium">{t('alertRule.newRule')}</div>
                <div className="grid gap-2 md:grid-cols-2">
                    <Input
                        placeholder={t('alertRule.namePlaceholder')}
                        value={draft.name}
                        onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                    />
                    <div className="flex gap-2">
                        <Select
                            value={draft.metric}
                            onValueChange={(v) => setDraft({ ...draft, metric: v as AlertMetric, threshold: v === 'latency' ? 5000 : 30 })}
                        >
                            <SelectTrigger className="w-32">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {METRICS.map((m) => (
                                    <SelectItem key={m} value={m}>
                                        {t(`alertRule.metrics.${m}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <Select
                            value={draft.scope}
                            onValueChange={(v) => setDraft({ ...draft, scope: v as AlertScope })}
                        >
                            <SelectTrigger className="w-32">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="all">{t('alertRule.scopes.all')}</SelectItem>
                                <SelectItem value="channel">{t('alertRule.scopes.channel')}</SelectItem>
                            </SelectContent>
                        </Select>
                        {draft.scope === 'channel' && (
                            <Input
                                placeholder={t('alertRule.channelPlaceholder')}
                                value={draft.scope_value}
                                onChange={(e) => setDraft({ ...draft, scope_value: e.target.value })}
                            />
                        )}
                    </div>
                </div>
                <div className="grid gap-2 md:grid-cols-4">
                    <label className="space-y-1 text-xs text-muted-foreground">
                        <span>{t('alertRule.threshold')}</span>
                        <Input
                            type="number"
                            value={draft.threshold}
                            onChange={(e) => setDraft({ ...draft, threshold: Number(e.target.value) })}
                        />
                    </label>
                    <label className="space-y-1 text-xs text-muted-foreground">
                        <span>{t('alertRule.window')}</span>
                        <Input
                            type="number"
                            value={draft.window_minutes}
                            onChange={(e) => setDraft({ ...draft, window_minutes: Number(e.target.value) })}
                        />
                    </label>
                    <label className="space-y-1 text-xs text-muted-foreground">
                        <span>{t('alertRule.minRequests')}</span>
                        <Input
                            type="number"
                            value={draft.min_requests}
                            onChange={(e) => setDraft({ ...draft, min_requests: Number(e.target.value) })}
                        />
                    </label>
                    <label className="space-y-1 text-xs text-muted-foreground">
                        <span>{t('alertRule.cooldown')}</span>
                        <Input
                            type="number"
                            value={draft.cooldown_minutes}
                            onChange={(e) => setDraft({ ...draft, cooldown_minutes: Number(e.target.value) })}
                        />
                    </label>
                </div>
                <div className="flex items-center justify-between">
                    <p className="text-xs text-muted-foreground">{t('alertRule.hint')}</p>
                    <Button type="button" onClick={submit} disabled={createRule.isPending}>
                        <Plus className="size-3.5" />
                        {t('alertRule.add')}
                    </Button>
                </div>
            </div>

            {/* 规则列表 */}
            <div className="space-y-2">
                {isLoading && <p className="text-sm text-muted-foreground">{t('alertRule.loading')}</p>}
                {!isLoading && (!rules || rules.length === 0) && (
                    <p className="text-sm text-muted-foreground">{t('alertRule.empty')}</p>
                )}
                {rules?.map((rule) => (
                    <div key={rule.id} className="rounded-2xl border border-border/50 p-3 space-y-2">
                        <div className="flex flex-wrap items-center gap-3 text-sm">
                            <Switch
                                checked={rule.enabled}
                                onCheckedChange={(checked) => updateRule.mutate({ id: rule.id, enabled: checked })}
                            />
                            <span className="font-medium">{rule.name}</span>
                            <Badge variant="outline">{t(`alertRule.metrics.${rule.metric}`)}</Badge>
                            <Badge variant="secondary">
                                {rule.metric === 'latency' ? `${rule.threshold}ms` : `${rule.threshold}%`}
                            </Badge>
                            <span className="text-xs text-muted-foreground">
                                {rule.scope === 'all' ? t('alertRule.scopes.all') : rule.scope_value} · {rule.window_minutes}
                                {t('alertRule.minutesShort')} · ≥{rule.min_requests} · {rule.cooldown_minutes}
                                {t('alertRule.minutesShort')}
                            </span>
                            <div className="ml-auto flex gap-1">
                                <Button type="button" variant="ghost" size="sm" onClick={() => runEvaluate(rule.id)}>
                                    <Activity className="size-3.5" />
                                </Button>
                                <Button
                                    type="button"
                                    variant="ghost"
                                    size="sm"
                                    onClick={() =>
                                        deleteRule.mutate(rule.id, {
                                            onSuccess: () => toast.success(t('saved')),
                                            onError: (error) => toast.error(String(error)),
                                        })
                                    }
                                >
                                    <Trash2 className="size-3.5" />
                                </Button>
                            </div>
                        </div>
                        {probe?.ruleId === rule.id && (
                            <div className="space-y-1 rounded-xl bg-muted/30 p-2 text-xs text-muted-foreground">
                                {probe.rows.length === 0 ? (
                                    <div>{t('alertRule.noSamples')}</div>
                                ) : (
                                    probe.rows.map((row, index) => <div key={index}>{row.text}</div>)
                                )}
                            </div>
                        )}
                    </div>
                ))}
            </div>

            {/* 立即检查 + 触发历史 */}
            <div className="flex flex-wrap items-center gap-2">
                <Button
                    type="button"
                    variant="ghost"
                    onClick={() =>
                        previewAll.mutate(undefined, {
                            onSuccess: (rows) => toast.info(`${t('alertRule.previewed')}: ${rows.length}`),
                            onError: (error) => toast.error(String(error)),
                        })
                    }
                    disabled={previewAll.isPending}
                >
                    <Eye className="size-3.5" />
                    {t('alertRule.preview')}
                </Button>
                <Button
                    type="button"
                    variant="secondary"
                    onClick={() =>
                        runAll.mutate(undefined, {
                            onSuccess: (res) => toast.success(`${t('alertRule.ran')}: ${res.fired}`),
                            onError: (error) => toast.error(String(error)),
                        })
                    }
                    disabled={runAll.isPending}
                >
                    <Play className="size-3.5" />
                    {t('alertRule.runNow')}
                </Button>
                {fires && fires.length > 0 && (
                    <span className="text-xs text-muted-foreground">
                        {t('alertRule.recentFires')}: {fires.length}
                    </span>
                )}
            </div>

            {fires && fires.length > 0 && (
                <div className="space-y-1">
                    {fires.slice(0, 5).map((fire) => (
                        <div key={fire.id} className="text-xs text-muted-foreground">
                            <span className="font-mono">{fire.fired_at?.slice(0, 19).replace('T', ' ')}</span> {fire.message}
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}
