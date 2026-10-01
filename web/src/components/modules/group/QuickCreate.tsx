import { useMemo, useState } from 'react';
import { useTranslations } from 'use-intl';
import { Check, Layers, Loader2 } from 'lucide-react';
import { toast } from 'sonner';
import {
    MorphingDialogClose,
    MorphingDialogDescription,
    MorphingDialogTitle,
    useMorphingDialog,
} from '@/components/ui/morphing-dialog';
import { Button } from '@/components/ui/button';
import { useChannelGrantList } from '@/api/channel';
import { useQuickCreateGroup } from '@/api/group';

// 快速建组（需求1）：选渠道 → 选模型（可再指定某条凭据）→ 一键建出 `渠道/模型` 分组。
//
// 为什么单独一个对话框而不是并进分组编辑器：
// 编辑器面向"我要精细编排成员"，而这里面向"把某渠道的某个模型拉出来"——
// 后者是最高频且最机械的诉求（备份里 202 个 `渠道/模型` 分组全是手工建的）。
// 两条路径混在一起会让编辑器承担它不需要承担的简单场景。
export function QuickCreateDialogContent() {
    const { setIsOpen } = useMorphingDialog();
    const t = useTranslations('group');
    const { data: grants, isLoading } = useChannelGrantList(true);
    const quickCreate = useQuickCreateGroup();

    const [channelID, setChannelID] = useState<number | null>(null);
    const [modelName, setModelName] = useState<string | null>(null);
    const [keyID, setKeyID] = useState<number | null>(null);

    // 可用渠道：只列出至少有一个可用授权的渠道 —— 列出一堆建不出分组的渠道没有意义。
    const channels = useMemo(() => {
        const seen = new Map<number, string>();
        for (const g of grants ?? []) {
            if (!g.available) continue;
            if (!seen.has(g.channel_id)) seen.set(g.channel_id, g.channel_name);
        }
        return [...seen.entries()]
            .map(([id, name]) => ({ id, name }))
            .sort((a, b) => a.name.localeCompare(b.name));
    }, [grants]);

    // 选中渠道下的模型 → 该模型下可用凭据。
    const models = useMemo(() => {
        if (channelID === null) return [];
        const byModel = new Map<string, { keyID: number; keyName: string }[]>();
        for (const g of grants ?? []) {
            if (g.channel_id !== channelID || !g.available) continue;
            const list = byModel.get(g.model_name) ?? [];
            list.push({ keyID: g.key_id, keyName: g.key_name });
            byModel.set(g.model_name, list);
        }
        return [...byModel.entries()]
            .map(([name, keys]) => ({ name, keys: keys.sort((a, b) => a.keyName.localeCompare(b.keyName)) }))
            .sort((a, b) => a.name.localeCompare(b.name));
    }, [grants, channelID]);

    const selectedChannel = channels.find((c) => c.id === channelID);
    const selectedModel = models.find((m) => m.name === modelName);
    const previewName = selectedChannel && modelName ? `${selectedChannel.name}/${modelName}` : '';

    const submit = () => {
        if (channelID === null || !modelName) return;
        quickCreate.mutate(
            { channel_id: channelID, model_name: modelName, key_id: keyID ?? undefined },
            {
                onSuccess: (result) => {
                    // 复用与新建要分开说 —— 否则用户点了两次以为建了两个组。
                    toast.success(t(result.reused ? 'quickCreate.reused' : 'quickCreate.created'), {
                        description: result.group.name,
                    });
                    setIsOpen(false);
                },
                onError: (error) => toast.error(t('quickCreate.failed'), { description: error.message }),
            }
        );
    };

    return (
        <div className="w-screen max-w-full md:max-w-3xl max-h-[calc(100vh-2rem)] min-h-0 flex flex-col">
            <div className="flex items-center gap-2 border-b px-4 py-3">
                <Layers className="h-4 w-4 text-muted-foreground" />
                <MorphingDialogTitle className="text-sm font-medium">
                    {t('quickCreate.title')}
                </MorphingDialogTitle>
                <MorphingDialogClose className="ml-auto" />
            </div>

            <MorphingDialogDescription className="min-h-0 flex-1 overflow-y-auto px-4 py-3 text-xs text-muted-foreground">
                {t('quickCreate.hint')}
            </MorphingDialogDescription>

            <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-3">
                {isLoading ? (
                    <div className="flex items-center justify-center py-12 text-muted-foreground">
                        <Loader2 className="h-4 w-4 animate-spin" />
                    </div>
                ) : channels.length === 0 ? (
                    <p className="py-12 text-center text-sm text-muted-foreground">
                        {t('quickCreate.noChannels')}
                    </p>
                ) : (
                    <div className="grid gap-4 md:grid-cols-2">
                        <section>
                            <h3 className="mb-2 text-xs font-medium">{t('quickCreate.stepChannel')}</h3>
                            <ul className="max-h-64 space-y-1 overflow-y-auto pr-1">
                                {channels.map((c) => (
                                    <li key={c.id}>
                                        <button
                                            type="button"
                                            onClick={() => {
                                                setChannelID(c.id);
                                                // 换渠道必须清掉模型与凭据 —— 留着上一个渠道的选择
                                                // 会建出 `新渠道/旧模型` 这种不存在的组合。
                                                setModelName(null);
                                                setKeyID(null);
                                            }}
                                            className={`w-full rounded-md px-2 py-1.5 text-left text-xs transition-colors ${
                                                channelID === c.id
                                                    ? 'bg-primary text-primary-foreground'
                                                    : 'hover:bg-muted'
                                            }`}
                                        >
                                            {c.name}
                                        </button>
                                    </li>
                                ))}
                            </ul>
                        </section>

                        <section>
                            <h3 className="mb-2 text-xs font-medium">{t('quickCreate.stepModel')}</h3>
                            {channelID === null ? (
                                <p className="py-4 text-center text-xs text-muted-foreground">
                                    {t('quickCreate.pickChannelFirst')}
                                </p>
                            ) : (
                                <ul className="max-h-64 space-y-1 overflow-y-auto pr-1">
                                    {models.map((m) => (
                                        <li key={m.name}>
                                            <button
                                                type="button"
                                                onClick={() => {
                                                    setModelName(m.name);
                                                    setKeyID(null);
                                                }}
                                                className={`w-full rounded-md px-2 py-1.5 text-left text-xs transition-colors ${
                                                    modelName === m.name
                                                        ? 'bg-primary text-primary-foreground'
                                                        : 'hover:bg-muted'
                                                }`}
                                            >
                                                {m.name}
                                            </button>
                                        </li>
                                    ))}
                                </ul>
                            )}
                        </section>
                    </div>
                )}

                {selectedModel && (
                    <section className="mt-4 border-t pt-3">
                        <h3 className="mb-2 text-xs font-medium">{t('quickCreate.stepKey')}</h3>
                        <div className="flex flex-wrap gap-1">
                            <button
                                type="button"
                                onClick={() => setKeyID(null)}
                                className={`rounded-md border px-2 py-1 text-xs transition-colors ${
                                    keyID === null ? 'border-primary bg-primary text-primary-foreground' : 'hover:bg-muted'
                                }`}
                            >
                                {t('quickCreate.allKeys', { count: selectedModel.keys.length })}
                            </button>
                            {selectedModel.keys.map((k) => (
                                <button
                                    key={`${k.keyID}-${k.keyName}`}
                                    type="button"
                                    onClick={() => setKeyID(k.keyID)}
                                    className={`rounded-md border px-2 py-1 text-xs transition-colors ${
                                        keyID === k.keyID
                                            ? 'border-primary bg-primary text-primary-foreground'
                                            : 'hover:bg-muted'
                                    }`}
                                >
                                    {k.keyName}
                                </button>
                            ))}
                        </div>
                        <p className="mt-2 text-xs text-muted-foreground">
                            {t('quickCreate.keyHint')}
                        </p>
                    </section>
                )}
            </div>

            <div className="flex items-center gap-2 border-t px-4 py-3">
                {/* 组名预览：用户点下去之前就能看到会建出什么名字，不用回头去列表里找。 */}
                {previewName && (
                    <span className="truncate font-mono text-xs text-muted-foreground">{previewName}</span>
                )}
                <Button
                    className="ml-auto"
                    size="sm"
                    disabled={channelID === null || !modelName || quickCreate.isPending}
                    onClick={submit}
                >
                    {quickCreate.isPending ? (
                        <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    ) : (
                        <Check className="h-3.5 w-3.5" />
                    )}
                    {t('quickCreate.submit')}
                </Button>
            </div>
        </div>
    );
}