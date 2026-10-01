import { useMutation, useQuery } from '@tanstack/react-query';
import { apiRequest, queryClient } from './client';

/**
 * 模型名智能重写（吸收上游 lingyuins/octopus 与 New-API 的共同做法）
 *
 * 解决的问题：客户端（Claude Code / Codex / Cherry Studio 等）常写死带版本后缀的模型名
 * （claude-3-5-sonnet-20241022、gpt-4o-2024-11-20），而本地分组名是简名（claude-sonnet、gpt-4o），
 * 客户端因此拿到 model not found —— 只能为每个版本后缀各建一个分组。
 *
 * 语义只有一条：规则命中客户端的请求模型名时改写成目标分组名，再按改写后的名字找分组。
 * **未命中任何规则时逐字保持原名**，所以没配规则的项目行为与改造前完全一致。
 */
export type ModelMatchType = 'exact' | 'wildcard' | 'regex';

export interface ModelMapping {
    id: number;
    name: string;
    pattern: string;
    match_type: ModelMatchType;
    target_model: string;
    priority: number;
    enabled: boolean;
    created_at?: string;
    updated_at?: string;
}

export interface ModelMappingInput {
    name: string;
    pattern: string;
    match_type: ModelMatchType;
    target_model: string;
    priority: number;
    enabled: boolean;
}

/** 用当前生效的规则集重写一个模型名的结果（面板上的「试一下」）。 */
export interface ModelMappingTestResult {
    original_model: string;
    target_model: string;
    matched: boolean;
    matched_rule?: ModelMapping;
}

export const modelMappingQueryOptions = {
    queryKey: ['model-mapping', 'list'],
    queryFn: () => apiRequest<ModelMapping[]>('/api/v1/model-mapping/list'),
};

export function useModelMappings(enabled = true) {
    return useQuery({ ...modelMappingQueryOptions, enabled });
}

function invalidate() {
    queryClient.invalidateQueries({ queryKey: ['model-mapping', 'list'] });
}

export function useCreateModelMapping() {
    return useMutation({
        mutationFn: (input: ModelMappingInput) =>
            apiRequest<ModelMapping>('/api/v1/model-mapping/create', { method: 'POST', body: input }),
        onSuccess: invalidate,
    });
}

export function useUpdateModelMapping() {
    return useMutation({
        mutationFn: ({ id, ...input }: ModelMappingInput & { id: number }) =>
            apiRequest<ModelMapping>(`/api/v1/model-mapping/update/${id}`, { method: 'POST', body: input }),
        onSuccess: invalidate,
    });
}

export function useDeleteModelMapping() {
    return useMutation({
        mutationFn: (id: number) => apiRequest<unknown>(`/api/v1/model-mapping/delete/${id}`, { method: 'DELETE' }),
        onSuccess: invalidate,
    });
}

export function useToggleModelMapping() {
    return useMutation({
        mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) =>
            apiRequest<ModelMapping>(`/api/v1/model-mapping/toggle/${id}`, { method: 'POST', body: { enabled } }),
        onSuccess: invalidate,
    });
}

/** 用当前规则集试跑一个模型名（保存后的效果预览）。 */
export function useTestModelMapping() {
    return useMutation({
        mutationFn: (modelName: string) =>
            apiRequest<ModelMappingTestResult>('/api/v1/model-mapping/test', {
                method: 'POST',
                body: { model_name: modelName },
            }),
    });
}

/** 在保存前试跑一条还没落库的规则，回答「这条规则会不会误伤」。 */
export function useDryRunModelMapping() {
    return useMutation({
        mutationFn: (input: { match_type: ModelMatchType; pattern: string; model_name: string }) =>
            apiRequest<{ matched: boolean }>('/api/v1/model-mapping/dry-run', { method: 'POST', body: input }),
    });
}
