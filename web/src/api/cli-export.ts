import { useMutation } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * CLI 配置导出（吸收上游 lingyuins/octopus 的 CLI Config Export）
 *
 * 解决的问题：把网关接好之后还得知道"客户端该怎么配"——base URL 填什么、key 填哪把、
 * model 填什么。这三样在面板上分散在三处，而各客户端要的格式又不同。
 *
 * 安全：导出内容**含 API Key 明文**（那正是它有用的原因），所以接口挂在管理面鉴权之内。
 */
export type CLITarget = 'claude_code' | 'codex' | 'gemini_cli' | 'cherry_studio' | 'openai_compatible';

export interface CLIExport {
    tool: string;
    title: string;
    format: string;
    filename: string;
    content: string;
    description: string;
    steps: string[] | null;
    notes: string[] | null;
}

export interface CLIExportInput {
    tool: CLITarget;
    base_url: string;
    model: string;
    /** 只给 key 的 id，由服务端取明文——省得为了导出一份配置还得先复制一遍 key。 */
    api_key_id?: number;
    api_key?: string;
}

export function useGenerateCLIExport() {
    return useMutation({
        mutationFn: (input: CLIExportInput) =>
            apiRequest<CLIExport>('/api/v1/cli-export/generate', { method: 'POST', body: input }),
    });
}
