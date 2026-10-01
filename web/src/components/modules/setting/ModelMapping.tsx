import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { ArrowRight, FlaskConical, Plus, Trash2, Wand2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Switch } from '@/components/ui/switch';
import { Badge } from '@/components/ui/badge';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    useModelMappings,
    useCreateModelMapping,
    useUpdateModelMapping,
    useDeleteModelMapping,
    useToggleModelMapping,
    useTestModelMapping,
    type ModelMapping,
    type ModelMatchType,
} from '@/api/model-mapping';
import { toast } from 'sonner';

const MATCH_TYPES: ModelMatchType[] = ['exact', 'wildcard', 'regex'];

const EMPTY: ModelMapping = {
    id: 0,
    name: '',
    pattern: '',
    match_type: 'exact',
    target_model: '',
    priority: 0,
    enabled: true,
};

/**
 * 模型名智能重写面板。
 *
 * 解决的问题：客户端写死带版本后缀的模型名（claude-3-5-sonnet-20241022），
 * 本地分组名却是简名（claude-sonnet），客户端因此拿到 model not found。
 *
 * 面板只做三件事：列规则、改规则、**试规则**（保存前后都能试）。
 * 「试一下」放在最显眼的位置，因为这类规则最容易写错——写宽了误伤别的模型，
 * 写窄了不命中，光看表达式看不出来，必须拿真实模型名跑一遍。
 */
export function SettingModelMapping() {
    const t = useTranslations('setting');
    const { data: rules, isLoading } = useModelMappings();
    const createRule = useCreateModelMapping();
    const updateRule = useUpdateModelMapping();
    const deleteRule = useDeleteModelMapping();
    const toggleRule = useToggleModelMapping();
    const testRule = useTestModelMapping();

    const [draft, setDraft] = useState<ModelMapping>({ ...EMPTY });
    const [editingId, setEditingId] = useState<number | null>(null);
    const [probe, setProbe] = useState('');
    const [probeResult, setProbeResult] = useState<{ matched: boolean; target: string; ruleName?: string } | null>(null);

    const resetDraft = () => {
        setDraft({ ...EMPTY });
        setEditingId(null);
    };

    const submit = () => {
        if (!draft.name.trim() || !draft.pattern.trim() || !draft.target_model.trim()) {
            toast.error(t('modelMapping.requiredHint'));
            return;
        }
        const payload = {
            name: draft.name.trim(),
            pattern: draft.pattern.trim(),
            match_type: draft.match_type,
            target_model: draft.target_model.trim(),
            priority: Number(draft.priority) || 0,
            enabled: draft.enabled,
        };
        const onSuccess = () => {
            toast.success(t('saved'));
            resetDraft();
        };
        const onError = (error: unknown) => toast.error(String(error));

        if (editingId) {
            updateRule.mutate({ id: editingId, ...payload }, { onSuccess, onError });
        } else {
            createRule.mutate(payload, { onSuccess, onError });
        }
    };

    const startEdit = (rule: ModelMapping) => {
        setDraft({ ...rule });
        setEditingId(rule.id);
    };

    const runProbe = () => {
        const name = probe.trim();
        if (!name) return;
        testRule.mutate(name, {
            onSuccess: (res) => {
                setProbeResult({
                    matched: res.matched,
                    target: res.target_model,
                    ruleName: res.matched_rule?.name,
                });
            },
            onError: (error) => toast.error(String(error)),
        });
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Wand2 className="size-4" />
                {t('modelMapping.title')}
            </h2>
            <p className="text-sm text-muted-foreground leading-relaxed">{t('modelMapping.description')}</p>

            {/* 试一下：这类规则最容易写错，先给一个立刻能验证的入口 */}
            <div className="rounded-2xl border border-border/50 bg-muted/20 p-3 space-y-2">
                <div className="flex items-center gap-2 text-sm font-medium">
                    <FlaskConical className="size-3.5" />
                    {t('modelMapping.tryIt')}
                </div>
                <div className="flex gap-2">
                    <Input
                        placeholder={t('modelMapping.tryPlaceholder')}
                        value={probe}
                        onChange={(e) => setProbe(e.target.value)}
                        onKeyDown={(e) => {
                            if (e.key === 'Enter') runProbe();
                        }}
                    />
                    <Button type="button" onClick={runProbe} disabled={testRule.isPending || !probe.trim()}>
                        {t('modelMapping.run')}
                    </Button>
                </div>
                {probeResult && (
                    <div className="flex items-center gap-2 text-sm">
                        <span className="font-mono text-xs">{probe.trim()}</span>
                        <ArrowRight className="size-3.5 shrink-0" />
                        {probeResult.matched ? (
                            <>
                                <span className="font-mono text-xs font-semibold">{probeResult.target}</span>
                                {probeResult.ruleName && <Badge variant="secondary">{probeResult.ruleName}</Badge>}
                            </>
                        ) : (
                            <span className="text-muted-foreground">{t('modelMapping.noMatch')}</span>
                        )}
                    </div>
                )}
            </div>

            {/* 规则编辑区 */}
            <div className="rounded-2xl border border-border/50 bg-muted/20 p-3 space-y-3">
                <div className="text-sm font-medium">
                    {editingId ? t('modelMapping.editing') : t('modelMapping.newRule')}
                </div>
                <div className="grid gap-2 md:grid-cols-2">
                    <Input
                        placeholder={t('modelMapping.namePlaceholder')}
                        value={draft.name}
                        onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                    />
                    <Input
                        placeholder={t('modelMapping.targetPlaceholder')}
                        value={draft.target_model}
                        onChange={(e) => setDraft({ ...draft, target_model: e.target.value })}
                    />
                    <Input
                        placeholder={t('modelMapping.patternPlaceholder')}
                        value={draft.pattern}
                        onChange={(e) => setDraft({ ...draft, pattern: e.target.value })}
                        className="font-mono text-xs md:col-span-2"
                    />
                    <div className="flex gap-2">
                        <Select
                            value={draft.match_type}
                            onValueChange={(value) => setDraft({ ...draft, match_type: value as ModelMatchType })}
                        >
                            <SelectTrigger className="w-32">
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {MATCH_TYPES.map((type) => (
                                    <SelectItem key={type} value={type}>
                                        {t(`modelMapping.matchType.${type}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                        <Input
                            type="number"
                            className="w-24"
                            placeholder={t('modelMapping.priority')}
                            value={draft.priority}
                            onChange={(e) => setDraft({ ...draft, priority: Number(e.target.value) })}
                        />
                        <div className="flex items-center gap-2">
                            <Switch
                                checked={draft.enabled}
                                onCheckedChange={(checked) => setDraft({ ...draft, enabled: checked })}
                            />
                            <span className="text-xs text-muted-foreground">{t('modelMapping.enabled')}</span>
                        </div>
                    </div>
                    <div className="flex justify-end gap-2">
                        {editingId && (
                            <Button type="button" variant="ghost" onClick={resetDraft}>
                                {t('modelMapping.cancel')}
                            </Button>
                        )}
                        <Button
                            type="button"
                            onClick={submit}
                            disabled={createRule.isPending || updateRule.isPending}
                        >
                            <Plus className="size-3.5" />
                            {editingId ? t('modelMapping.save') : t('modelMapping.add')}
                        </Button>
                    </div>
                </div>
                <p className="text-xs text-muted-foreground">{t('modelMapping.matchTypeHint')}</p>
            </div>

            {/* 规则列表 */}
            <div className="space-y-2">
                {isLoading && <p className="text-sm text-muted-foreground">{t('modelMapping.loading')}</p>}
                {!isLoading && (!rules || rules.length === 0) && (
                    <p className="text-sm text-muted-foreground">{t('modelMapping.empty')}</p>
                )}
                {rules?.map((rule) => (
                    <div
                        key={rule.id}
                        className="flex flex-wrap items-center gap-3 rounded-2xl border border-border/50 p-3 text-sm"
                    >
                        <Switch
                            checked={rule.enabled}
                            onCheckedChange={(checked) => toggleRule.mutate({ id: rule.id, enabled: checked })}
                        />
                        <div className="min-w-0 flex-1">
                            <div className="flex items-center gap-2">
                                <span className="truncate font-medium">{rule.name}</span>
                                <Badge variant="outline">{t(`modelMapping.matchType.${rule.match_type}`)}</Badge>
                                {rule.priority !== 0 && (
                                    <Badge variant="secondary">
                                        {t('modelMapping.priority')} {rule.priority}
                                    </Badge>
                                )}
                            </div>
                            <div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground">
                                <span className="truncate font-mono">{rule.pattern}</span>
                                <ArrowRight className="size-3 shrink-0" />
                                <span className="truncate font-mono">{rule.target_model}</span>
                            </div>
                        </div>
                        <Button type="button" variant="ghost" size="sm" onClick={() => startEdit(rule)}>
                            {t('modelMapping.edit')}
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
                ))}
            </div>
        </div>
    );
}
