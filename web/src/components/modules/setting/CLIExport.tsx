import { useState } from 'react';
import { useTranslations } from 'use-intl';
import { Copy, Terminal } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import { useAPIKeyList } from '@/api/apikey';
import { useGroupList } from '@/api/group';
import { useGenerateCLIExport, type CLITarget, type CLIExport } from '@/api/cli-export';
import { toast } from 'sonner';

const TARGETS: CLITarget[] = ['claude_code', 'codex', 'gemini_cli', 'cherry_studio', 'openai_compatible'];

/**
 * CLI 配置导出面板。
 *
 * 三样东西决定客户端能不能连上：转发口地址、网关 Key、分组名。
 * 面板把它们放在一起并要求**逐项确认**——网关无法可靠地推断自己的公网地址
 * （反代、端口映射都会改变它），猜错会让人配出一个连不上的客户端，
 * 所以地址是必填且由用户确认，而不是悄悄塞一个默认值。
 */
export function SettingCLIExport() {
    const t = useTranslations('setting');
    const { data: apiKeys } = useAPIKeyList();
    const { data: groups } = useGroupList();
    const generate = useGenerateCLIExport();

    const [target, setTarget] = useState<CLITarget>('claude_code');
    const [baseUrl, setBaseUrl] = useState('');
    const [keyId, setKeyId] = useState('');
    const [model, setModel] = useState('');
    const [result, setResult] = useState<CLIExport | null>(null);

    const run = () => {
        if (!baseUrl.trim()) {
            toast.error(t('cliExport.baseUrlRequired'));
            return;
        }
        if (!model.trim()) {
            toast.error(t('cliExport.modelRequired'));
            return;
        }
        if (!keyId) {
            toast.error(t('cliExport.keyRequired'));
            return;
        }
        generate.mutate(
            { tool: target, base_url: baseUrl.trim(), model: model.trim(), api_key_id: Number(keyId) },
            {
                onSuccess: (exported) => setResult(exported),
                onError: (error) => toast.error(String(error)),
            },
        );
    };

    const copy = async (text: string) => {
        try {
            await navigator.clipboard.writeText(text);
            toast.success(t('cliExport.copied'));
        } catch {
            toast.error(t('cliExport.copyFailed'));
        }
    };

    return (
        <div className="rounded-3xl border border-border bg-card p-6 space-y-5">
            <h2 className="text-lg font-bold text-card-foreground flex items-center gap-2">
                <Terminal className="size-4" />
                {t('cliExport.title')}
            </h2>
            <p className="text-sm text-muted-foreground leading-relaxed">{t('cliExport.description')}</p>

            <div className="rounded-2xl border border-border/50 bg-muted/20 p-3 space-y-3">
                <div className="grid gap-2 md:grid-cols-2">
                    <label className="space-y-1 text-xs text-muted-foreground">
                        <span>{t('cliExport.target')}</span>
                        <Select value={target} onValueChange={(v) => setTarget(v as CLITarget)}>
                            <SelectTrigger>
                                <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                                {TARGETS.map((item) => (
                                    <SelectItem key={item} value={item}>
                                        {t(`cliExport.targets.${item}`)}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </label>
                    <label className="space-y-1 text-xs text-muted-foreground">
                        <span>{t('cliExport.key')}</span>
                        <Select value={keyId} onValueChange={setKeyId}>
                            <SelectTrigger>
                                <SelectValue placeholder={t('cliExport.keyPlaceholder')} />
                            </SelectTrigger>
                            <SelectContent>
                                {(apiKeys ?? []).map((item) => (
                                    <SelectItem key={item.id} value={String(item.id)}>
                                        {item.name}
                                    </SelectItem>
                                ))}
                            </SelectContent>
                        </Select>
                    </label>
                    <label className="space-y-1 text-xs text-muted-foreground md:col-span-2">
                        <span>{t('cliExport.baseUrl')}</span>
                        <Input
                            placeholder="https://your-gateway.example.com"
                            value={baseUrl}
                            onChange={(e) => setBaseUrl(e.target.value)}
                        />
                    </label>
                    <label className="space-y-1 text-xs text-muted-foreground md:col-span-2">
                        <span>{t('cliExport.model')}</span>
                        <Input
                            placeholder={t('cliExport.modelPlaceholder')}
                            value={model}
                            onChange={(e) => setModel(e.target.value)}
                        />
                        {groups && groups.length > 0 && (
                            <span className="block text-[11px] text-muted-foreground">
                                {t('cliExport.knownGroups')}: {groups.slice(0, 6).map((g) => g.name).join('、')}
                                {groups.length > 6 ? ' …' : ''}
                            </span>
                        )}
                    </label>
                </div>
                <div className="flex items-center justify-between">
                    <p className="text-xs text-muted-foreground">{t('cliExport.hint')}</p>
                    <Button type="button" onClick={run} disabled={generate.isPending}>
                        {t('cliExport.generate')}
                    </Button>
                </div>
            </div>

            {result && (
                <div className="space-y-3 rounded-2xl border border-border/50 p-3">
                    <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm font-medium">{result.title}</span>
                        {result.filename && (
                            <span className="font-mono text-xs text-muted-foreground">{result.filename}</span>
                        )}
                        <Button
                            type="button"
                            variant="ghost"
                            size="sm"
                            className="ml-auto"
                            onClick={() => copy(result.content)}
                        >
                            <Copy className="size-3.5" />
                            {t('cliExport.copy')}
                        </Button>
                    </div>
                    <p className="text-xs text-muted-foreground">{result.description}</p>
                    <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded-xl bg-muted/30 p-3 text-xs leading-relaxed">
                        {result.content}
                    </pre>
                    {result.steps && result.steps.length > 0 && (
                        <div className="space-y-1">
                            <div className="text-xs font-medium">{t('cliExport.steps')}</div>
                            {result.steps.map((step, index) => (
                                <div key={index} className="text-xs text-muted-foreground">
                                    {index + 1}. {step}
                                </div>
                            ))}
                        </div>
                    )}
                    {result.notes && result.notes.length > 0 && (
                        <div className="space-y-1">
                            <div className="text-xs font-medium">{t('cliExport.notes')}</div>
                            {result.notes.map((note, index) => (
                                <div key={index} className="text-xs text-muted-foreground">
                                    · {note}
                                </div>
                            ))}
                        </div>
                    )}
                </div>
            )}
        </div>
    );
}
