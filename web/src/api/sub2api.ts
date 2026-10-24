import { queryOptions, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from './client';

export type Sub2APIPlatform =
    | 'anthropic'
    | 'openai'
    | 'gemini'
    | 'antigravity'
    | 'grok'
    | 'kimi'
    | 'deepseek'
    | 'opencode_go'
    | 'cline';

export type Sub2APIAccountType = 'oauth' | 'apikey' | 'setup-token';

export interface Sub2APIAccount {
    id: number;
    name: string;
    platform: Sub2APIPlatform;
    account_type: Sub2APIAccountType;
    base_url: string;
    account_mode: string;
    api_protocol: string;
    enabled: boolean;
    proxy_node_id: number;
    rate_limit: number;
    extra: string;
    notes?: string;
    channel_id?: number;
    last_sync_summary?: string;
    created_at: string;
    updated_at: string;
}

export interface Sub2APIAccountCreatePayload {
    name: string;
    platform: Sub2APIPlatform;
    account_type?: Sub2APIAccountType;
    base_url: string;
    account_mode?: string;
    api_protocol?: string;
    enabled?: boolean;
    proxy_node_id?: number;
    rate_limit?: number;
    api_key?: string;
    headers?: Record<string, string>;
    notes?: string;
    sync_channel?: boolean;
}

export const sub2apiAccountListQueryKey = ['sub2api', 'accounts', 'list'] as const;

export const sub2apiAccountListQueryOptions = queryOptions({
    queryKey: sub2apiAccountListQueryKey,
    queryFn: () => apiRequest<Sub2APIAccount[]>('/api/v1/sub2api/accounts'),
});

export function useSub2APIAccounts() {
    return useQuery({
        ...sub2apiAccountListQueryOptions,
        refetchInterval: 30000,
        refetchOnMount: 'always',
    });
}

export function useCreateSub2APIAccount() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (payload: Sub2APIAccountCreatePayload) =>
            apiRequest<Sub2APIAccount>('/api/v1/sub2api/accounts', {
                method: 'POST',
                body: payload,
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: sub2apiAccountListQueryKey });
            void queryClient.invalidateQueries({ queryKey: ['channel', 'list'] });
            void queryClient.invalidateQueries({ queryKey: ['group', 'list'] });
        },
    });
}

export function useDeleteSub2APIAccount() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<{ id: number; deleted: boolean }>(`/api/v1/sub2api/accounts/${id}`, {
                method: 'DELETE',
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: sub2apiAccountListQueryKey });
            void queryClient.invalidateQueries({ queryKey: ['channel', 'list'] });
            void queryClient.invalidateQueries({ queryKey: ['group', 'list'] });
        },
    });
}

export function useProbeSub2APIAccount() {
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<{ id: number; status: string }>(`/api/v1/sub2api/accounts/${id}/probe`, {
                method: 'POST',
            }),
    });
}

export function useCleanSub2APIStaleModels() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<{ id: number; cleaned: boolean; removed: string[]; removed_count: number }>(
                `/api/v1/sub2api/accounts/${id}/clean-stale`,
                { method: 'POST' }
            ),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: sub2apiAccountListQueryKey });
            void queryClient.invalidateQueries({ queryKey: ['channel', 'list'] });
            void queryClient.invalidateQueries({ queryKey: ['group', 'list'] });
        },
    });
}

export function useUpdateSub2APIAccount() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: ({ id, payload }: { id: number; payload: Partial<Sub2APIAccountCreatePayload> }) =>
            apiRequest<Sub2APIAccount>(`/api/v1/sub2api/accounts/${id}`, {
                method: 'PUT',
                body: payload,
            }),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: sub2apiAccountListQueryKey });
            void queryClient.invalidateQueries({ queryKey: ['channel', 'list'] });
            void queryClient.invalidateQueries({ queryKey: ['group', 'list'] });
        },
    });
}

export function useSyncSub2APIModels() {
    const queryClient = useQueryClient();
    return useMutation({
        mutationFn: (id: number) =>
            apiRequest<{
                id: number;
                synced: boolean;
                models: string[];
                count: number;
                added: string[];
                added_count: number;
                stale: string[];
                stale_count: number;
            }>(
                `/api/v1/sub2api/accounts/${id}/sync-models`,
                { method: 'POST' }
            ),
        onSuccess: () => {
            void queryClient.invalidateQueries({ queryKey: sub2apiAccountListQueryKey });
            void queryClient.invalidateQueries({ queryKey: ['channel', 'list'] });
            void queryClient.invalidateQueries({ queryKey: ['group', 'list'] });
        },
    });
}
