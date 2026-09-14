import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

// 账号能力（T-acct-001 官方账号 OAuth / T-acct-005 手动登录跳转页）的前端数据层。
// 类型严格对齐后端 model JSON：凭据密文/明文都不出现在任何响应里（json:"-"），
// 这里能拿到的只有账号元数据（档位/窗口/健康/错误），跳转令牌明文只在创建响应出现一次。

export type OfficialProvider = 'openai' | 'gemini' | 'claude';
export type OfficialStatus = 'pending' | 'active' | 'expired' | 'revoked' | 'error';

export interface OfficialAccount {
    id: number;
    provider: OfficialProvider;
    external_name: string;
    status: OfficialStatus;
    expires_at: string | null;
    plan_tier: string;
    window_5h: string;
    window_7d: string;
    healthy: boolean;
    last_checked_at: string | null;
    last_error: string;
    created_at: string;
    updated_at: string;
}

// authorize 响应：账号行 + 授权链接 + 一次性 state（callback 需回填）。
export interface AuthorizeResult {
    account: OfficialAccount;
    authorize_url: string;
    state: string;
    expires_in: number;
}

export interface OfficialAccountListResult {
    items: OfficialAccount[];
    total: number;
}

export interface CallbackPayload {
    account_id: number;
    one_time_code: string;
    state: string;
}

export type JumpKind = 'na' | 's2';

export interface JumpToken {
    id: number;
    created_at: string;
    kind: JumpKind;
    target_url: string;
    actor: string;
    expires_at: string;
    consumed_at: string | null;
    note: string;
}

// create 响应只在此处回显一次令牌明文；列表行里永远拿不到。
export interface CreateJumpResult {
    token: string;
    expires_at: string;
    kind: JumpKind;
}

export interface JumpTokenListResult {
    items: JumpToken[];
    total: number;
}

export const officialAccountListQueryKey = ['account', 'official', 'list'] as const;
export const jumpTokenListQueryKey = ['account', 'jump', 'list'] as const;

export const officialAccountListQueryOptions = queryOptions({
    queryKey: officialAccountListQueryKey,
    queryFn: () => apiRequest<OfficialAccountListResult>('/api/v1/account/official/list'),
});

export const jumpTokenListQueryOptions = queryOptions({
    queryKey: jumpTokenListQueryKey,
    queryFn: () => apiRequest<JumpTokenListResult>('/api/v1/account/jump/list'),
});

export function useOfficialAccounts() {
    return useQuery({ ...officialAccountListQueryOptions, refetchInterval: 30000, refetchOnMount: 'always' });
}

export function useJumpTokens() {
    return useQuery({ ...jumpTokenListQueryOptions, refetchOnMount: 'always' });
}

// 发起官方账号授权：建 pending 行并返回授权链接 + 一次性 state。
export function useAuthorizeOfficialAccount() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (provider: OfficialProvider) =>
            apiRequest<AuthorizeResult>('/api/v1/account/official/authorize', { method: 'POST', body: { provider } }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: officialAccountListQueryKey }),
    });
}

// 回调确认：以一次性 code + state 换取凭据并密文落库，成功即 active。
export function useConfirmOfficialAccount() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (payload: CallbackPayload) =>
            apiRequest<OfficialAccount>('/api/v1/account/official/callback', { method: 'POST', body: payload }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: officialAccountListQueryKey }),
    });
}

// 手动刷新单个账号的套餐/健康/窗口快照（回填后返回最新账号行）。
export function useRefreshOfficialAccountUsage() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<OfficialAccount>(`/api/v1/account/official/usage/${id}`, { method: 'POST', body: {} }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: officialAccountListQueryKey }),
    });
}

// 创建一次性登录跳转令牌：明文只回显一次，列表行仅含哈希后的审计信息。
export function useCreateJumpToken() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (payload: { kind: JumpKind; target_url: string }) =>
            apiRequest<CreateJumpResult>('/api/v1/account/jump/create', { method: 'POST', body: payload }),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: jumpTokenListQueryKey }),
    });
}
