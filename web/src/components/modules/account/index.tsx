import { useMemo, useState } from 'react';
import { CheckCircle2, CircleAlert, Clock3, Copy, ExternalLink, KeyRound, Link2, Loader2, Plus, RefreshCw, ShieldCheck, Sparkles, UserRound } from 'lucide-react';
import { toast } from 'sonner';
import { useTranslations } from 'use-intl';
import { useQueryClient } from '@tanstack/react-query';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs';
import { Separator } from '@/components/ui/separator';
import { CopyIconButton } from '@/components/common/CopyButton';
import { cn } from '@/lib/utils';
import {
    officialAccountListQueryKey,
    jumpTokenListQueryKey,
    useAuthorizeOfficialAccount,
    useConfirmOfficialAccount,
    useCreateJumpToken,
    useJumpTokens,
    useOfficialAccounts,
    useOfficialPools,
    useRefreshOfficialAccountUsage,
    useSyncOfficialPool,
    type OfficialAccount,
    type OfficialPoolStatus,
    type OfficialProvider,
    type JumpKind,
    type JumpToken,
} from '@/api/account';

const providerMeta: Record<OfficialProvider, { label: string; className: string }> = {
    openai: { label: 'OpenAI', className: 'bg-emerald-500/10 text-emerald-600 dark:text-emerald-400' },
    gemini: { label: 'Gemini', className: 'bg-blue-500/10 text-blue-600 dark:text-blue-400' },
    claude: { label: 'Claude', className: 'bg-orange-500/10 text-orange-600 dark:text-orange-400' },
};

function formatDate(value: string | null) {
    return value ? new Date(value).toLocaleString() : '—';
}

function StatusBadge({ account }: { account: OfficialAccount }) {
    const t = useTranslations('accounts');
    const healthy = account.status === 'active' && account.healthy;
    const label = healthy ? t('status.healthy') : t(`status.${account.status}`);
    return (
        <Badge variant={healthy ? 'default' : account.status === 'error' ? 'destructive' : 'secondary'} className="gap-1">
            {healthy ? <CheckCircle2 className="size-3" /> : <CircleAlert className="size-3" />}
            {label}
        </Badge>
    );
}

function AccountCard({ account }: { account: OfficialAccount }) {
    const t = useTranslations('accounts');
    const refresh = useRefreshOfficialAccountUsage();
    const meta = providerMeta[account.provider];
    return (
        <article className="group relative overflow-hidden rounded-3xl border border-border/70 bg-card p-5 shadow-sm transition-all hover:-translate-y-0.5 hover:shadow-lg">
            <div className="absolute inset-x-0 top-0 h-1 bg-gradient-to-r from-primary/70 via-primary/20 to-transparent" />
            <div className="flex items-start gap-4">
                <div className={`grid size-12 shrink-0 place-items-center rounded-2xl text-sm font-bold ${meta.className}`}>
                    {account.provider === 'openai' ? 'AI' : account.provider === 'gemini' ? 'G' : 'C'}
                </div>
                <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                        <h3 className="truncate font-semibold">{account.external_name || t('pendingAccount')}</h3>
                        <Badge variant="outline" className="capitalize">{meta.label}</Badge>
                        <StatusBadge account={account} />
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">{t('accountId', { id: account.id })}</p>
                </div>
                <Button variant="ghost" size="icon" className="rounded-xl" disabled={refresh.isPending} onClick={() => refresh.mutate(account.id, { onSuccess: () => toast.success(t('refreshed')), onError: (error) => toast.error(t('refreshFailed'), { description: error.message }) })}>
                    {refresh.isPending ? <Loader2 className="size-4 animate-spin" /> : <RefreshCw className="size-4" />}
                </Button>
            </div>
            <div className="mt-5 grid grid-cols-2 gap-3 sm:grid-cols-4">
                <Metric label={t('plan')} value={account.plan_tier || '—'} />
                <Metric label={t('fiveHour')} value={account.window_5h || '—'} />
                <Metric label={t('sevenDay')} value={account.window_7d || '—'} />
                <Metric label={t('expires')} value={formatDate(account.expires_at)} />
            </div>
            {account.last_error && <div className="mt-4 flex items-start gap-2 rounded-2xl bg-destructive/10 p-3 text-xs text-destructive"><CircleAlert className="mt-0.5 size-4 shrink-0" />{account.last_error}</div>}
            <p className="mt-4 text-right text-[11px] text-muted-foreground">{t('checkedAt', { value: formatDate(account.last_checked_at) })}</p>
        </article>
    );
}

function Metric({ label, value }: { label: string; value: string }) {
    return <div className="rounded-2xl bg-muted/40 p-3"><p className="text-[11px] text-muted-foreground">{label}</p><p className="mt-1 truncate text-sm font-semibold tabular-nums">{value}</p></div>;
}

function OfficialAccountsPanel() {
    const t = useTranslations('accounts');
    const queryClient = useQueryClient();
    const { data, isLoading, error } = useOfficialAccounts();
    const authorize = useAuthorizeOfficialAccount();
    const confirm = useConfirmOfficialAccount();
    const [provider, setProvider] = useState<OfficialProvider>('openai');
    const [pending, setPending] = useState<{ id: number; state: string; url: string } | null>(null);
    const [code, setCode] = useState('');

    const startAuthorization = () => authorize.mutate(provider, {
        onSuccess: (result) => {
            setPending({ id: result.account.id, state: result.state, url: result.authorize_url });
            window.open(result.authorize_url, '_blank', 'noopener,noreferrer');
            toast.success(t('authorizationStarted'));
        },
        onError: (requestError) => toast.error(t('authorizationFailed'), { description: requestError.message }),
    });

    const finishAuthorization = (event: React.FormEvent) => {
        event.preventDefault();
        if (!pending || !code.trim()) return;
        confirm.mutate({ account_id: pending.id, one_time_code: code.trim(), state: pending.state }, {
            onSuccess: () => {
                setPending(null); setCode('');
                void queryClient.invalidateQueries({ queryKey: officialAccountListQueryKey });
                toast.success(t('authorizationComplete'));
            },
            onError: (requestError) => toast.error(t('authorizationFailed'), { description: requestError.message }),
        });
    };

    return (
        <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-auto pb-8 pr-1">
            <section className="relative overflow-hidden rounded-3xl border border-border/70 bg-gradient-to-br from-primary/[0.12] via-card to-card p-6">
                <div className="absolute -right-16 -top-20 size-52 rounded-full bg-primary/10 blur-3xl" />
                <div className="relative flex flex-col gap-5 md:flex-row md:items-center md:justify-between">
                    <div><div className="mb-2 inline-flex items-center gap-2 rounded-full bg-primary/10 px-3 py-1 text-xs font-medium text-primary"><ShieldCheck className="size-3.5" />{t('secureTitle')}</div><h2 className="text-xl font-bold tracking-tight">{t('officialTitle')}</h2><p className="mt-1 max-w-xl text-sm text-muted-foreground">{t('officialSubtitle')}</p></div>
                    <div className="flex flex-wrap gap-2">
                        {(Object.keys(providerMeta) as OfficialProvider[]).map((item) => <Button key={item} type="button" variant={provider === item ? 'default' : 'outline'} className="rounded-xl" onClick={() => setProvider(item)}>{providerMeta[item].label}</Button>)}
                        <Button type="button" className="rounded-xl" disabled={authorize.isPending} onClick={startAuthorization}>{authorize.isPending ? <Loader2 className="animate-spin" /> : <Plus />} {t('connect')}</Button>
                    </div>
                </div>
            </section>
            {pending && <form onSubmit={finishAuthorization} className="rounded-3xl border border-primary/30 bg-primary/[0.06] p-5"><div className="flex items-start gap-3"><KeyRound className="mt-1 size-5 text-primary" /><div className="min-w-0 flex-1"><h3 className="font-semibold">{t('finishTitle')}</h3><p className="mt-1 text-sm text-muted-foreground">{t('finishSubtitle')}</p><div className="mt-4 flex flex-col gap-2 sm:flex-row"><Input value={code} onChange={(event) => setCode(event.target.value)} placeholder={t('codePlaceholder')} className="rounded-xl bg-background" autoComplete="one-time-code" /><Button type="submit" className="rounded-xl" disabled={confirm.isPending || !code.trim()}>{confirm.isPending ? <Loader2 className="animate-spin" /> : <CheckCircle2 />} {t('confirm')}</Button></div><button type="button" className="mt-3 inline-flex items-center gap-1 text-xs text-primary hover:underline" onClick={() => window.open(pending.url, '_blank', 'noopener,noreferrer')}><ExternalLink className="size-3" />{t('reopen')}</button></div></div></form>}
            {isLoading && <LoadingState />}
            {error && <ErrorState message={error.message} />}
            {!isLoading && !error && data?.items.length === 0 && <EmptyState icon={Sparkles} title={t('noAccounts')} description={t('noAccountsHint')} />}
            <div className="grid gap-4 xl:grid-cols-2">{data?.items.map((account) => <AccountCard key={account.id} account={account} />)}</div>
        </div>
    );
}

function JumpPanel() {
    const t = useTranslations('accounts');
    const { data, isLoading } = useJumpTokens();
    const create = useCreateJumpToken();
    const [kind, setKind] = useState<JumpKind>('na');
    const [url, setUrl] = useState('');
    const [created, setCreated] = useState<string | null>(null);

    const submit = (event: React.FormEvent) => {
        event.preventDefault();
        if (!url.trim()) return;
        create.mutate({ kind, target_url: url.trim() }, { onSuccess: (result) => { setCreated(`${window.location.origin}/api/v1/account/jump/go/${result.token}`); setUrl(''); toast.success(t('linkCreated')); }, onError: (error) => toast.error(t('linkFailed'), { description: error.message }) });
    };

    return <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-auto pb-8 pr-1">
        <section className="rounded-3xl border border-border/70 bg-card p-6"><div className="flex items-start gap-3"><div className="grid size-11 place-items-center rounded-2xl bg-amber-500/10 text-amber-600"><Link2 className="size-5" /></div><div><h2 className="text-xl font-bold">{t('jumpTitle')}</h2><p className="mt-1 max-w-2xl text-sm text-muted-foreground">{t('jumpSubtitle')}</p></div></div><Separator className="my-5" /><form onSubmit={submit}><FieldGroup className="gap-4"><div className="grid gap-4 sm:grid-cols-[180px_1fr_auto]"><Field><FieldLabel>{t('loginTarget')}</FieldLabel><div className="grid grid-cols-2 gap-2">{(['na', 's2'] as JumpKind[]).map((item) => <Button key={item} type="button" variant={kind === item ? 'default' : 'outline'} className="rounded-xl" onClick={() => setKind(item)}>{item === 'na' ? 'New API' : 'Sub2Api'}</Button>)}</div></Field><Field><FieldLabel htmlFor="jump-url">{t('targetUrl')}</FieldLabel><Input id="jump-url" value={url} onChange={(event) => setUrl(event.target.value)} placeholder={t('urlPlaceholder')} type="url" className="rounded-xl" /></Field><Button type="submit" className="mt-auto rounded-xl" disabled={create.isPending || !url.trim()}>{create.isPending ? <Loader2 className="animate-spin" /> : <Plus />} {t('createLink')}</Button></div></FieldGroup></form>{created && <div className="mt-4 flex flex-col gap-3 rounded-2xl border border-emerald-500/30 bg-emerald-500/[0.06] p-4"><div className="flex items-center gap-2 text-sm font-semibold text-emerald-600"><CheckCircle2 className="size-4" />{t('linkCreatedOnce')}</div><div className="flex items-center gap-2"><code className="min-w-0 flex-1 truncate rounded-xl bg-background px-3 py-2 text-xs">{created}</code><CopyIconButton text={created} className="grid size-9 shrink-0 place-items-center rounded-xl bg-background text-muted-foreground hover:text-foreground" /></div><p className="text-xs text-muted-foreground"><Clock3 className="mr-1 inline size-3" />{t('linkExpiry')}</p></div>}</section>
        <section className="rounded-3xl border border-border/70 bg-card p-5"><div className="mb-4 flex items-center justify-between"><div><h3 className="font-semibold">{t('auditTitle')}</h3><p className="text-xs text-muted-foreground">{t('auditSubtitle')}</p></div><Badge variant="secondary">{data?.total ?? 0}</Badge></div>{isLoading ? <LoadingState /> : data?.items.length ? <div className="grid gap-2">{data.items.map((token) => <JumpRow key={token.id} token={token} />)}</div> : <EmptyState icon={Clock3} title={t('noLinks')} description={t('noLinksHint')} />}</section>
    </div>;
}

function JumpRow({ token }: { token: JumpToken }) {
    const t = useTranslations('accounts');
    const expired = new Date(token.expires_at).getTime() < Date.now();
    return <div className="flex flex-col gap-2 rounded-2xl bg-muted/35 p-3 text-sm sm:flex-row sm:items-center"><div className="flex items-center gap-2"><Badge variant="outline">{token.kind === 'na' ? 'New API' : 'Sub2Api'}</Badge><span className="font-medium">{token.target_url}</span></div><div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">{token.consumed_at ? <Badge variant="secondary">{t('used')}</Badge> : expired ? <Badge variant="destructive">{t('expired')}</Badge> : <Badge variant="default">{t('available')}</Badge>}<span>{formatDate(token.created_at)}</span></div></div>;
}

// PoolPanel 是统一号池视图：OpenAI(GPT) 与 Gemini(谷歌) 以及 Claude 三个号池放在同一张表里，
// 一个"全部同步"按钮即可把它们一起物化成号池渠道凭据（后端按 provider 各建一条渠道，渠道地址互不干扰）。
function PoolPanel() {
    const t = useTranslations('pool');
    const { data, isLoading, error } = useOfficialPools();
    const sync = useSyncOfficialPool();
    const [notes, setNotes] = useState<Array<{ provider: OfficialProvider; notes: string[] }>>([]);
    const pools = data?.items ?? [];

    const runSync = (provider?: OfficialProvider) => {
        sync.mutate(provider, {
            onSuccess: (result) => {
                const withNotes = (result.items ?? [])
                    .filter((item) => (item.notes ?? []).length > 0)
                    .map((item) => ({ provider: item.provider, notes: item.notes ?? [] }));
                setNotes(withNotes);
                toast.success(provider ? t('syncedOne', { provider: providerMeta[provider].label }) : t('syncedAll'));
            },
            onError: (err) => toast.error(t('syncFailed'), { description: err.message }),
        });
    };

    return <div className="flex min-h-0 flex-1 flex-col gap-5 overflow-auto pb-8 pr-1">
        <section className="rounded-3xl border border-border/70 bg-card p-6">
            <div className="flex items-start gap-3">
                <div className="grid size-11 shrink-0 place-items-center rounded-2xl bg-sky-500/10 text-sky-600"><KeyRound className="size-5" /></div>
                <div className="min-w-0 flex-1">
                    <h2 className="text-xl font-bold">{t('title')}</h2>
                    <p className="mt-1 max-w-3xl text-sm text-muted-foreground">{t('subtitle')}</p>
                </div>
                <Button className="rounded-xl" onClick={() => runSync()} disabled={sync.isPending}>
                    {sync.isPending ? <Loader2 className="animate-spin" /> : <RefreshCw />} {t('syncAll')}
                </Button>
            </div>
            <Separator className="my-5" />
            {isLoading ? <LoadingState /> : error ? <ErrorState message={error.message} /> : pools.length ? (
                <div className="grid gap-4">
                    {pools.map((pool) => <PoolCard key={pool.provider} pool={pool} syncing={sync.isPending} onSync={() => runSync(pool.provider)} />)}
                </div>
            ) : <EmptyState icon={Sparkles} title={t('empty')} description={t('emptyHint')} />}
            {notes.length > 0 && <div className="mt-4 grid gap-2">{notes.map((item) => <div key={item.provider} className="rounded-2xl border border-amber-500/30 bg-amber-500/[0.06] p-3 text-xs text-amber-700 dark:text-amber-400"><span className="font-semibold">{providerMeta[item.provider].label}</span>{item.notes.map((note, index) => <div key={index} className="mt-1">{note}</div>)}</div>)}</div>}
        </section>
    </div>;
}

// PoolCard 是一个服务商的号池：概览计数 + 逐账号的"账号 → 凭据"映射 + 单独同步入口。
function PoolCard({ pool, syncing, onSync }: { pool: OfficialPoolStatus; syncing: boolean; onSync: () => void }) {
    const t = useTranslations('pool');
    const meta = providerMeta[pool.provider];
    const members = pool.members ?? [];
    return <div className="rounded-2xl border border-border/70 bg-muted/25 p-4">
        <div className="flex flex-wrap items-center gap-3">
            <span className={cn('rounded-lg px-2 py-0.5 text-xs font-semibold', meta.className)}>{meta.label}</span>
            <span className="font-medium">{pool.channel_name}</span>
            {pool.channel_id === 0 ? <Badge variant="secondary">{t('notSynced')}</Badge> : null}
            <div className="ml-auto flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                <span>{t('accounts')} <b className="text-foreground">{pool.accounts}</b></span>
                <span>{t('activeKeys')} <b className="text-foreground">{pool.active_keys}</b></span>
                <span>{t('models')} <b className="text-foreground">{pool.models}</b></span>
                <span>{t('grants')} <b className="text-foreground">{pool.grants}</b></span>
                <Button size="sm" variant="outline" className="rounded-xl" onClick={onSync} disabled={syncing}>
                    <RefreshCw className="size-3.5" /> {t('syncOne')}
                </Button>
            </div>
        </div>
        {members.length ? <div className="mt-3 grid gap-1.5">{members.map((member) => <div key={member.account_id} className="flex flex-wrap items-center gap-2 rounded-xl bg-background/70 px-3 py-2 text-xs">
            <span className="font-medium">{member.external_name}</span>
            <Badge variant={member.status === 'active' ? 'default' : 'secondary'}>{member.status}</Badge>
            {member.plan_tier ? <span className="text-muted-foreground">{member.plan_tier}</span> : null}
            {member.window_5h ? <span className="text-muted-foreground">5H {member.window_5h}</span> : null}
            {member.window_7d ? <span className="text-muted-foreground">7D {member.window_7d}</span> : null}
            {member.expires_at ? <span className="text-muted-foreground">{formatDate(member.expires_at)}</span> : null}
            <span className="ml-auto flex items-center gap-1.5">
                {member.key_exists
                    ? (member.key_enabled
                        ? <><CheckCircle2 className="size-3.5 text-emerald-500" />{t('keyEnabled')}</>
                        : <><CircleAlert className="size-3.5 text-muted-foreground" />{t('keyDisabled')}</>)
                    : <><CircleAlert className="size-3.5 text-amber-500" />{t('keyMissing')}</>}
            </span>
        </div>)}</div> : <p className="mt-3 text-xs text-muted-foreground">{t('noAccount')}</p>}
    </div>;
}

function LoadingState() { return <div className="grid place-items-center rounded-3xl border border-dashed border-border p-12 text-sm text-muted-foreground"><Loader2 className="mb-2 size-5 animate-spin" />Loading…</div>; }
function ErrorState({ message }: { message: string }) { return <div className="rounded-3xl border border-destructive/30 bg-destructive/5 p-5 text-sm text-destructive">{message}</div>; }
function EmptyState({ icon: Icon, title, description }: { icon: typeof Sparkles; title: string; description: string }) { return <div className="grid place-items-center rounded-3xl border border-dashed border-border p-12 text-center"><Icon className="mb-3 size-8 text-muted-foreground/50" /><h3 className="font-semibold">{title}</h3><p className="mt-1 text-sm text-muted-foreground">{description}</p></div>; }

export function AccountActions() { return null; }

export function Account() {
    const t = useTranslations('accounts');
    const stats = useMemo(() => null, []);
    void stats;
    return <Tabs defaultValue="official" className="flex min-h-0 flex-1"><TabsList className="mb-4 grid w-full max-w-xl grid-cols-3 rounded-2xl bg-muted/60 p-1"><TabsTrigger value="official" className="rounded-xl"><UserRound className="size-4" />{t('officialTab')}</TabsTrigger><TabsTrigger value="pool" className="rounded-xl"><KeyRound className="size-4" />{t('poolTab')}</TabsTrigger><TabsTrigger value="jump" className="rounded-xl"><Link2 className="size-4" />{t('jumpTab')}</TabsTrigger></TabsList><TabsContent value="official"><OfficialAccountsPanel /></TabsContent><TabsContent value="pool"><PoolPanel /></TabsContent><TabsContent value="jump"><JumpPanel /></TabsContent></Tabs>;
}
