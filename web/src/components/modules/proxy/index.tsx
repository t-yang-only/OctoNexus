import { useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslations } from 'use-intl';
import { toast } from 'sonner';
import { Activity, Download, Globe, Import, Plus, RefreshCw, Server, Zap } from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Switch } from '@/components/ui/switch';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { IconButton } from '@/components/common/IconButton';
import {
    allocateProxyPorts,
    createProxySubscription,
    deleteProxyNode,
    deleteProxySubscription,
    importProxyNodesYAML,
    probeProxyNode,
    proxyCoreStatusQueryOptions,
    proxyNodesQueryOptions,
    proxySubscriptionsQueryOptions,
    refreshProxySubscription,
    startProxyCore,
    stopProxyCore,
    syncProxyCore,
    updateProxyNode,
    type ProxyNode,
} from '@/api/proxy';

// Proxy 是代理出口页。
//
// 页面只回答三个问题，顺序也就是用户排查问题的顺序：
//   ① 内核起来了没有（所有出口的共同前置，坏了全是它）；
//   ② 有哪些节点、哪个通（探测出口 IP 是唯一可信的"通"判据）；
//   ③ 这些节点从哪来（订阅）。
// "某个账号走哪个节点"不在这一页选，放在渠道编辑里——绑定是渠道的属性，离开渠道去选会丢上下文。
export function Proxy() {
    const t = useTranslations('proxy');
    const queryClient = useQueryClient();
    const nodesQuery = useQuery(proxyNodesQueryOptions);
    const coreQuery = useQuery(proxyCoreStatusQueryOptions);
    const subscriptionsQuery = useQuery(proxySubscriptionsQueryOptions);

    const [keyword, setKeyword] = useState('');
    const [subscription, setSubscription] = useState({ name: '', url: '' });
    const [manualYAML, setManualYAML] = useState('');
    const [probing, setProbing] = useState<number | null>(null);

    const nodes = nodesQuery.data?.items ?? [];
    const usage = nodesQuery.data?.usage ?? {};
    const core = coreQuery.data;

    const refreshProxy = () => {
        void queryClient.invalidateQueries({ queryKey: ['proxy-nodes'] });
        void queryClient.invalidateQueries({ queryKey: ['proxy-subscriptions'] });
        void queryClient.invalidateQueries({ queryKey: ['proxy-core-status'] });
    };

    const filtered = useMemo(() => {
        const needle = keyword.trim().toLowerCase();
        if (!needle) return nodes;
        return nodes.filter((node) =>
            node.name.toLowerCase().includes(needle)
            || node.server.toLowerCase().includes(needle)
            || node.type.toLowerCase().includes(needle));
    }, [nodes, keyword]);

    const reportImport = (result: { added: number; updated: number; skipped: number; infos?: string[]; message?: string }) => {
        toast.success(t('importDone', { added: result.added, updated: result.updated, skipped: result.skipped }));
        // 订阅里夹带的运营信息（剩余流量/到期提醒）只提示、不入节点池——它们不是能连的节点。
        if (result.infos && result.infos.length > 0) toast.message(t('importInfos', { count: result.infos.length }));
        if (result.message) toast.message(result.message);
        refreshProxy();
    };

    const coreAction = useMutation({
        // 四个动作返回的形状不同（状态 / 端口分配结果），统一收成"动作 + 可选状态"，
        // 调用方只关心"成功与否 + 重新拉状态"，不关心返回值差异。
        mutationFn: async (action: 'start' | 'stop' | 'sync' | 'ports') => {
            if (action === 'start') return startProxyCore();
            if (action === 'stop') return stopProxyCore();
            if (action === 'sync') return syncProxyCore();
            await allocateProxyPorts();
            return undefined;
        },
        onSuccess: (_result, action) => {
            toast.success(t(action === 'start' ? 'coreStarted' : action === 'stop' ? 'coreStopped' : action === 'sync' ? 'coreSynced' : 'portsAllocated'));
            refreshProxy();
        },
        onError: (error: Error) => toast.error(error.message),
    });

    const toggleNode = useMutation({
        mutationFn: (node: ProxyNode) => updateProxyNode(node.id, {
            name: node.name, type: node.type, server: node.server, port: node.port, enabled: !node.enabled,
        }),
        onSuccess: () => refreshProxy(),
        onError: (error: Error) => toast.error(error.message),
    });

    const removeNode = useMutation({
        mutationFn: (id: number) => deleteProxyNode(id),
        onSuccess: () => toast.success(t('nodeDeleted')),
        // 已被账号/渠道引用的节点不允许删（后端会拒），把原因原样告诉用户。
        onError: (error: Error) => toast.error(error.message),
    });

    const probe = async (node: ProxyNode) => {
        setProbing(node.id);
        try {
            const result = await probeProxyNode(node.id);
            toast.success(t('probeOK', { name: node.name, ip: result.exit_ip }));
            refreshProxy();
        } catch (error) {
            toast.error((error as Error).message);
        } finally {
            setProbing(null);
        }
    };

    const addSubscription = useMutation({
        mutationFn: () => createProxySubscription({ name: subscription.name.trim(), url: subscription.url.trim(), enabled: true }),
        onSuccess: () => {
            setSubscription({ name: '', url: '' });
            toast.success(t('subscriptionAdded'));
            refreshProxy();
        },
        onError: (error: Error) => toast.error(error.message),
    });

    const refreshSubscription = useMutation({
        mutationFn: (id: number) => refreshProxySubscription(id),
        onSuccess: (result) => reportImport(result),
        onError: (error: Error) => toast.error(error.message),
    });

    const removeSubscription = useMutation({
        mutationFn: (id: number) => deleteProxySubscription(id),
        onSuccess: () => {
            toast.success(t('subscriptionDeleted'));
            refreshProxy();
        },
        onError: (error: Error) => toast.error(error.message),
    });

    const importYAML = useMutation({
        mutationFn: () => importProxyNodesYAML(manualYAML),
        onSuccess: (result) => {
            setManualYAML('');
            reportImport(result);
        },
        onError: (error: Error) => toast.error(error.message),
    });

    const enabledNodes = nodes.filter((node) => node.enabled).length;
    const readyNodes = nodes.filter((node) => node.enabled && node.local_port > 0).length;

    return (
        <div className="flex h-full min-h-0 flex-col gap-4 p-4">
            {/* 内核状态：它是所有出口的共同前置，常驻在最上面 */}
            <div className="rounded-2xl border border-border p-4">
                <div className="flex flex-wrap items-center gap-3">
                    <Activity className="size-5" />
                    <span className="text-sm font-medium">{t('core')}</span>
                    <Badge variant={core?.running ? 'default' : 'secondary'}>
                        {core?.running ? t('coreRunning') : t('coreStopped')}
                    </Badge>
                    {core?.running && <span className="text-xs text-muted-foreground">{t('corePid', { pid: core.pid })}</span>}
                    <span className="text-xs text-muted-foreground">
                        {t('coreListeners', { ready: readyNodes, enabled: enabledNodes })}
                    </span>
                    {core?.version && (
                        <span className="text-xs text-muted-foreground truncate max-w-[22rem]" title={core.version}>
                            {core.version}
                        </span>
                    )}
                    <div className="ml-auto flex items-center gap-2">
                        <Button size="sm" variant="outline" onClick={() => coreAction.mutate('ports')} disabled={coreAction.isPending}>
                            {t('allocatePorts')}
                        </Button>
                        <Button size="sm" variant="outline" onClick={() => coreAction.mutate('sync')} disabled={coreAction.isPending}>
                            <RefreshCw className="size-4" />{t('syncCore')}
                        </Button>
                        {core?.running
                            ? <Button size="sm" variant="outline" onClick={() => coreAction.mutate('stop')} disabled={coreAction.isPending}>{t('stopCore')}</Button>
                            : <Button size="sm" onClick={() => coreAction.mutate('start')} disabled={coreAction.isPending}>{t('startCore')}</Button>}
                    </div>
                </div>
                {(core?.last_error || !core?.binary_ok) && (
                    <p className="mt-2 text-xs text-destructive">{core?.last_error}</p>
                )}
                <p className="mt-2 text-xs text-muted-foreground">{t('coreHint')}</p>
            </div>

            <Tabs defaultValue="nodes" className="flex min-h-0 flex-1 flex-col">
                <TabsList className="shrink-0">
                    <TabsTrigger value="nodes">{t('tabNodes', { count: nodes.length })}</TabsTrigger>
                    <TabsTrigger value="subscriptions">{t('tabSubscriptions', { count: subscriptionsQuery.data?.total ?? 0 })}</TabsTrigger>
                    <TabsTrigger value="import">{t('tabImport')}</TabsTrigger>
                </TabsList>

                <TabsContent value="nodes" className="flex min-h-0 flex-1 flex-col gap-3">
                    <Input
                        value={keyword}
                        onChange={(event) => setKeyword(event.target.value)}
                        placeholder={t('searchNode')}
                        className="rounded-xl max-w-sm"
                    />
                    <div className="min-h-0 flex-1 overflow-y-auto overscroll-contain rounded-2xl border border-border">
                        <table className="w-full text-sm">
                            <thead className="sticky top-0 bg-background/95 backdrop-blur">
                                <tr className="text-left text-xs text-muted-foreground">
                                    <th className="px-3 py-2 font-medium">{t('colName')}</th>
                                    <th className="px-3 py-2 font-medium">{t('colType')}</th>
                                    <th className="px-3 py-2 font-medium">{t('colServer')}</th>
                                    <th className="px-3 py-2 font-medium">{t('colPort')}</th>
                                    <th className="px-3 py-2 font-medium">{t('colExit')}</th>
                                    <th className="px-3 py-2 font-medium">{t('colUsed')}</th>
                                    <th className="px-3 py-2" />
                                </tr>
                            </thead>
                            <tbody>
                                {filtered.map((node) => (
                                    <tr key={node.id} className="border-t border-border/60">
                                        <td className="px-3 py-2 max-w-[16rem] truncate" title={node.name}>{node.name}</td>
                                        <td className="px-3 py-2 text-muted-foreground">{node.type}</td>
                                        <td className="px-3 py-2 font-mono text-xs text-muted-foreground truncate max-w-[14rem]" title={`${node.server}:${node.port}`}>
                                            {node.server}:{node.port}
                                        </td>
                                        <td className="px-3 py-2 font-mono text-xs tabular-nums">
                                            {node.local_port > 0 ? `127.0.0.1:${node.local_port}` : <span className="text-muted-foreground">{t('portPending')}</span>}
                                        </td>
                                        <td className="px-3 py-2 font-mono text-xs">
                                            {node.last_exit_ip
                                                ? <span className={node.last_probe_ok ? '' : 'text-destructive'}>{node.last_exit_ip}</span>
                                                : <span className="text-muted-foreground">—</span>}
                                        </td>
                                        <td className="px-3 py-2 text-xs text-muted-foreground max-w-[14rem] truncate" title={(usage[String(node.id)] ?? []).join('、')}>
                                            {(usage[String(node.id)] ?? []).length > 0
                                                ? t('usedBy', { count: (usage[String(node.id)] ?? []).length })
                                                : t('unused')}
                                        </td>
                                        <td className="px-3 py-2">
                                            <div className="flex items-center justify-end gap-2">
                                                <IconButton
                                                    className="size-8"
                                                    tip={t('probe')}
                                                    disabled={probing === node.id || !node.enabled || node.local_port <= 0}
                                                    onClick={() => void probe(node)}
                                                >
                                                    <Zap className={`size-4 ${probing === node.id ? 'animate-pulse' : ''}`} />
                                                </IconButton>
                                                <Switch
                                                    checked={node.enabled}
                                                    onCheckedChange={() => toggleNode.mutate(node)}
                                                />
                                                <IconButton className="size-8 hover:text-destructive" tip={t('delete')} onClick={() => removeNode.mutate(node.id)}>
                                                    <Globe className="size-4" />
                                                </IconButton>
                                            </div>
                                        </td>
                                    </tr>
                                ))}
                                {filtered.length === 0 && (
                                    <tr>
                                        <td colSpan={7} className="px-3 py-10 text-center text-sm text-muted-foreground">
                                            {nodes.length === 0 ? t('noNodes') : t('noMatch')}
                                        </td>
                                    </tr>
                                )}
                            </tbody>
                        </table>
                    </div>
                </TabsContent>

                <TabsContent value="subscriptions" className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain">
                    <div className="flex flex-wrap items-end gap-2 rounded-2xl border border-border p-3">
                        <div className="space-y-1">
                            <Label htmlFor="subscription-name">{t('subscriptionName')}</Label>
                            <Input
                                id="subscription-name"
                                value={subscription.name}
                                onChange={(event) => setSubscription({ ...subscription, name: event.target.value })}
                                placeholder={t('subscriptionNamePlaceholder')}
                                className="rounded-xl w-48"
                            />
                        </div>
                        <div className="space-y-1 flex-1 min-w-[16rem]">
                            <Label htmlFor="subscription-url">{t('subscriptionURL')}</Label>
                            <Input
                                id="subscription-url"
                                value={subscription.url}
                                onChange={(event) => setSubscription({ ...subscription, url: event.target.value })}
                                placeholder="https://…/sub"
                                className="rounded-xl"
                            />
                        </div>
                        <Button
                            onClick={() => addSubscription.mutate()}
                            disabled={addSubscription.isPending || !subscription.name.trim() || !subscription.url.trim()}
                        >
                            <Plus className="size-4" />{t('addSubscription')}
                        </Button>
                    </div>

                    <div className="space-y-2">
                        {(subscriptionsQuery.data?.items ?? []).map((sub) => (
                            <div key={sub.id} className="flex flex-wrap items-center gap-3 rounded-xl border border-border px-3 py-2">
                                <Server className="size-4" />
                                <span className="text-sm font-medium">{sub.name}</span>
                                <span className="text-xs font-mono text-muted-foreground">{sub.url_hint}</span>
                                <Badge variant="secondary">{t('nodeCount', { count: sub.node_count })}</Badge>
                                {sub.last_fetch_at && <span className="text-xs text-muted-foreground">{sub.last_fetch_at}</span>}
                                {sub.last_error && <span className="text-xs text-destructive truncate max-w-[20rem]">{sub.last_error}</span>}
                                <div className="ml-auto flex items-center gap-2">
                                    <IconButton
                                        className="size-8"
                                        tip={t('refresh')}
                                        disabled={refreshSubscription.isPending}
                                        onClick={() => refreshSubscription.mutate(sub.id)}
                                    >
                                        <RefreshCw className={`size-4 ${refreshSubscription.isPending ? 'animate-spin' : ''}`} />
                                    </IconButton>
                                    <IconButton className="size-8 hover:text-destructive" tip={t('delete')} onClick={() => removeSubscription.mutate(sub.id)}>
                                        <Globe className="size-4" />
                                    </IconButton>
                                </div>
                            </div>
                        ))}
                        {(subscriptionsQuery.data?.items ?? []).length === 0 && (
                            <p className="py-10 text-center text-sm text-muted-foreground">{t('noSubscriptions')}</p>
                        )}
                    </div>
                </TabsContent>

                <TabsContent value="import" className="min-h-0 flex-1 space-y-3 overflow-y-auto overscroll-contain">
                    <p className="text-xs text-muted-foreground">{t('importHint')}</p>
                    <textarea
                        value={manualYAML}
                        onChange={(event) => setManualYAML(event.target.value)}
                        placeholder="proxies:&#10;  - name: 节点名&#10;    type: ss&#10;    server: 1.2.3.4&#10;    port: 443&#10;    cipher: aes-128-gcm&#10;    password: …"
                        className="h-64 w-full rounded-xl border border-border bg-transparent p-3 font-mono text-xs outline-none"
                    />
                    <div className="flex items-center gap-2">
                        <Button onClick={() => importYAML.mutate()} disabled={importYAML.isPending || !manualYAML.trim()}>
                            <Import className="size-4" />{t('importNow')}
                        </Button>
                        <span className="text-xs text-muted-foreground">{t('importLocalHint')}</span>
                    </div>
                    <p className="flex items-center gap-2 text-xs text-muted-foreground">
                        <Download className="size-3.5" />{t('importNodeHint')}
                    </p>
                </TabsContent>
            </Tabs>
        </div>
    );
}
