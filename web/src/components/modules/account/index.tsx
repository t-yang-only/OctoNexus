import { useState } from 'react';
import { toast } from 'sonner';
import {
    CheckCircle2,
    Loader2,
    Plus,
    RefreshCw,
    Server,
    Trash2,
    Workflow,
} from 'lucide-react';
import { Badge } from '@/components/ui/badge';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field';
import { Separator } from '@/components/ui/separator';
import {
    useSub2APIAccounts,
    useCreateSub2APIAccount,
    useDeleteSub2APIAccount,
    useProbeSub2APIAccount,
    type Sub2APIAccount,
    type Sub2APIPlatform,
    type Sub2APIAccountType,
} from '@/api/sub2api';

const platformOptions: Array<{ id: Sub2APIPlatform; label: string; defaultBaseURL: string; desc: string }> = [
    { id: 'anthropic', label: 'Claude (Anthropic)', defaultBaseURL: 'https://api.anthropic.com', desc: '支持 Claude OAuth 会话、Setup Token 与 API Key 接入' },
    { id: 'openai', label: 'OpenAI (ChatGPT)', defaultBaseURL: 'https://api.openai.com', desc: '支持 Responses / Chat Completions 协议反代' },
    { id: 'gemini', label: 'Google Gemini', defaultBaseURL: 'https://generativelanguage.googleapis.com', desc: 'Google AI Studio / 订阅 OAuth 原生反代' },
    { id: 'antigravity', label: 'Antigravity (Cloud Code)', defaultBaseURL: 'https://cloudcode-pa.googleapis.com', desc: 'Cloud Code PA 沙盒多环境反向代理' },
    { id: 'grok', label: 'xAI Grok', defaultBaseURL: 'https://api.x.ai', desc: '支持 Grok 订阅会话桥接与 Responses 代理' },
    { id: 'deepseek', label: 'DeepSeek', defaultBaseURL: 'https://api.deepseek.com', desc: '深度求索原生与兼容端点反代' },
    { id: 'kimi', label: 'Moonshot Kimi', defaultBaseURL: 'https://api.moonshot.cn', desc: '月之暗面高并发与长上下文反代' },
    { id: 'opencode_go', label: 'OpenCode (Go/Zen)', defaultBaseURL: 'https://opencode.ai/zen/v1', desc: 'OpenCode Zen 按量与 Go 订阅双模' },
    { id: 'cline', label: 'Cline', defaultBaseURL: 'https://api.cline.bot/v1', desc: 'Cline 开发者订阅与积分反代' },
];

function Sub2APIAccountCard({ account }: { account: Sub2APIAccount }) {
    const deleteMutation = useDeleteSub2APIAccount();
    const probeMutation = useProbeSub2APIAccount();
    const [probeResult, setProbeResult] = useState<string | null>(null);

    const handleDelete = () => {
        if (confirm(`确认删除订阅反代账号「${account.name}」？`)) {
            deleteMutation.mutate(account.id, {
                onSuccess: () => toast.success('账号已删除'),
                onError: (err) => toast.error('删除失败: ' + err.message),
            });
        }
    };

    const handleProbe = () => {
        setProbeResult(null);
        probeMutation.mutate(account.id, {
            onSuccess: (res) => {
                setProbeResult(res.status);
                toast.success('探测完成: ' + res.status);
            },
            onError: (err) => {
                setProbeResult('异常');
                toast.error('探测失败: ' + err.message);
            },
        });
    };

    return (
        <div className="flex flex-col justify-between rounded-2xl border border-border/70 bg-card p-5 shadow-sm transition-all hover:border-border hover:shadow-md">
            <div>
                <div className="flex items-start justify-between gap-3">
                    <div className="flex items-center gap-2.5 min-w-0">
                        <div className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary/10 text-primary">
                            <Server className="size-5" />
                        </div>
                        <div className="min-w-0">
                            <h3 className="truncate font-semibold text-base leading-tight">{account.name}</h3>
                            <p className="mt-0.5 truncate text-xs text-muted-foreground">{account.base_url}</p>
                        </div>
                    </div>
                    <div className="flex items-center gap-1.5 shrink-0">
                        {account.channel_id && account.channel_id > 0 ? (
                            <Badge variant="outline" className="text-xs bg-emerald-500/10 text-emerald-600 dark:text-emerald-400 border-emerald-500/20">
                                渠道 #{account.channel_id}
                            </Badge>
                        ) : null}
                        <Badge variant={account.enabled ? 'default' : 'secondary'} className="capitalize">
                            {account.platform}
                        </Badge>
                    </div>
                </div>

                <div className="mt-4 grid grid-cols-2 gap-2 text-xs">
                    <div className="rounded-xl bg-muted/40 p-2.5">
                        <span className="text-muted-foreground block mb-0.5">账号类型</span>
                        <span className="font-medium text-foreground uppercase">{account.account_type}</span>
                    </div>
                    <div className="rounded-xl bg-muted/40 p-2.5">
                        <span className="text-muted-foreground block mb-0.5">协议分流</span>
                        <span className="font-medium text-foreground">{account.api_protocol}</span>
                    </div>
                </div>

                {account.notes && (
                    <p className="mt-3 rounded-lg border border-border/40 bg-muted/20 px-3 py-2 text-xs text-muted-foreground line-clamp-2">
                        {account.notes}
                    </p>
                )}

                {probeResult && (
                    <div className="mt-3 flex items-center gap-1.5 text-xs text-emerald-600 font-medium">
                        <CheckCircle2 className="size-3.5" />
                        连通性: {probeResult}
                    </div>
                )}
            </div>

            <div className="mt-5 flex items-center justify-between border-t border-border/50 pt-3">
                <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="h-8 rounded-xl text-xs"
                    onClick={handleProbe}
                    disabled={probeMutation.isPending}
                >
                    {probeMutation.isPending ? <Loader2 className="mr-1.5 size-3.5 animate-spin" /> : <RefreshCw className="mr-1.5 size-3.5" />}
                    测试连接
                </Button>
                <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-8 rounded-xl text-xs text-destructive hover:bg-destructive/10 hover:text-destructive"
                    onClick={handleDelete}
                    disabled={deleteMutation.isPending}
                >
                    <Trash2 className="size-3.5" />
                </Button>
            </div>
        </div>
    );
}

function Sub2APIAccountsPanel() {
    const { data: accounts, isLoading, error } = useSub2APIAccounts();
    const createMutation = useCreateSub2APIAccount();

    const [name, setName] = useState('');
    const [platform, setPlatform] = useState<Sub2APIPlatform>('anthropic');
    const [accountType, setAccountType] = useState<Sub2APIAccountType>('oauth');
    const [baseURL, setBaseURL] = useState('https://api.anthropic.com');
    const [apiKey, setApiKey] = useState('');
    const [apiProtocol, setApiProtocol] = useState('chat_completions');
    const [syncChannel, setSyncChannel] = useState(true);
    const [notes, setNotes] = useState('');

    const handlePlatformChange = (p: Sub2APIPlatform) => {
        setPlatform(p);
        const matched = platformOptions.find((opt) => opt.id === p);
        if (matched) {
            setBaseURL(matched.defaultBaseURL);
        }
    };

    const handleCreate = (e: React.FormEvent) => {
        e.preventDefault();
        if (!name.trim()) {
            toast.error('请填写账号名称');
            return;
        }
        if (!baseURL.trim()) {
            toast.error('请填写反代基地址');
            return;
        }

        createMutation.mutate(
            {
                name: name.trim(),
                platform,
                account_type: accountType,
                base_url: baseURL.trim(),
                api_key: apiKey.trim(),
                api_protocol: apiProtocol,
                enabled: true,
                sync_channel: syncChannel,
                notes: notes.trim(),
            },
            {
                onSuccess: () => {
                    toast.success('Sub2API 订阅反代账号创建成功');
                    setName('');
                    setApiKey('');
                    setNotes('');
                },
                onError: (err) => toast.error('创建失败: ' + err.message),
            }
        );
    };

    return (
        <div className="space-y-6">
            <section className="rounded-3xl border border-border/70 bg-card p-6 shadow-sm">
                <div className="flex items-start justify-between gap-4">
                    <div>
                        <div className="mb-2 inline-flex items-center gap-2 rounded-full bg-primary/10 px-3 py-1 text-xs font-medium text-primary">
                            <Workflow className="size-3.5" />
                            Sub2API 架构集成
                        </div>
                        <h2 className="text-xl font-bold tracking-tight">接入 Sub2API 订阅反代</h2>
                        <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
                            无缝接入 Claude、OpenAI、Gemini、Antigravity、Grok 订阅池与多模型反代网关，支持协议直通、格式转换及防关联出口绑定。
                        </p>
                    </div>
                </div>

                <Separator className="my-5" />

                <form onSubmit={handleCreate} className="space-y-4">
                    <FieldGroup className="gap-4">
                        <div>
                            <FieldLabel className="mb-2 block text-xs font-semibold uppercase tracking-wider text-muted-foreground">
                                目标平台选择
                            </FieldLabel>
                            <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
                                {platformOptions.map((item) => (
                                    <button
                                        key={item.id}
                                        type="button"
                                        onClick={() => handlePlatformChange(item.id)}
                                        className={`flex flex-col items-start rounded-xl border p-3 text-left transition-all ${
                                            platform === item.id
                                                ? 'border-primary bg-primary/5 text-primary shadow-sm'
                                                : 'border-border/70 bg-muted/20 hover:border-border hover:bg-muted/40'
                                        }`}
                                    >
                                        <span className="font-semibold text-xs text-foreground">{item.label}</span>
                                        <span className="mt-1 line-clamp-1 text-[11px] text-muted-foreground">{item.desc}</span>
                                    </button>
                                ))}
                            </div>
                        </div>

                        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
                            <Field>
                                <FieldLabel htmlFor="sub2api-name">账号别名</FieldLabel>
                                <Input
                                    id="sub2api-name"
                                    placeholder="例如 Claude 拼车 01 号"
                                    value={name}
                                    onChange={(e) => setName(e.target.value)}
                                    className="rounded-xl"
                                />
                            </Field>

                            <Field>
                                <FieldLabel htmlFor="sub2api-url">反代基地址 (Base URL)</FieldLabel>
                                <Input
                                    id="sub2api-url"
                                    placeholder="https://..."
                                    value={baseURL}
                                    onChange={(e) => setBaseURL(e.target.value)}
                                    className="rounded-xl"
                                />
                            </Field>

                            <Field>
                                <FieldLabel htmlFor="sub2api-type">认证形式</FieldLabel>
                                <select
                                    id="sub2api-type"
                                    className="flex h-9 w-full rounded-xl border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                                    value={accountType}
                                    onChange={(e) => setAccountType(e.target.value as Sub2APIAccountType)}
                                >
                                    <option value="oauth">OAuth 会话 / 订阅反代</option>
                                    <option value="apikey">标准 API Key</option>
                                    <option value="setup-token">Setup Token (Claude)</option>
                                </select>
                            </Field>

                            <Field>
                                <FieldLabel htmlFor="sub2api-proto">上游协议分流</FieldLabel>
                                <select
                                    id="sub2api-proto"
                                    className="flex h-9 w-full rounded-xl border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                                    value={apiProtocol}
                                    onChange={(e) => setApiProtocol(e.target.value)}
                                >
                                    <option value="chat_completions">Chat Completions (OpenAI 兼容)</option>
                                    <option value="responses">Responses (Codex 原生)</option>
                                    <option value="anthropic">Anthropic Messages (/v1/messages)</option>
                                    <option value="adaptive">自适应 (按入站协议直通)</option>
                                </select>
                            </Field>
                        </div>

                        <div className="grid gap-4 sm:grid-cols-2">
                            <Field>
                                <FieldLabel htmlFor="sub2api-key">访问凭据 (Token / Secret / Session Key)</FieldLabel>
                                <Input
                                    id="sub2api-key"
                                    type="password"
                                    placeholder="可选，若反代需要鉴权请填写"
                                    value={apiKey}
                                    onChange={(e) => setApiKey(e.target.value)}
                                    className="rounded-xl"
                                />
                            </Field>

                            <Field>
                                <FieldLabel htmlFor="sub2api-notes">备注与说明</FieldLabel>
                                <Input
                                    id="sub2api-notes"
                                    placeholder="配额窗口、拼车团队或出口节点说明"
                                    value={notes}
                                    onChange={(e) => setNotes(e.target.value)}
                                    className="rounded-xl"
                                />
                            </Field>
                        </div>

                        <div className="flex items-center justify-between pt-2">
                            <label className="flex items-center gap-2 cursor-pointer text-xs text-muted-foreground hover:text-foreground">
                                <input
                                    type="checkbox"
                                    checked={syncChannel}
                                    onChange={(e) => setSyncChannel(e.target.checked)}
                                    className="rounded border-border text-primary focus:ring-primary size-4"
                                />
                                <span>自动物化注册为系统渠道（同步接入路由分发与故障转移）</span>
                            </label>
                            <Button type="submit" disabled={createMutation.isPending} className="rounded-xl px-5">
                                {createMutation.isPending ? <Loader2 className="mr-2 size-4 animate-spin" /> : <Plus className="mr-2 size-4" />}
                                添加反代账号
                            </Button>
                        </div>
                    </FieldGroup>
                </form>
            </section>

            <section>
                <div className="mb-4 flex items-center justify-between">
                    <div>
                        <h3 className="font-bold text-lg">已配置的 Sub2API 反代服务</h3>
                        <p className="text-xs text-muted-foreground">已接入的订阅反代与网关账号列表，状态实时同步</p>
                    </div>
                </div>

                {isLoading && (
                    <div className="flex h-36 items-center justify-center rounded-2xl border border-dashed border-border">
                        <Loader2 className="size-6 animate-spin text-muted-foreground" />
                    </div>
                )}

                {error && (
                    <div className="rounded-2xl border border-destructive/20 bg-destructive/5 p-4 text-sm text-destructive">
                        加载反代账号失败: {error.message}
                    </div>
                )}

                {!isLoading && (!accounts || accounts.length === 0) && (
                    <div className="flex h-40 flex-col items-center justify-center rounded-2xl border border-dashed border-border p-6 text-center">
                        <Server className="size-8 text-muted-foreground/50 mb-2" />
                        <p className="text-sm font-medium text-foreground">暂无 Sub2API 订阅反代账号</p>
                        <p className="mt-1 text-xs text-muted-foreground">在上方表单添加你的首个反代网关或订阅账号</p>
                    </div>
                )}

                {accounts && accounts.length > 0 && (
                    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
                        {accounts.map((acct) => (
                            <Sub2APIAccountCard key={acct.id} account={acct} />
                        ))}
                    </div>
                )}
            </section>
        </div>
    );
}

export function Account() {
    return (
        <div className="flex h-full min-h-0 flex-1 flex-col overflow-y-auto px-4 py-6">
            <div className="container mx-auto max-w-6xl">
                <Sub2APIAccountsPanel />
            </div>
        </div>
    );
}
