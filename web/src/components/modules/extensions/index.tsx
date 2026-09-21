import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import {
    Boxes,
    ExternalLink,
    KeyRound,
    Layers,
    LogIn,
    Play,
    RefreshCw,
    ScanSearch,
    Sparkles,
    Square,
    Trash2,
    Wand2,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { proxyNodesQueryOptions } from '@/api/proxy';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import {
    clearCollectorSession,
    createCredentialSource,
    credentialSourcesQueryOptions,
    finishCollectorLogin,
    pluginsQueryOptions,
    poolAdaptersQueryOptions,
    poolTemplatesQueryOptions,
    registerPoolAdapter,
    removeCredentialSource,
    removePlugin,
    removePoolAdapter,
    renderSiteTemplate,
    runCredentialSource,
    scanPlugins,
    siteTemplatesQueryOptions,
    startCollectorLogin,
    startPlugin,
    stopPlugin,
    updateCredentialSource,
    updatePlugin,
    type CredentialSource,
} from '@/api/extensions';

// R-ext-001 「扩展」页：octopus 里的"软件内的软件"。
//
// 三类扩展（插件托管 / 账号级采集凭据 / 号池声明式适配器）各自已有完整后端契约；
// 这一页把它们摆到可点的位置，并且**按用户的排查顺序**排列子菜单：
//   插件      —— 我要接一个自己的反代工具进来
//   采集凭据  —— 我要看到某家中转站的余额（登录一次或装一个包）
//   号池适配器 —— 我要让号池认识一个新的账号池形态
//
// 子菜单是页内一级（不是三个一级菜单）：这三件事常互为因果（插件要出口、采集要反代、
// 适配器要主机白名单），分开会让人来回跳。
type Section = 'plugins' | 'collectors' | 'adapters';

const SECTION_ICONS = { plugins: Boxes, collectors: KeyRound, adapters: Layers } as const;

export function Extensions() {
    const t = useTranslations('extensions');
    const [section, setSection] = useState<Section>('plugins');
    const sections: Section[] = ['plugins', 'collectors', 'adapters'];

    return (
        <div className="flex h-full min-h-0 gap-4">
            <nav className="w-44 shrink-0 space-y-1">
                <p className="px-2 pb-1 text-xs font-medium text-muted-foreground">{t('submenu')}</p>
                {sections.map((item) => {
                    const Icon = SECTION_ICONS[item];
                    const active = section === item;
                    return (
                        <button
                            key={item}
                            type="button"
                            onClick={() => setSection(item)}
                            className={
                                'flex w-full items-center gap-2 rounded-md px-2 py-2 text-left text-sm transition-colors ' +
                                (active ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent/50')
                            }
                        >
                            <Icon className="h-4 w-4" />
                            <span>{t(`menu.${item}`)}</span>
                        </button>
                    );
                })}
            </nav>
            <div className="min-h-0 flex-1 overflow-auto pr-1">
                {section === 'plugins' && <PluginsPanel />}
                {section === 'collectors' && <CollectorsPanel />}
                {section === 'adapters' && <AdaptersPanel />}
            </div>
        </div>
    );
}

function PluginsPanel() {
    const t = useTranslations('extensions');
    const queryClient = useQueryClient();
    const pluginsQuery = useQuery(pluginsQueryOptions);
    const invalidate = () => queryClient.invalidateQueries({ queryKey: ['extensions-plugins'] });

    const scan = useMutation({ mutationFn: scanPlugins, onSuccess: (data) => { invalidate(); toast.success(t('plugins.scanDone', { count: data.total ?? (data.items?.length ?? 0) })); } });
    const start = useMutation({ mutationFn: startPlugin, onSuccess: () => { invalidate(); toast.success(t('plugins.started')); }, onError: (error: Error) => toast.error(error.message) });
    const stop = useMutation({ mutationFn: stopPlugin, onSuccess: () => { invalidate(); toast.success(t('plugins.stopped')); } });
    const remove = useMutation({ mutationFn: removePlugin, onSuccess: () => { invalidate(); toast.success(t('common.removed')); } });
    const update = useMutation({ mutationFn: (input: { slug: string; body: { enabled?: boolean; auto_start?: boolean } }) => updatePlugin(input.slug, input.body), onSuccess: invalidate });

    const items = pluginsQuery.data?.items ?? [];
    return (
        <div className="space-y-4">
            <header className="flex flex-wrap items-center justify-between gap-2">
                <div>
                    <h2 className="text-base font-semibold">{t('plugins.title')}</h2>
                    <p className="text-xs text-muted-foreground">{t('plugins.hint')}</p>
                    <p className="text-xs text-muted-foreground">{t('plugins.dir')}: {pluginsQuery.data?.dir ?? '-'}</p>
                </div>
                <Button size="sm" variant="outline" onClick={() => scan.mutate()} disabled={scan.isPending}>
                    <ScanSearch className="mr-1 h-4 w-4" />
                    {t('plugins.scan')}
                </Button>
            </header>
            {items.length === 0 && <p className="text-sm text-muted-foreground">{t('plugins.empty')}</p>}
            {items.map((plugin) => (
                <div key={plugin.slug} className="space-y-2 rounded-lg border p-3">
                    <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{plugin.name || plugin.slug}</span>
                        <Badge variant={plugin.running ? 'default' : 'secondary'}>{plugin.running ? t('plugins.stateRunning') : plugin.status || t('plugins.stateStopped')}</Badge>
                        <Badge variant="outline">{plugin.runtime}</Badge>
                        {plugin.auto_channel && <Badge variant="outline">{t('plugins.autoChannel')}</Badge>}
                        <span className="text-xs text-muted-foreground">{plugin.protocol}</span>
                        <div className="ml-auto flex items-center gap-2">
                            <Button size="sm" variant={plugin.running ? 'outline' : 'default'} onClick={() => (plugin.running ? stop.mutate(plugin.slug) : start.mutate(plugin.slug))}>
                                {plugin.running ? <Square className="mr-1 h-3.5 w-3.5" /> : <Play className="mr-1 h-3.5 w-3.5" />}
                                {plugin.running ? t('plugins.stop') : t('plugins.start')}
                            </Button>
                            <Button size="sm" variant="ghost" onClick={() => remove.mutate(plugin.slug)}>
                                <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                        </div>
                    </div>
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-muted-foreground md:grid-cols-3">
                        <span>{t('plugins.port')}: {plugin.port || '-'}</span>
                        <span>{t('plugins.endpoint')}: {plugin.endpoint || '-'}</span>
                        <span>{t('plugins.channel')}: {plugin.channel_id || '-'}</span>
                        <span className="truncate">{t('plugins.egress')}: {plugin.egress_proxy || plugin.egress_note || t('plugins.noEgress')}</span>
                        <span>{t('plugins.pid')}: {plugin.pid || '-'}</span>
                    </div>
                    <div className="flex items-center gap-6">
                        <label className="flex items-center gap-2 text-xs">
                            <Switch checked={plugin.enabled} onCheckedChange={(checked) => update.mutate({ slug: plugin.slug, body: { enabled: checked } })} />
                            {t('common.enabled')}
                        </label>
                        <label className="flex items-center gap-2 text-xs">
                            <Switch checked={plugin.auto_start} onCheckedChange={(checked) => update.mutate({ slug: plugin.slug, body: { auto_start: checked } })} />
                            {t('plugins.autoStart')}
                        </label>
                    </div>
                    {(plugin.last_error || plugin.log_tail) && (
                        <pre className="max-h-24 overflow-auto rounded bg-muted p-2 text-[11px] leading-relaxed">
{plugin.last_error ? plugin.last_error + '\n' : ''}{plugin.log_tail || ''}
                        </pre>
                    )}
                </div>
            ))}
        </div>
    );
}

// TemplatePicker：把"输入账号密码登录站点看余额"降成"选类型 + 填网址"。
//
// 内置四类站点（new-api / one-api / sub2api / litellm）的登录与查账端点是业界惯例，
// 让人手抄一遍只会抄错 —— 这里由后端生成一份**已过装载门禁**的采集包填进表单，人只需要核对。
// 两种形态的区别是真实存在的：站点挂了验证码/Turnstile 时 form 模式必然登录失败，
// 此时选"反代登录页"，人在 octopus 托管的页面里输验证码，会话由服务端捕获。
function TemplatePicker({ onApply }: { onApply: (applied: { name: string; kind: string; site: string; pack: string }) => void }) {
    const t = useTranslations('extensions');
    const templatesQuery = useQuery(siteTemplatesQueryOptions);
    const [kind, setKind] = useState('new-api');
    const [mode, setMode] = useState('form');
    const [baseURL, setBaseURL] = useState('');
    const render = useMutation({
        mutationFn: () => renderSiteTemplate({ kind, base_url: baseURL, mode }),
        onSuccess: (data) => {
            const chosen = (templatesQuery.data?.items ?? []).find((item) => item.kind === kind && item.mode === mode);
            onApply({
                name: chosen?.title || kind,
                // 反代登录的源用 login 形态（面板会给"开始登录/完成登录"），form 的用采集包形态。
                kind: mode === 'interactive' ? 'login' : 'pack',
                site: baseURL,
                pack: data.pack,
            });
        },
        onError: (error: Error) => toast.error(error.message),
    });
    const kinds = useMemo(() => {
        const seen = new Map<string, string>();
        for (const item of templatesQuery.data?.items ?? []) {
            if (!seen.has(item.kind)) seen.set(item.kind, item.title);
        }
        return Array.from(seen.entries());
    }, [templatesQuery.data]);
    const current = (templatesQuery.data?.items ?? []).find((item) => item.kind === kind && item.mode === mode);

    return (
        <div className="space-y-2 rounded-lg border border-dashed p-3">
            <p className="text-sm font-medium">{t('collectors.fromTemplate')}</p>
            <p className="text-xs text-muted-foreground">{t('collectors.fromTemplateHint')}</p>
            <div className="grid gap-2 md:grid-cols-3">
                <div className="space-y-1">
                    <Label className="text-xs">{t('collectors.siteKind')}</Label>
                    <select className="h-9 w-full rounded-md border bg-transparent px-2 text-sm" value={kind} onChange={(event) => setKind(event.target.value)}>
                        {kinds.map(([value, title]) => (
                            <option key={value} value={value}>{title} ({value})</option>
                        ))}
                    </select>
                </div>
                <div className="space-y-1">
                    <Label className="text-xs">{t('collectors.loginMode')}</Label>
                    <select className="h-9 w-full rounded-md border bg-transparent px-2 text-sm" value={mode} onChange={(event) => setMode(event.target.value)}>
                        <option value="form">{t('collectors.modeForm')}</option>
                        <option value="interactive">{t('collectors.modeInteractive')}</option>
                    </select>
                </div>
                <div className="space-y-1">
                    <Label className="text-xs">{t('collectors.site')}</Label>
                    <Input value={baseURL} onChange={(event) => setBaseURL(event.target.value)} placeholder="https://relay.example.com" />
                </div>
            </div>
            {current && (
                <p className="text-xs text-muted-foreground">
                    {current.note}
                    <span className="ml-1">{current.paths.join(' · ')}</span>
                    {current.need_captcha && <span className="ml-1 font-medium">{t('collectors.captchaHint')}</span>}
                </p>
            )}
            <Button size="sm" variant="outline" onClick={() => render.mutate()} disabled={render.isPending || baseURL.trim() === ''}>
                <Sparkles className="mr-1 h-4 w-4" />
                {t('collectors.applyTemplate')}
            </Button>
        </div>
    );
}

function CollectorsPanel() {
    const t = useTranslations('extensions');
    const queryClient = useQueryClient();
    const sourcesQuery = useQuery(credentialSourcesQueryOptions);
    // 出口节点：这里列出来的节点是"采集与反代登录从哪台机器出去"。
    // 不选 = 从本机真实 IP 发出，站点看得到你 —— 所以选项里明确写出来。
    const nodesQuery = useQuery(proxyNodesQueryOptions);
    const readyNodes = (nodesQuery.data?.items ?? []).filter((item) => item.enabled && item.local_port > 0);
    const invalidate = () => queryClient.invalidateQueries({ queryKey: ['extensions-collector-sources'] });
    const [form, setForm] = useState({ name: '', kind: 'login', site: '', channel_id: '', pack: '', username: '', password: '', proxy_node_id: '' });
    const [revealed, setRevealed] = useState<number | null>(null);

    const create = useMutation({
        mutationFn: () => createCredentialSource({
            name: form.name,
            kind: form.kind,
            site: form.site,
            channel_id: form.channel_id ? Number(form.channel_id) : 0,
            pack: form.pack,
            proxy_node_id: form.proxy_node_id ? Number(form.proxy_node_id) : 0,
            username: form.username,
            password: form.password,
        }),
        onSuccess: () => { invalidate(); toast.success(t('collectors.created')); },
        onError: (error: Error) => toast.error(error.message),
    });
    const run = useMutation({
        mutationFn: runCredentialSource,
        onSuccess: (data) => { invalidate(); data.ok ? toast.success(t('collectors.runOK', { value: data.item?.last_balance ?? 0 })) : toast.error(data.message || t('collectors.runFail')); },
        onError: (error: Error) => toast.error(error.message),
    });
    const startLogin = useMutation({
        mutationFn: startCollectorLogin,
        onSuccess: (data) => { invalidate(); setRevealed(data.item.id); toast.success(t('collectors.loginReady')); },
        onError: (error: Error) => toast.error(error.message),
    });
    const finishLogin = useMutation({
        mutationFn: finishCollectorLogin,
        onSuccess: () => { invalidate(); toast.success(t('collectors.loginDone')); },
        onError: (error: Error) => toast.error(error.message),
    });
    const clearSession = useMutation({ mutationFn: clearCollectorSession, onSuccess: () => { invalidate(); toast.success(t('collectors.sessionCleared')); } });
    const remove = useMutation({ mutationFn: removeCredentialSource, onSuccess: () => { invalidate(); toast.success(t('common.removed')); } });
    const toggle = useMutation({
        mutationFn: (input: { id: number; body: { enabled?: boolean; auto_refresh?: boolean; channel_id?: number; proxy_node_id?: number } }) => updateCredentialSource(input.id, input.body),
        onSuccess: invalidate,
    });

    const items = sourcesQuery.data?.items ?? [];
    return (
        <div className="space-y-4">
            <header>
                <h2 className="text-base font-semibold">{t('collectors.title')}</h2>
                <p className="text-xs text-muted-foreground">{t('collectors.hint')}</p>
            </header>

            <TemplatePicker
                onApply={(applied) => {
                    setForm({ ...form, ...applied, name: form.name || applied.name });
                    toast.success(t('collectors.templateApplied'));
                }}
            />

            <div className="space-y-3 rounded-lg border p-3">
                <p className="text-sm font-medium">{t('collectors.new')}</p>
                <div className="grid gap-2 md:grid-cols-2">
                    <div className="space-y-1">
                        <Label className="text-xs">{t('collectors.name')}</Label>
                        <Input value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} placeholder={t('collectors.nameHint')} />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t('collectors.kind')}</Label>
                        <select
                            className="h-9 w-full rounded-md border bg-transparent px-2 text-sm"
                            value={form.kind}
                            onChange={(event) => setForm({ ...form, kind: event.target.value })}
                        >
                            <option value="login">{t('collectors.kindLogin')}</option>
                            <option value="pack">{t('collectors.kindPack')}</option>
                        </select>
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t('collectors.site')}</Label>
                        <Input value={form.site} onChange={(event) => setForm({ ...form, site: event.target.value })} placeholder={t('collectors.siteHint')} />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t('collectors.channelId')}</Label>
                        <Input value={form.channel_id} onChange={(event) => setForm({ ...form, channel_id: event.target.value })} placeholder="0" />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t('collectors.proxyNode')}</Label>
                        <select
                            className="h-9 w-full rounded-md border bg-transparent px-2 text-sm"
                            value={form.proxy_node_id}
                            onChange={(event) => setForm({ ...form, proxy_node_id: event.target.value })}
                        >
                            <option value="">{t('collectors.proxyNodeDirect')}</option>
                            {readyNodes.map((item) => (
                                <option key={item.id} value={item.id}>
                                    {item.name} · {item.local_port}
                                </option>
                            ))}
                        </select>
                        <p className="text-[11px] text-muted-foreground">{t('collectors.proxyNodeHint')}</p>
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t('collectors.username')}</Label>
                        <Input value={form.username} onChange={(event) => setForm({ ...form, username: event.target.value })} />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t('collectors.password')}</Label>
                        <Input type="password" value={form.password} onChange={(event) => setForm({ ...form, password: event.target.value })} placeholder={t('collectors.passwordHint')} />
                    </div>
                </div>
                <div className="space-y-1">
                    <Label className="text-xs">{t('collectors.packText')}</Label>
                    <textarea
                        className="min-h-24 w-full rounded-md border bg-transparent p-2 font-mono text-xs"
                        value={form.pack}
                        onChange={(event) => setForm({ ...form, pack: event.target.value })}
                        placeholder={t('collectors.packHint')}
                    />
                </div>
                <Button size="sm" onClick={() => create.mutate()} disabled={create.isPending || !form.name}>
                    <Wand2 className="mr-1 h-4 w-4" />
                    {t('common.create')}
                </Button>
            </div>

            {items.length === 0 && <p className="text-sm text-muted-foreground">{t('collectors.empty')}</p>}
            {items.map((source: CredentialSource) => (
                <div key={source.id} className="space-y-2 rounded-lg border p-3">
                    <div className="flex flex-wrap items-center gap-2">
                        <span className="font-medium">{source.name}</span>
                        <Badge variant="outline">{source.kind === 'login' ? t('collectors.kindLogin') : t('collectors.kindPack')}</Badge>
                        <Badge variant={source.status === 'authorized' ? 'default' : source.status === 'failed' ? 'destructive' : 'secondary'}>
                            {t(`collectors.status.${source.status}`)}
                        </Badge>
                        <span className="text-xs text-muted-foreground">{source.hosts || source.site}</span>
                        <div className="ml-auto flex flex-wrap items-center gap-2">
                            <Button size="sm" variant="outline" onClick={() => run.mutate(source.id)} disabled={run.isPending}>
                                <RefreshCw className="mr-1 h-3.5 w-3.5" />
                                {t('collectors.runNow')}
                            </Button>
                            {source.kind === 'login' && (
                                <>
                                    <Button size="sm" variant="outline" onClick={() => startLogin.mutate(source.id)}>
                                        <LogIn className="mr-1 h-3.5 w-3.5" />
                                        {t('collectors.startLogin')}
                                    </Button>
                                    <Button size="sm" variant="outline" onClick={() => finishLogin.mutate(source.id)}>
                                        {t('collectors.finishLogin')}
                                    </Button>
                                    <Button size="sm" variant="ghost" onClick={() => clearSession.mutate(source.id)}>
                                        {t('collectors.clearSession')}
                                    </Button>
                                </>
                            )}
                            <Button size="sm" variant="ghost" onClick={() => remove.mutate(source.id)}>
                                <Trash2 className="h-3.5 w-3.5" />
                            </Button>
                        </div>
                    </div>
                    <div className="grid grid-cols-2 gap-x-4 gap-y-1 text-xs text-muted-foreground md:grid-cols-3">
                        <span>{t('collectors.lastBalance')}: {source.last_balance || 0} {source.last_currency || (source.units === 'points' ? t('collectors.points') : 'USD')}</span>
                        <span>{t('collectors.lastRead')}: {source.last_read_at || '-'}</span>
                        <span>{t('collectors.channelId')}: {source.channel_id || '-'}</span>
                        <span>{t('collectors.credentials')}: {source.has_credentials ? t('common.yes') : t('common.no')}</span>
                        <span>{t('collectors.session')}: {source.has_session ? t('common.yes') : t('common.no')}</span>
                        <span>{t('collectors.units')}: {source.units}</span>
                        <span>
                            {t('collectors.egress')}: {readyNodes.find((item) => item.id === source.proxy_node_id)?.name || t('collectors.proxyNodeDirect')}
                        </span>
                        <span className="flex items-center gap-1">
                            <select
                                className="rounded border bg-transparent px-1 text-[11px]"
                                value={source.proxy_node_id || ''}
                                onChange={(event) => toggle.mutate({ id: source.id, body: { proxy_node_id: Number(event.target.value || 0) } })}
                            >
                                <option value="">{t('collectors.proxyNodeDirect')}</option>
                                {readyNodes.map((item) => (
                                    <option key={item.id} value={item.id}>
                                        {item.name}
                                    </option>
                                ))}
                            </select>
                        </span>
                    </div>
                    {source.last_error && <p className="text-xs text-destructive">{t('collectors.lastError')}: {source.last_error}</p>}
                    {source.kind === 'login' && revealed === source.id && source.login_url && (
                        <p className="break-all text-xs">
                            {t('collectors.loginURL')}:{' '}
                            <a className="underline" href={source.login_url} target="_blank" rel="noreferrer">
                                {source.login_url} <ExternalLink className="inline h-3 w-3" />
                            </a>
                            <span className="ml-1 text-muted-foreground">{t('collectors.loginTip')}</span>
                        </p>
                    )}
                    <div className="flex items-center gap-6">
                        <label className="flex items-center gap-2 text-xs">
                            <Switch checked={source.enabled} onCheckedChange={(checked) => toggle.mutate({ id: source.id, body: { enabled: checked } })} />
                            {t('common.enabled')}
                        </label>
                        <label className="flex items-center gap-2 text-xs">
                            <Switch checked={source.auto_refresh} onCheckedChange={(checked) => toggle.mutate({ id: source.id, body: { auto_refresh: checked } })} />
                            {t('collectors.autoRefresh')}
                        </label>
                    </div>
                    {(source.steps?.length ?? 0) > 0 && (
                        <pre className="max-h-24 overflow-auto rounded bg-muted p-2 text-[11px] leading-relaxed">{(source.steps || []).join('\n')}</pre>
                    )}
                </div>
            ))}
        </div>
    );
}

function AdaptersPanel() {
    const t = useTranslations('extensions');
    const queryClient = useQueryClient();
    const adaptersQuery = useQuery(poolAdaptersQueryOptions);
    const templatesQuery = useQuery(poolTemplatesQueryOptions);
    const invalidate = () => queryClient.invalidateQueries({ queryKey: ['extensions-pool-adapters'] });
    const [spec, setSpec] = useState('');
    const [kind, setKind] = useState('');
    const register = useMutation({
        mutationFn: (payload: string) => registerPoolAdapter(payload),
        onSuccess: () => { invalidate(); toast.success(t('adapters.registered')); setSpec(''); setKind(''); },
        onError: (error: Error) => toast.error(error.message),
    });
    const remove = useMutation({ mutationFn: removePoolAdapter, onSuccess: () => { invalidate(); toast.success(t('common.removed')); } });

    const adapters = adaptersQuery.data?.items ?? [];
    const templates = templatesQuery.data?.items ?? [];
    return (
        <div className="space-y-4">
            <header>
                <h2 className="text-base font-semibold">{t('adapters.title')}</h2>
                <p className="text-xs text-muted-foreground">{t('adapters.hint')}</p>
            </header>

            <div className="grid gap-2 rounded-lg border p-3 text-xs md:grid-cols-3">
                <span>{t('adapters.hosts')}: {adaptersQuery.data?.hosts || t('adapters.hostsEmpty')}</span>
                <span>{t('adapters.readOnly')}: {adaptersQuery.data?.read_only_capable ? t('common.yes') : t('common.no')}</span>
                <span>{t('adapters.ops')}: {(adaptersQuery.data?.declarative_only_ops || []).join(', ') || '-'}</span>
            </div>

            <div className="space-y-3 rounded-lg border p-3">
                <p className="text-sm font-medium">{t('adapters.register')}</p>
                <div className="grid gap-2 md:grid-cols-2">
                    <div className="space-y-1">
                        <Label className="text-xs">{t('adapters.kind')}</Label>
                        <Input value={kind} onChange={(event) => setKind(event.target.value)} placeholder="custom-my-site" />
                    </div>
                    <div className="space-y-1">
                        <Label className="text-xs">{t('adapters.templates')}</Label>
                        <select
                            className="h-9 w-full rounded-md border bg-transparent px-2 text-sm"
                            value=""
                            onChange={(event) => {
                                const chosen = templates.find((item) => item.kind === event.target.value);
                                if (chosen) {
                                    setKind(chosen.kind);
                                    setSpec(JSON.stringify(chosen.spec, null, 2));
                                }
                            }}
                        >
                            <option value="">{t('adapters.useTemplate')}</option>
                            {templates.map((item) => (
                                <option key={item.kind} value={item.kind}>{item.title || item.kind}</option>
                            ))}
                        </select>
                    </div>
                </div>
                <textarea
                    className="min-h-32 w-full rounded-md border bg-transparent p-2 font-mono text-xs"
                    value={spec}
                    onChange={(event) => setSpec(event.target.value)}
                    placeholder={t('adapters.registerHint')}
                />
                <Button size="sm" onClick={() => register.mutate(spec)} disabled={register.isPending || spec.trim() === ''}>
                    <Wand2 className="mr-1 h-4 w-4" />
                    {t('adapters.registerAction')}
                </Button>
            </div>

            {adapters.length === 0 && <p className="text-sm text-muted-foreground">{t('adapters.empty')}</p>}
            {adapters.map((adapter) => (
                <div key={adapter.kind} className="flex flex-wrap items-center gap-2 rounded-lg border p-3">
                    <span className="font-medium">{adapter.title || adapter.kind}</span>
                    <Badge variant="outline">{adapter.kind}</Badge>
                    {(adapter.capabilities || []).map((capability) => (
                        <Badge key={capability} variant="secondary">{capability}</Badge>
                    ))}
                    <span className="text-xs text-muted-foreground">{adapter.base_url}</span>
                    <Button className="ml-auto" size="sm" variant="ghost" onClick={() => remove.mutate(adapter.kind)}>
                        <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                </div>
            ))}

            <div className="space-y-1 rounded-lg border p-3 text-xs text-muted-foreground">
                <p className="font-medium text-foreground">{t('adapters.apiDoc')}</p>
                <p>/api/v1/pool/openapi.json · /api/v1/pool/kinds · /api/v1/pool/entries · /api/v1/pool/summary</p>
                <p>{t('adapters.relayDoc')}: /v1/pool/kinds · /v1/pool/entries · /v1/pool/stats · /v1/pool/summary</p>
            </div>
        </div>
    );
}
