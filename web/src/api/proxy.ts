import { queryOptions } from '@tanstack/react-query';
import { apiRequest } from './client';

// R-proxy-001 代理节点池的前端客户端。
//
// 这里的页面只做两件用户看得见的事：看"我有哪些出口节点"和"给某个账号指定从哪个节点出去"。
// 出口节点的本地端口由内核分配（40xxx 段的冷门端口），页面只展示，不允许手改——
// 手改端口与内核实际入站对不上时，请求会静默走直连，那正是这个功能要防的事。

// ProxyNode 是一条出口节点：一条 Clash 节点记录 + 它的本地入站端口与最近出口 IP。
export type ProxyNode = {
    id: number;
    name: string;
    type: string;
    server: string;
    port: number;
    source: string;
    sub_id: number;
    local_port: number; // 0 表示内核还没给它分配出口端口
    enabled: boolean;
    last_probe_at?: string;
    last_probe_ok?: boolean;
    last_exit_ip?: string;
    last_error?: string;
    created_at?: string;
    updated_at?: string;
};

// ProxySubscription 是一条订阅来源；订阅地址只回显打码后的提示，绝不带凭据。
export type ProxySubscription = {
    id: number;
    name: string;
    url_hint: string;
    enabled: boolean;
    auto_refresh: boolean;
    node_count: number;
    info_note?: string;
    last_fetch_at?: string;
    last_error?: string;
};

// ProxyImportResult 是一次导入/刷新的结果，用来显示"新增几个、更新几个、跳过几个"。
export type ProxyImportResult = {
    added: number;
    updated: number;
    skipped: number;
    infos?: string[];
    message?: string;
};

// ProxyCoreStatus 是内核现状：它是所有出口的共同前置，页面顶部常驻显示。
export type ProxyCoreStatus = {
    running: boolean;
    pid: number;
    binary: string;
    binary_ok: boolean;
    config_path: string;
    log_path: string;
    listeners: number;
    ports: number[];
    last_error?: string;
    version?: string;
    started_at?: string;
};

type NodeListPayload = { items: ProxyNode[]; total: number; usage?: Record<string, string[]> };
type SubscriptionListPayload = { items: ProxySubscription[]; total: number };

export const proxyNodesQueryOptions = queryOptions({
    queryKey: ['proxy-nodes'],
    queryFn: () => apiRequest<NodeListPayload>('/api/v1/proxy/nodes'),
    staleTime: 15_000,
});

export const proxySubscriptionsQueryOptions = queryOptions({
    queryKey: ['proxy-subscriptions'],
    queryFn: () => apiRequest<SubscriptionListPayload>('/api/v1/proxy/subscriptions'),
    staleTime: 15_000,
});

export const proxyCoreStatusQueryOptions = queryOptions({
    queryKey: ['proxy-core-status'],
    // 内核状态带 pid 与端口，是"现在这一刻"的事实，不做缓存复用。
    queryFn: () => apiRequest<ProxyCoreStatus>('/api/v1/proxy/core/status'),
    staleTime: 0,
});

export function createProxyNode(input: { name: string; type: string; server: string; port: number; params?: string; enabled?: boolean }) {
    return apiRequest<{ item: ProxyNode }>('/api/v1/proxy/nodes', { method: 'POST', body: input });
}

export function updateProxyNode(id: number, input: { name: string; type: string; server: string; port: number; params?: string; enabled?: boolean }) {
    return apiRequest<{ item: ProxyNode }>(`/api/v1/proxy/nodes/${id}`, { method: 'PUT', body: input });
}

export function deleteProxyNode(id: number) {
    return apiRequest<{ id: number; removed: boolean }>(`/api/v1/proxy/nodes/${id}`, { method: 'DELETE' });
}

// probeProxyNode 让内核从该节点出去请求一次出口 IP 探测地址，返回看到的 IP。
// 这是"这个节点到底通不通、出口是谁"的判据：探测失败说明节点自身不可用（与 octopus 无关）。
export function probeProxyNode(id: number) {
    return apiRequest<{ exit_ip: string; probe_url: string }>(`/api/v1/proxy/nodes/${id}/probe`, { method: 'POST' });
}

// importProxyNodesYAML 导入一份 Clash 配置里的 proxies（订阅文件或本地 yaml 的原文）。
export function importProxyNodesYAML(raw: string, source = 'manual') {
    return apiRequest<ProxyImportResult>('/api/v1/proxy/nodes/import', { method: 'POST', body: { raw, source } });
}

export function createProxySubscription(input: { name: string; url: string; enabled?: boolean; auto_refresh?: boolean }) {
    return apiRequest<{ item: ProxySubscription }>('/api/v1/proxy/subscriptions', { method: 'POST', body: input });
}

export function deleteProxySubscription(id: number) {
    return apiRequest<{ id: number; removed: boolean }>(`/api/v1/proxy/subscriptions/${id}`, { method: 'DELETE' });
}

export function refreshProxySubscription(id: number) {
    return apiRequest<ProxyImportResult>(`/api/v1/proxy/subscriptions/${id}/refresh`, { method: 'POST' });
}

export function startProxyCore() {
    return apiRequest<ProxyCoreStatus>('/api/v1/proxy/core/start', { method: 'POST' });
}

export function stopProxyCore() {
    return apiRequest<ProxyCoreStatus>('/api/v1/proxy/core/stop', { method: 'POST' });
}

// syncProxyCore 按节点池现状重建内核配置并重启（导入/启停节点后必须调用，否则端口与配置对不上）。
export function syncProxyCore() {
    return apiRequest<ProxyCoreStatus>('/api/v1/proxy/core/sync', { method: 'POST' });
}

export function allocateProxyPorts() {
    return apiRequest<{ assigned: number; released: number; port_start: number; port_end: number }>('/api/v1/proxy/core/ports', { method: 'POST' });
}
