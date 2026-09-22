import { useMutation, useQuery } from '@tanstack/react-query';
import { apiRequest } from './client';

/**
 * WebDAV 云备份（T-backup-001）。
 *
 * 口令**不走这里**：它只从服务端环境变量 OCTOPUS_WEBDAV_PASSWORD 读。
 * 设置表会随导出转储与备份一起流转，把口令写进去等于把它复制到每一个备份里。
 */
export interface WebDAVBackupResult {
    /** 上传成功的远端文件名。 */
    file: string;
    /** 本次按保留份数清理掉的旧文件。 */
    pruned: string[];
    /** 清理失败时的原因；备份本身已成功，所以它是提示而不是错误。 */
    warning?: string;
}

export interface WebDAVBackupList {
    files: string[];
    count: number;
}

/** 立即跑一轮云备份。 */
export function useRunWebDAVBackup() {
    return useMutation({
        mutationFn: () =>
            apiRequest<WebDAVBackupResult>('/api/v1/backup/webdav/run', { method: 'POST', body: {} }),
    });
}

/** 列出远端已有的备份文件。 */
export function useWebDAVBackupList(enabled: boolean) {
    return useQuery({
        queryKey: ['webdav-backup-list'],
        queryFn: () => apiRequest<WebDAVBackupList>('/api/v1/backup/webdav/list'),
        enabled,
        retry: false,
    });
}
