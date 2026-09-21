import { queryOptions } from '@tanstack/react-query';
import { apiRequest } from './client';

// R-ext-001 「扩展」页的前端客户端：把 octopus 开放给社区/高级用户的三类扩展统一到一个入口。
//
// 三类扩展各自已经有完整的后端契约（插件托管 / 账号级采集凭据 / 号池声明式适配器），
// 这一页不新增能力，只把它们**摆到用户能看见、能配置的地方** —— 用户要的"软件内的软件"。
//
// 为什么放在一起：它们的共同点是"不改主程序就能接新东西"，排查问题时也常常互为因果
// （插件要出口、采集凭据要反代、适配器要主机白名单），分成三个一级菜单反而让人来回跳。

export type PluginStatus = {
    slug: string;
    name?: string;
    version?: string;
    runtime: string;
    enabled: boolean;
    auto_start: boolean;
    auto_channel: boolean;
    protocol?: string;
    entry: string;
    port: number;
    base_path?: string;
    health_path?: string;
    endpoint?: string;
    running: boolean;
    status: string;
    pid: number;
    egress_mode?: string;
    egress_node_id?: number;
    egress_proxy?: string;
    egress_note?: string;
    channel_id?: number;
    dir?: string;
    log_tail?: string;
    last_error?: string;
};

export type PluginListPayload = { items: PluginStatus[]; total?: number; dir?: string; failures?: Record<string, string> };

export type CredentialSource = {
    id: number;
    name: string;
    kind: string; // login（交互登录）/ pack（采集包）
    site: string;
    channel_id: number;
    enabled: boolean;
    auto_refresh: boolean;
    units: string; // usd / points
    status: string; // pending / authorized / failed
    username: string;
    hosts: string;
    proxy_node_id: number; // 0 = 直连（真实 IP 会暴露给站点）
    last_read_at?: string;
    last_error?: string;
    last_balance?: number;
    last_used?: number;
    last_quota?: number;
    last_currency?: string;
    note?: string;
    pack_text?: string;
    has_credentials?: boolean;
    has_session?: boolean;
    login_url?: string;
    steps?: string[];
};

export type CredentialSourceListPayload = { items: CredentialSource[]; total: number };

export type PoolAdapterItem = {
    kind: string;
    title?: string;
    enabled?: boolean;
    capabilities?: string[];
    base_url?: string;
    note?: string;
};

export type PoolAdapterPayload = {
    items: PoolAdapterItem[];
    total: number;
    hosts: string;
    allowed_hosts: string[] | null;
    read_only_capable: boolean;
    declarative_only_ops: string[];
};

export type PoolTemplate = { kind: string; title: string; note?: string; capabilities?: string[]; spec: unknown };

export const pluginsQueryOptions = queryOptions({
    queryKey: ['extensions-plugins'],
    // 插件的 running/pid/端口是"现在这一刻"的事实，不做缓存复用。
    queryFn: () => apiRequest<PluginListPayload>('/api/v1/plugin/list'),
    staleTime: 0,
});

export const credentialSourcesQueryOptions = queryOptions({
    queryKey: ['extensions-collector-sources'],
    queryFn: () => apiRequest<CredentialSourceListPayload>('/api/v1/collector/sources'),
    staleTime: 5_000,
});

export const poolAdaptersQueryOptions = queryOptions({
    queryKey: ['extensions-pool-adapters'],
    queryFn: () => apiRequest<PoolAdapterPayload>('/api/v1/pool/adapters'),
    staleTime: 10_000,
});

export const poolTemplatesQueryOptions = queryOptions({
    queryKey: ['extensions-pool-templates'],
    queryFn: () => apiRequest<{ items: PoolTemplate[]; total?: number }>('/api/v1/pool/adapters/templates'),
    staleTime: 60_000,
});

export function scanPlugins() {
    return apiRequest<PluginListPayload>('/api/v1/plugin/scan', { method: 'POST' });
}

export function startPlugin(slug: string) {
    return apiRequest<{ item: PluginStatus }>(`/api/v1/plugin/${slug}/start`, { method: 'POST' });
}

export function stopPlugin(slug: string) {
    return apiRequest<{ item?: PluginStatus }>(`/api/v1/plugin/${slug}/stop`, { method: 'POST' });
}

export function updatePlugin(slug: string, input: { enabled?: boolean; auto_start?: boolean; egress_node_id?: number }) {
    return apiRequest<{ item: PluginStatus }>(`/api/v1/plugin/${slug}`, { method: 'PUT', body: input });
}

export function removePlugin(slug: string) {
    return apiRequest<Record<string, never>>(`/api/v1/plugin/${slug}`, { method: 'DELETE' });
}

export function createCredentialSource(input: {
    name: string;
    kind: string;
    site?: string;
    channel_id?: number;
    pack?: string;
    proxy_node_id?: number;
    username?: string;
    password?: string;
}) {
    return apiRequest<{ item: CredentialSource }>('/api/v1/collector/sources', { method: 'POST', body: input });
}

export function updateCredentialSource(id: number, input: { name?: string; channel_id?: number; enabled?: boolean; auto_refresh?: boolean; pack?: string; proxy_node_id?: number }) {
    return apiRequest<{ item: CredentialSource }>(`/api/v1/collector/sources/${id}`, { method: 'PUT', body: input });
}

export function removeCredentialSource(id: number) {
    return apiRequest<Record<string, never>>(`/api/v1/collector/sources/${id}`, { method: 'DELETE' });
}

export function runCredentialSource(id: number) {
    return apiRequest<{ ok: boolean; reason?: string; message?: string; steps?: string[]; item: CredentialSource }>(
        `/api/v1/collector/sources/${id}/run`,
        { method: 'POST' },
    );
}

// startCollectorLogin 返回"登录临时地址"：在那个地址里登录，会话由 octopus 服务端捕获。
export function startCollectorLogin(id: number) {
    return apiRequest<{ item: CredentialSource; login_url: string }>(`/api/v1/collector/sources/${id}/login`, { method: 'POST' });
}

export function finishCollectorLogin(id: number) {
    return apiRequest<{ item: CredentialSource }>(`/api/v1/collector/sources/${id}/login/finish`, { method: 'POST' });
}

export function clearCollectorSession(id: number) {
    return apiRequest<{ item: CredentialSource }>(`/api/v1/collector/sources/${id}/session`, { method: 'DELETE' });
}

export function registerPoolAdapter(spec: string) {
    return apiRequest<{ item: PoolAdapterItem }>('/api/v1/pool/adapters', { method: 'POST', body: { spec } });
}

export function removePoolAdapter(kind: string) {
    return apiRequest<Record<string, never>>(`/api/v1/pool/adapters/${kind}`, { method: 'DELETE' });
}

// SiteTemplate 是一条内置站点类型（new-api / one-api / sub2api / litellm）。
// 每类有 form 与 interactive 两版：前者直接 POST 登录，后者走反代登录页（验证码场景）。
export type SiteTemplate = {
    kind: string;
    title: string;
    note: string;
    units: string;
    mode: string; // form / interactive
    need_captcha: boolean;
    paths: string[];
    fields: string[];
};

export const siteTemplatesQueryOptions = queryOptions({
    queryKey: ['extensions-site-templates'],
    queryFn: () => apiRequest<{ items: SiteTemplate[]; total: number; kinds: string[] }>('/api/v1/collector/templates'),
    staleTime: 300_000,
});

// renderSiteTemplate 让后端按"站点类型 + 你填的网址"生成一份采集包（后端生成即过装载门禁）。
export function renderSiteTemplate(input: { kind: string; base_url: string; mode: string }) {
    return apiRequest<{ pack: string; kind: string; mode: string; units: string }>('/api/v1/collector/templates/render', {
        method: 'POST',
        body: input,
    });
}
